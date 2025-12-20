package dedupe

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
}

// NewFlockGroup creates a new FlockGroup.
// lockDir is the directory where lock files will be created.
// If lockDir is empty, it defaults to os.TempDir()/gobuildcache-dedupe-locks.
func NewFlockGroup(lockDir string) (*FSLockGroup, error) {
	if lockDir == "" {
		lockDir = filepath.Join(os.TempDir(), "gobuildcache-dedupe-locks")
	}

	// Ensure lock directory exists
	if err := os.MkdirAll(lockDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create lock directory: %w", err)
	}

	return &FSLockGroup{
		lockDir: lockDir,
	}, nil
}

// Do executes and returns the results of the given function using filesystem locks.
// Only one execution will occur for a given key at a time across all processes.
// The shared return value is always false since this implementation provides mutual
// exclusion rather than result sharing.
func (g *FSLockGroup) Do(key string, fn func() (interface{}, error)) (v interface{}, err error, shared bool) {
	// Hash the key to create a safe filename
	hash := sha256.Sum256([]byte(key))
	lockFileName := hex.EncodeToString(hash[:]) + ".lock"
	lockPath := filepath.Join(g.lockDir, lockFileName)

	// Acquire filesystem lock (blocks until lock is available)
	fileLock := flock.New(lockPath)
	ctx, cc := context.WithTimeout(context.Background(), time.Second)
	defer cc()
	acquired, err := fileLock.TryLockContext(ctx, 10*time.Millisecond)
	if err != nil {
		return nil, fmt.Errorf("failed to acquire lock: %w", err), false
	}
	if !acquired {
		return nil, fmt.Errorf("failed to acquire lock: timeout"), false
	}
	defer fileLock.Unlock()

	// Execute the function
	v, err = fn()
	return v, err, false
}
