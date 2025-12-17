package backends

import (
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"time"
)

// Debug wraps any Backend and adds debug logging.
// This allows any backend implementation to have debug logging without
// coupling the debug logic to the backend implementation.
type Debug struct {
	backend Backend
}

// NewDebug creates a new debug wrapper around an existing backend.
func NewDebug(backend Backend) *Debug {
	return &Debug{
		backend: backend,
	}
}

// Put stores an object in the cache with debug logging.
func (d *Debug) Put(actionID, outputID []byte, body io.Reader, bodySize int64) (string, error) {
	fmt.Fprintf(os.Stderr, "[DEBUG] Put: actionID=%s, outputID=%s, size=%d\n",
		hex.EncodeToString(actionID), hex.EncodeToString(outputID), bodySize)

	start := time.Now()
	diskPath, err := d.backend.Put(actionID, outputID, body, bodySize)
	duration := time.Since(start)

	if err != nil {
		fmt.Fprintf(os.Stderr, "[DEBUG] Put: ERROR: %v (duration: %v)\n", err, duration)
		return diskPath, err
	}

	fmt.Fprintf(os.Stderr, "[DEBUG] Put: stored at %s (duration: %v)\n", diskPath, duration)
	return diskPath, nil
}

// Get retrieves an object from the cache with debug logging.
func (d *Debug) Get(actionID []byte) ([]byte, string, int64, *time.Time, bool, error) {
	fmt.Fprintf(os.Stderr, "[DEBUG] Get: actionID=%s\n", hex.EncodeToString(actionID))

	start := time.Now()
	outputID, diskPath, size, putTime, miss, err := d.backend.Get(actionID)
	duration := time.Since(start)

	if err != nil {
		fmt.Fprintf(os.Stderr, "[DEBUG] Get: ERROR: %v (duration: %v)\n", err, duration)
		return outputID, diskPath, size, putTime, miss, err
	}

	if miss {
		fmt.Fprintf(os.Stderr, "[DEBUG] Get: MISS (duration: %v)\n", duration)
	} else {
		fmt.Fprintf(os.Stderr, "[DEBUG] Get: HIT at %s, outputID=%s, size=%d (duration: %v)\n",
			diskPath, hex.EncodeToString(outputID), size, duration)
	}

	return outputID, diskPath, size, putTime, miss, err
}

// Close performs cleanup operations with debug logging.
func (d *Debug) Close() error {
	fmt.Fprintf(os.Stderr, "[DEBUG] Close: closing backend\n")

	start := time.Now()
	err := d.backend.Close()
	duration := time.Since(start)

	if err != nil {
		fmt.Fprintf(os.Stderr, "[DEBUG] Close: ERROR: %v (duration: %v)\n", err, duration)
	} else {
		fmt.Fprintf(os.Stderr, "[DEBUG] Close: completed (duration: %v)\n", duration)
	}

	return err
}

// Clear removes all entries from the cache with debug logging.
func (d *Debug) Clear() error {
	fmt.Fprintf(os.Stderr, "[DEBUG] Clear: clearing cache\n")

	start := time.Now()
	err := d.backend.Clear()
	duration := time.Since(start)

	if err != nil {
		fmt.Fprintf(os.Stderr, "[DEBUG] Clear: ERROR: %v (duration: %v)\n", err, duration)
		return err
	}

	fmt.Fprintf(os.Stderr, "[DEBUG] Clear: cache cleared successfully (duration: %v)\n", duration)
	return nil
}

