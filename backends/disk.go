package backends

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Disk implements Backend using the local file system.
// It uses per-key locking to allow concurrent operations on different keys
// while ensuring safety for operations on the same key.
type Disk struct {
	baseDir string

	// Per-key locks for fine-grained concurrency control
	locksMu sync.RWMutex
	locks   map[string]*sync.Mutex
}

// NewDisk creates a new disk-based cache backend.
// baseDir is the directory where cache files will be stored.
func NewDisk(baseDir string) (*Disk, error) {
	if err := os.MkdirAll(baseDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create cache directory: %w", err)
	}

	return &Disk{
		baseDir: baseDir,
		locks:   make(map[string]*sync.Mutex),
	}, nil
}

// getLock gets or creates a lock for a specific actionID.
// This allows concurrent operations on different keys while serializing
// operations on the same key.
func (d *Disk) getLock(actionID []byte) *sync.Mutex {
	key := hex.EncodeToString(actionID)

	d.locksMu.RLock()
	lock, exists := d.locks[key]
	d.locksMu.RUnlock()

	if exists {
		return lock
	}

	// Lock doesn't exist, create it
	d.locksMu.Lock()
	defer d.locksMu.Unlock()

	// Double-check after acquiring write lock
	lock, exists = d.locks[key]
	if exists {
		return lock
	}

	lock = &sync.Mutex{}
	d.locks[key] = lock
	return lock
}

// Put stores an object in the backend storage atomically.
// Uses write-to-temp-then-rename to ensure Get operations never see partial writes.
func (d *Disk) Put(actionID, outputID []byte, body io.Reader, bodySize int64) error {
	// Acquire per-key lock to prevent concurrent writes to the same actionID
	lock := d.getLock(actionID)
	lock.Lock()
	defer lock.Unlock()

	diskPath := d.actionIDToPath(actionID)
	metaPath := d.metadataPath(actionID)

	// Create temporary files for atomic write
	tmpFile, err := d.createTempFile()
	if err != nil {
		return fmt.Errorf("failed to create temp file: %w", err)
	}
	tmpFilePath := tmpFile.Name()
	defer os.Remove(tmpFilePath) // Clean up temp file if something goes wrong

	tmpMetaPath := tmpFilePath + ".meta"
	defer os.Remove(tmpMetaPath) // Clean up temp meta file if something goes wrong

	// Write the body to the temp file (skip if bodySize is 0)
	var written int64
	if bodySize > 0 && body != nil {
		written, err = io.Copy(tmpFile, body)
		if err != nil {
			tmpFile.Close()
			return fmt.Errorf("failed to write cache file: %w", err)
		}

		if written != bodySize {
			tmpFile.Close()
			return fmt.Errorf("size mismatch: expected %d, wrote %d", bodySize, written)
		}
	}

	// Close the temp file before renaming
	if err := tmpFile.Close(); err != nil {
		return fmt.Errorf("failed to close temp file: %w", err)
	}

	// Write metadata to temp file
	now := time.Now()
	meta := fmt.Sprintf("outputID:%s\nsize:%d\ntime:%d\n",
		hex.EncodeToString(outputID), bodySize, now.Unix())
	if err := os.WriteFile(tmpMetaPath, []byte(meta), 0644); err != nil {
		return fmt.Errorf("failed to write metadata: %w", err)
	}

	// Atomically rename temp files to final destination
	// This ensures Get operations never see partial writes
	if err := os.Rename(tmpMetaPath, metaPath); err != nil {
		return fmt.Errorf("failed to rename metadata file: %w", err)
	}

	if err := os.Rename(tmpFilePath, diskPath); err != nil {
		// Try to clean up the metadata file if data file rename fails
		os.Remove(metaPath)
		return fmt.Errorf("failed to rename cache file: %w", err)
	}

	return nil
}

