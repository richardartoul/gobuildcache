package backends

import (
	"fmt"
	"io"
	"math/rand"
	"sync"
	"sync/atomic"
	"time"
)

// Error wraps any Backend and randomly returns errors based on a configured percentage.
// This is useful for testing error handling and resilience.
type Error struct {
	backend   Backend
	errorRate float64 // Percentage of operations that should fail (0.0 to 1.0)

	rng   *rand.Rand
	rngMu sync.Mutex // Protects rng access (rand.Rand is not thread-safe)

	putErrors   atomic.Int64
	getErrors   atomic.Int64
	closeErrors atomic.Int64
	clearErrors atomic.Int64
}

// NewError creates a new error-injecting wrapper around an existing backend.
// errorRate should be between 0.0 (no errors) and 1.0 (all errors fail).
func NewError(backend Backend, errorRate float64) *Error {
	if errorRate < 0.0 {
		errorRate = 0.0
	}
	if errorRate > 1.0 {
		errorRate = 1.0
	}

	return &Error{
		backend:   backend,
		errorRate: errorRate,
		rng:       rand.New(rand.NewSource(time.Now().UnixNano())),
	}
}

// shouldError returns true if this operation should fail based on the error rate.
// This method is thread-safe.
func (e *Error) shouldError() bool {
	e.rngMu.Lock()
	defer e.rngMu.Unlock()
	return e.rng.Float64() < e.errorRate
}

// Put stores an object in the backend storage, potentially returning an error.
func (e *Error) Put(actionID, outputID []byte, body io.Reader, bodySize int64) error {
	if e.shouldError() {
		e.putErrors.Add(1)
		return fmt.Errorf("error backend: simulated Put error (error rate: %.2f%%)", e.errorRate*100)
	}
	return e.backend.Put(actionID, outputID, body, bodySize)
}

// Get retrieves an object from the backend storage, potentially returning an error.
func (e *Error) Get(actionID []byte) ([]byte, io.ReadCloser, int64, *time.Time, bool, error) {
	if e.shouldError() {
		e.getErrors.Add(1)
		return nil, nil, 0, nil, false, fmt.Errorf("error backend: simulated Get error (error rate: %.2f%%)", e.errorRate*100)
	}
	return e.backend.Get(actionID)
}

// Close performs cleanup operations, potentially returning an error.
func (e *Error) Close() error {
	if e.shouldError() {
		e.closeErrors.Add(1)
		return fmt.Errorf("error backend: simulated Close error (error rate: %.2f%%)", e.errorRate*100)
	}
	return e.backend.Close()
}

// Clear removes all entries from the cache, potentially returning an error.
func (e *Error) Clear() error {
	if e.shouldError() {
		e.clearErrors.Add(1)
		return fmt.Errorf("error backend: simulated Clear error (error rate: %.2f%%)", e.errorRate*100)
	}
	return e.backend.Clear()
}

// GetStats returns the number of errors injected for each operation type.
// This method is thread-safe.
func (e *Error) GetStats() (putErrors, getErrors, closeErrors, clearErrors int64) {
	return e.putErrors.Load(), e.getErrors.Load(), e.closeErrors.Load(), e.clearErrors.Load()
}
