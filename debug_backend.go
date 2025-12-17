package main

import (
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"time"
)

// DebugBackend wraps any CacheBackend and adds debug logging.
// This allows any backend implementation to have debug logging without
// coupling the debug logic to the backend implementation.
type DebugBackend struct {
	backend CacheBackend
}

// NewDebugBackend creates a new debug wrapper around an existing backend.
func NewDebugBackend(backend CacheBackend) *DebugBackend {
	return &DebugBackend{
		backend: backend,
	}
}

// Put stores an object in the cache with debug logging.
func (d *DebugBackend) Put(actionID, outputID []byte, body io.Reader, bodySize int64) (string, error) {
	fmt.Fprintf(os.Stderr, "[DEBUG] Put: actionID=%s, outputID=%s, size=%d\n",
		hex.EncodeToString(actionID), hex.EncodeToString(outputID), bodySize)

	diskPath, err := d.backend.Put(actionID, outputID, body, bodySize)

	if err != nil {
		fmt.Fprintf(os.Stderr, "[DEBUG] Put: ERROR: %v\n", err)
		return diskPath, err
	}

	fmt.Fprintf(os.Stderr, "[DEBUG] Put: stored at %s\n", diskPath)
	return diskPath, nil
}

// Get retrieves an object from the cache with debug logging.
func (d *DebugBackend) Get(actionID []byte) ([]byte, string, int64, *time.Time, bool, error) {
	fmt.Fprintf(os.Stderr, "[DEBUG] Get: actionID=%s\n", hex.EncodeToString(actionID))

	outputID, diskPath, size, putTime, miss, err := d.backend.Get(actionID)

	if err != nil {
		fmt.Fprintf(os.Stderr, "[DEBUG] Get: ERROR: %v\n", err)
		return outputID, diskPath, size, putTime, miss, err
	}

	if miss {
		fmt.Fprintf(os.Stderr, "[DEBUG] Get: MISS\n")
	} else {
		fmt.Fprintf(os.Stderr, "[DEBUG] Get: HIT at %s, outputID=%s, size=%d\n",
			diskPath, hex.EncodeToString(outputID), size)
	}

	return outputID, diskPath, size, putTime, miss, err
}

// Close performs cleanup operations with debug logging.
func (d *DebugBackend) Close() error {
	fmt.Fprintf(os.Stderr, "[DEBUG] Close: closing backend\n")

	err := d.backend.Close()

	if err != nil {
		fmt.Fprintf(os.Stderr, "[DEBUG] Close: ERROR: %v\n", err)
	}

	return err
}

// Clear removes all entries from the cache with debug logging.
func (d *DebugBackend) Clear() error {
	fmt.Fprintf(os.Stderr, "[DEBUG] Clear: clearing cache\n")

	err := d.backend.Clear()

	if err != nil {
		fmt.Fprintf(os.Stderr, "[DEBUG] Clear: ERROR: %v\n", err)
		return err
	}

	fmt.Fprintf(os.Stderr, "[DEBUG] Clear: cache cleared successfully\n")
	return nil
}
