package backends

import (
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Disk implements Backend using the local file system.
type Disk struct {
	baseDir string
}

// NewDisk creates a new disk-based cache backend.
// baseDir is the directory where cache files will be stored.
func NewDisk(baseDir string) (*Disk, error) {
	if err := os.MkdirAll(baseDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create cache directory: %w", err)
	}

	return &Disk{
		baseDir: baseDir,
	}, nil
}

// Put stores an object in the cache.
func (d *Disk) Put(actionID, outputID []byte, body io.Reader, bodySize int64) (string, error) {
	diskPath := d.actionIDToPath(actionID)
	metaPath := d.metadataPath(actionID)

	// Create the cache file
	file, err := os.Create(diskPath)
	if err != nil {
		return "", fmt.Errorf("failed to create cache file: %w", err)
	}
	defer file.Close()

	// Write the body to the file (skip if bodySize is 0)
	var written int64
	if bodySize > 0 && body != nil {
		written, err = io.Copy(file, body)
		if err != nil {
			os.Remove(diskPath)
			return "", fmt.Errorf("failed to write cache file: %w", err)
		}

		if written != bodySize {
			os.Remove(diskPath)
			return "", fmt.Errorf("size mismatch: expected %d, wrote %d", bodySize, written)
		}
	}

	// Write metadata file
	now := time.Now()
	meta := fmt.Sprintf("outputID:%s\nsize:%d\ntime:%d\n",
		hex.EncodeToString(outputID), bodySize, now.Unix())
	if err := os.WriteFile(metaPath, []byte(meta), 0644); err != nil {
		os.Remove(diskPath)
		return "", fmt.Errorf("failed to write metadata: %w", err)
	}

	absPath, err := filepath.Abs(diskPath)
	if err != nil {
		return diskPath, nil // fallback to relative path
	}

	return absPath, nil
}

// Get retrieves an object from the cache.
func (d *Disk) Get(actionID []byte) ([]byte, string, int64, *time.Time, bool, error) {
	diskPath := d.actionIDToPath(actionID)
	metaPath := d.metadataPath(actionID)

	// Check if file exists
	if _, err := os.Stat(diskPath); os.IsNotExist(err) {
		return nil, "", 0, nil, true, nil
	}

	// Read metadata
	metaData, err := os.ReadFile(metaPath)
	if err != nil {
		return nil, "", 0, nil, true, nil
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
		return nil, "", 0, nil, true, nil
	}

	putTime := time.Unix(putTimeUnix, 0)

	absPath, err := filepath.Abs(diskPath)
	if err != nil {
		absPath = diskPath
	}

	return outputID, absPath, size, &putTime, false, nil
}

// Close performs cleanup operations.
func (d *Disk) Close() error {
	// No cleanup needed for disk backend
	return nil
}

// Clear removes all entries from the cache.
func (d *Disk) Clear() error {
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

	return nil
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
