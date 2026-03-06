package backends

import (
	"bytes"
	"fmt"
	"io"
	"log/slog"
	"runtime"
	"sync"
	"sync/atomic"
	"time"
)

// AsyncBackendWriter wraps a Backend and provides asynchronous PUT operations.
// GET operations are still synchronous as they're in the critical path for builds.
// PUT operations spawn a goroutine on demand.
type AsyncBackendWriter struct {
	backend   Backend
	logger    *slog.Logger
	semaphore chan struct{}
	wg        sync.WaitGroup

	// Stats
	startedPuts  atomic.Int64
	failedPuts   atomic.Int64
	successPuts  atomic.Int64
	totalPutTime atomic.Int64 // microseconds
}

func NewAsyncBackendWriter(
	backend Backend,
	logger *slog.Logger,
) *AsyncBackendWriter {
	return &AsyncBackendWriter{
		backend:   backend,
		logger:    logger,
		semaphore: make(chan struct{}, 128*runtime.GOMAXPROCS(0)),
	}
}

// Put spawns a goroutine to execute the PUT operation asynchronously.
// The body is copied to avoid holding references to the original data.
func (abw *AsyncBackendWriter) Put(actionID, outputID []byte, body io.Reader, bodySize int64) error {
	// Try to acquire semaphore slot
	select {
	case abw.semaphore <- struct{}{}:
		// Successfully acquired
	default:
		return fmt.Errorf("too many concurrent PUT operations")
	}

	// Copy the body data since we're processing asynchronously
	bodyData, err := io.ReadAll(body)
	if err != nil {
		<-abw.semaphore // Release semaphore on error
		return fmt.Errorf("failed to read body: %w", err)
	}

	abw.wg.Add(1)
	abw.startedPuts.Add(1)
	go func() {
		defer abw.wg.Done()
		defer func() { <-abw.semaphore }() // Release semaphore when done

		start := time.Now()
		err := abw.backend.Put(actionID, outputID, bytes.NewReader(bodyData), bodySize)
		duration := time.Since(start)

		abw.totalPutTime.Add(int64(duration.Microseconds()))

		if err != nil {
			abw.failedPuts.Add(1)
			abw.logger.Warn("async backend PUT failed",
				"actionID", fmt.Sprintf("%x", actionID[:min(8, len(actionID))]),
				"size", bodySize,
				"duration", duration,
				"error", err)
		} else {
			abw.successPuts.Add(1)
			abw.logger.Debug("async backend PUT succeeded",
				"actionID", fmt.Sprintf("%x", actionID[:min(8, len(actionID))]),
				"size", bodySize,
				"duration", duration)
		}
	}()

	return nil
}

// Get passes through to the underlying backend (synchronous).
// GET operations remain synchronous as they're in the critical path.
func (abw *AsyncBackendWriter) Get(actionID []byte) (outputID []byte, body io.ReadCloser, size int64, putTime *time.Time, miss bool, err error) {
	return abw.backend.Get(actionID)
}

// Close gracefully shuts down the async writer and waits for all in-flight operations to complete.
// It also closes the underlying backend.
func (abw *AsyncBackendWriter) Close() error {
	abw.logger.Debug("shutting down async backend writer",
		"startedPuts", abw.startedPuts.Load(),
		"successPuts", abw.successPuts.Load(),
		"failedPuts", abw.failedPuts.Load())

	// Wait for all in-flight PUTs to finish
	abw.wg.Wait()

	// Close the underlying backend
	err := abw.backend.Close()

	if abw.successPuts.Load() > 0 {
		avgPutTime := time.Duration(abw.totalPutTime.Load()/abw.successPuts.Load()) * time.Microsecond
		abw.logger.Info("async backend writer statistics",
			"avgPutTime", avgPutTime)
	}

	return err
}

// Clear passes through to the underlying backend
func (abw *AsyncBackendWriter) Clear() error {
	return abw.backend.Clear()
}

// Stats returns current statistics about the async writer
func (abw *AsyncBackendWriter) Stats() AsyncBackendStats {
	return AsyncBackendStats{
		StartedPuts:        abw.startedPuts.Load(),
		SuccessPuts:        abw.successPuts.Load(),
		FailedPuts:         abw.failedPuts.Load(),
		TotalPutTimeMicros: abw.totalPutTime.Load(),
	}
}

// AsyncBackendStats holds statistics for the async backend writer
type AsyncBackendStats struct {
	StartedPuts        int64
	SuccessPuts        int64
	FailedPuts         int64
	TotalPutTimeMicros int64
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
