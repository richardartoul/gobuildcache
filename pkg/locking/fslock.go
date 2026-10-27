package locking

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/gofrs/flock"
)

// FSLockGroup is a Group implementation that uses filesystem locks for mutual exclusion.
// It uses the gofrs/flock library to ensure only one execution happens for a given key
// at a time across all goroutines and processes. Unlike SingleflightGroup, this does not
// share results within the same process - each caller will execute the function once they
// acquire the lock.
type FSLockGroup struct {
	lockDir string
	timeout time.Duration
}

// DefaultLockTimeout is how long DoWithLock waits for a lock when no timeout is given.
const DefaultLockTimeout = 5 * time.Second

// NewFlockGroup creates a new FlockGroup.
// lockDir is the directory where lock files will be created.
// If lockDir is empty, it defaults to os.TempDir()/gobuildcache-locks.
// timeout is how long DoWithLock waits for a lock; if it is not positive,
// DefaultLockTimeout is used.
func NewFlockGroup(lockDir string, timeout time.Duration) (*FSLockGroup, error) {
	if lockDir == "" {
		lockDir = filepath.Join(os.TempDir(), "gobuildcache-locks")
	}
	if timeout <= 0 {
		timeout = DefaultLockTimeout
	}

	// Ensure lock directory exists
	if err := os.MkdirAll(lockDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create lock directory: %w", err)
	}

	return &FSLockGroup{
		lockDir: lockDir,
		timeout: timeout,
	}, nil
}

// Do executes and returns the results of the given function using filesystem locks.
// Only one execution will occur for a given key at a time across all processes.
// The shared return value is always false since this implementation provides mutual
// exclusion rather than result sharing.
//
// If the lock can't be acquired within the group's timeout, or at all, fn is not
// run and the returned error wraps ErrLockNotAcquired.
func (g *FSLockGroup) DoWithLock(key string, fn func() (interface{}, error)) (v interface{}, err error) {
	// Hash the key to create a safe filename
	hash := sha256.Sum256([]byte(key))
	lockFileName := hex.EncodeToString(hash[:]) + ".lock"
	lockPath := filepath.Join(g.lockDir, lockFileName)

	// Acquire filesystem lock (blocks until lock is available)
	fileLock := flock.New(lockPath)
	ctx, cc := context.WithTimeout(context.Background(), g.timeout)
	defer cc()
	acquired, err := fileLock.TryLockContext(ctx, 10*time.Millisecond)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrLockNotAcquired, err)
	}
	if !acquired {
		return nil, fmt.Errorf("%w: timeout", ErrLockNotAcquired)
	}
	defer fileLock.Unlock()

	return fn()
}