// Get retrieves an object from the backend storage.
// Returns the object data as an io.ReadCloser that must be closed by the caller.
// Uses per-key locking to ensure it never sees partial Put operations.
func (d *Disk) Get(actionID []byte) ([]byte, io.ReadCloser, int64, *time.Time, bool, error) {
	// Acquire per-key lock to ensure we don't read during a write
	lock := d.getLock(actionID)
	lock.Lock()
	defer lock.Unlock()

	diskPath := d.actionIDToPath(actionID)
	metaPath := d.metadataPath(actionID)

	// Check if file exists
	if _, err := os.Stat(diskPath); os.IsNotExist(err) {
		return nil, nil, 0, nil, true, nil
	}

	// Read metadata
	metaData, err := os.ReadFile(metaPath)
	if err != nil {
		// If metadata is missing but data file exists, treat as miss
		// This can happen if a Put operation was interrupted
		return nil, nil, 0, nil, true, nil
	}

	// Parse metadata (simple format: outputID:hex\nsize:num\ntime:unix\n)
	var outputIDHex string
	var size int64
	var putTimeUnix int64

	lines := string(metaData)
	// Parse each line
	for _, line := range strings.Split(lines, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "outputID:") {
			fmt.Sscanf(line, "outputID:%s", &outputIDHex)
		} else if strings.HasPrefix(line, "size:") {
			fmt.Sscanf(line, "size:%d", &size)
		} else if strings.HasPrefix(line, "time:") {
			fmt.Sscanf(line, "time:%d", &putTimeUnix)
		}
	}

	outputID, err := hex.DecodeString(outputIDHex)
	if err != nil {
		// Corrupted metadata, treat as miss
		return nil, nil, 0, nil, true, nil
	}

	putTime := time.Unix(putTimeUnix, 0)

	// Open the file for reading
	// Note: We can safely unlock after opening because the file is immutable
	// (atomic write-to-temp-then-rename ensures files are never modified after creation)
	file, err := os.Open(diskPath)
	if err != nil {
		// File might have been deleted between stat and open
		return nil, nil, 0, nil, true, nil
	}

	return outputID, file, size, &putTime, false, nil
}

// Close performs cleanup operations.
func (d *Disk) Close() error {
	// Clean up the lock map to free memory
	d.locksMu.Lock()
	d.locks = make(map[string]*sync.Mutex)
	d.locksMu.Unlock()
	return nil
}

// Clear removes all entries from the cache.
func (d *Disk) Clear() error {
	// No need for per-key locks since we're clearing everything
	// But we should ensure no operations are in progress
	d.locksMu.Lock()
	defer d.locksMu.Unlock()

	// Read all files in the cache directory
	entries, err := os.ReadDir(d.baseDir)
	if err != nil {
		if os.IsNotExist(err) {
			// Directory doesn't exist, nothing to clear
			return nil
		}
		return fmt.Errorf("failed to read cache directory: %w", err)
	}

	// Remove all files
	for _, entry := range entries {
		path := filepath.Join(d.baseDir, entry.Name())
		if err := os.Remove(path); err != nil {
			return fmt.Errorf("failed to remove %s: %w", path, err)
		}
	}

	// Clear the lock map since all keys are now gone
	d.locks = make(map[string]*sync.Mutex)

	return nil
}

// createTempFile creates a temporary file in the cache directory.
func (d *Disk) createTempFile() (*os.File, error) {
	// Generate a random temp file name
	var randBytes [16]byte
	if _, err := rand.Read(randBytes[:]); err != nil {
		return nil, fmt.Errorf("failed to generate random bytes: %w", err)
	}
	tmpName := filepath.Join(d.baseDir, ".tmp."+hex.EncodeToString(randBytes[:]))

	file, err := os.Create(tmpName)
	if err != nil {
		return nil, fmt.Errorf("failed to create temp file: %w", err)
	}

	return file, nil
}

// actionIDToPath converts an actionID to a file path.
func (d *Disk) actionIDToPath(actionID []byte) string {
	hexID := hex.EncodeToString(actionID)
	return filepath.Join(d.baseDir, hexID)
}

// metadataPath returns the path to the metadata file for an actionID.
func (d *Disk) metadataPath(actionID []byte) string {
	hexID := hex.EncodeToString(actionID)
	return filepath.Join(d.baseDir, hexID+".meta")
}
