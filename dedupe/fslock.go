package dedupe

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/juju/fslock"
)

// GofslockGroup is a Group implementation that uses filesystem locks for deduplication.
// It uses the juju/fslock library to ensure only one execution happens for a given key
// across all goroutines, with waiters sharing the result of the first execution.
// This implementation is useful for cross-process deduplication or testing.
type GofslockGroup struct {
	lockDir string
	mu      sync.Mutex
	calls   map[string]*fslockCall
}

// fslockCall represents an in-flight or completed call
type fslockCall struct {
	result interface{}
	err    error
	done   chan struct{}
}

// NewGofslockGroup creates a new GofslockGroup.
// lockDir is the directory where lock files will be created.
// If lockDir is empty, it defaults to os.TempDir()/dedupe-locks.
func NewGofslockGroup(lockDir string) (*GofslockGroup, error) {
	if lockDir == "" {
		lockDir = filepath.Join(os.TempDir(), "gobuildcache-dedupe-locks")
	}

	// Ensure lock directory exists
	if err := os.MkdirAll(lockDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create lock directory: %w", err)
	}

	return &GofslockGroup{
		lockDir: lockDir,
		calls:   make(map[string]*fslockCall),
	}, nil
}

// Do executes and returns the results of the given function using filesystem locks.
// Only one execution will occur for a given key at a time, with other callers
// waiting and sharing the result.
func (g *GofslockGroup) Do(key string, fn func() (interface{}, error)) (v interface{}, err error, shared bool) {
	// Hash the key to create a safe filename
	hash := sha256.Sum256([]byte(key))
	lockFileName := hex.EncodeToString(hash[:]) + ".lock"
	lockPath := filepath.Join(g.lockDir, lockFileName)

	// Check if there's already a call in progress
	g.mu.Lock()
	if call, exists := g.calls[key]; exists {
		g.mu.Unlock()
		// Wait for the existing call to complete
		<-call.done
		return call.result, call.err, true
	}

	// Create a new call
	call := &fslockCall{
		done: make(chan struct{}),
	}
	g.calls[key] = call
	g.mu.Unlock()

	// Acquire filesystem lock
	lock := fslock.New(lockPath)
	if err := lock.Lock(); err != nil {
		g.mu.Lock()
		delete(g.calls, key)
		g.mu.Unlock()
		close(call.done)
		return nil, fmt.Errorf("failed to acquire lock: %w", err), false
	}

	// Execute the function
	call.result, call.err = fn()

	// Release the lock
	lock.Unlock()

	// Clean up
	g.mu.Lock()
	delete(g.calls, key)
	g.mu.Unlock()

	// Notify waiters
	close(call.done)

	return call.result, call.err, false
}
