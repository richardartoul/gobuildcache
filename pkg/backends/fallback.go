package backends

import (
	"io"
	"log/slog"
	"sync/atomic"
	"time"
)

// Fallback wraps a Backend so that backend failures never fail a build.
//
// A failed Get becomes a miss and a failed Put is dropped, so Go rebuilds
// the action instead of seeing an error. After maxConsecutiveErrors failures
// in a row, Fallback stops calling the backend for the rest of the process
// and serves everything from the local cache. A single gobuildcache process
// lives for one go command, so the backend is tried again on the next one.
type Fallback struct {
	backend              Backend
	logger               *slog.Logger
	maxConsecutiveErrors int64

	consecutiveErrors atomic.Int64
	totalErrors       atomic.Int64
	tripped           atomic.Bool
}

// NewFallback wraps backend. maxConsecutiveErrors must be at least 1.
func NewFallback(backend Backend, maxConsecutiveErrors int64, logger *slog.Logger) *Fallback {
	return &Fallback{
		backend:              backend,
		logger:               logger,
		maxConsecutiveErrors: maxConsecutiveErrors,
	}
}

// Put stores an object in the backend, dropping it if the backend fails.
func (f *Fallback) Put(actionID, outputID []byte, body io.Reader, bodySize int64) error {
	if f.tripped.Load() {
		return nil
	}
	f.record(f.backend.Put(actionID, outputID, body, bodySize))
	return nil
}

// Get retrieves an object from the backend, returning a miss if the backend fails.
func (f *Fallback) Get(actionID []byte) ([]byte, io.ReadCloser, int64, *time.Time, bool, error) {
	if f.tripped.Load() {
		return nil, nil, 0, nil, true, nil
	}
	outputID, body, size, putTime, miss, err := f.backend.Get(actionID)
	f.record(err)
	if err != nil {
		return nil, nil, 0, nil, true, nil
	}
	return outputID, body, size, putTime, miss, nil
}

func (f *Fallback) record(err error) {
	if err == nil {
		f.consecutiveErrors.Store(0)
		return
	}
	f.totalErrors.Add(1)
	f.logger.Warn("backend request failed, treating as a cache miss", "error", err)
	if f.consecutiveErrors.Add(1) >= f.maxConsecutiveErrors && f.tripped.CompareAndSwap(false, true) {
		f.logger.Warn("backend failed repeatedly, using only the local cache for the rest of this build",
			"consecutiveErrors", f.maxConsecutiveErrors)
	}
}

// Close closes the underlying backend and reports any failures it hid.
func (f *Fallback) Close() error {
	if n := f.totalErrors.Load(); n > 0 {
		f.logger.Warn("backend failures were treated as cache misses", "errors", n, "localOnly", f.tripped.Load())
	}
	return f.backend.Close()
}

// Clear passes through to the underlying backend, so clearing never silently does nothing.
func (f *Fallback) Clear() error {
	return f.backend.Clear()
}
