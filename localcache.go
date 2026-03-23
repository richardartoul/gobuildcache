package main

import (
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// localCache manages the local disk cache where Go build tools access cached files.
// It handles writing, reading, and metadata management for cached entries.
type localCache struct {
	cacheDir string // Absolute path to cache directory
	logger   *slog.Logger
}

// localCacheMetadata holds metadata for a cached entry.
type localCacheMetadata struct {
	OutputID []byte
	Size     int64
	PutTime  time.Time
}

// newLocalCache creates a new local cache instance.
// cacheDir is the directory where cached files will be stored.
func newLocalCache(cacheDir string, logger *slog.Logger) (*localCache, error) {
	// Ensure cache directory exists
	if err := os.MkdirAll(cacheDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create cache directory: %w", err)
	}

	// Convert to absolute path once at initialization
	// This avoids repeated filepath.Abs() calls later
	absCacheDir, err := filepath.Abs(cacheDir)
	if err != nil {
		return nil, fmt.Errorf("failed to get absolute path: %w", err)
	}

	// Precreate all 256 subdirectories (00-ff) in parallel to avoid syscalls during writes.
	var (
		wg      sync.WaitGroup
		errChan = make(chan error, 256)
	)
	for i := range 256 {
		wg.Add(1)
		go func() {
			defer wg.Done()

			var (
				subdir     = fmt.Sprintf("%02x", i)
				subdirPath = filepath.Join(absCacheDir, subdir)
			)
			if err := os.MkdirAll(subdirPath, 0755); err != nil {
				errChan <- fmt.Errorf("failed to create subdirectory %s: %w", subdir, err)
			}
		}()
	}

	wg.Wait()
	close(errChan)

	// Check if any errors occurred.
	if err := <-errChan; err != nil {
		return nil, err
	}

	return &localCache{
		cacheDir: absCacheDir,
		logger:   logger,
	}, nil
}

// writeMetadata writes metadata for a cache entry.
func (lc *localCache) writeMetadata(actionID []byte, meta localCacheMetadata) error {
	metaPath := lc.metadataPath(actionID)

	// Format: outputID:hex\nsize:num\ntime:unix\n
	content := fmt.Sprintf("outputID:%s\nsize:%d\ntime:%d\n",
		hex.EncodeToString(meta.OutputID),
		meta.Size,
		meta.PutTime.Unix())

	// Write to temp file first for atomic operation.
	tmpPath := metaPath + ".tmp"
	if err := os.WriteFile(tmpPath, []byte(content), 0644); err != nil {
		return fmt.Errorf("failed to write temp metadata: %w", err)
	}

	// Then atomically rename. This prevents any partial metadata files
	// from ever existing, although it increases the number of syscalls
	// we need to perform.
	if err := os.Rename(tmpPath, metaPath); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("failed to rename metadata: %w", err)
	}

	return nil
}

// readMetadata reads metadata for a cache entry.
// Returns an error if metadata doesn't exist or is corrupted.
func (lc *localCache) readMetadata(actionID []byte) (*localCacheMetadata, error) {
	metaPath := lc.metadataPath(actionID)

	data, err := os.ReadFile(metaPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read metadata: %w", err)
	}

	var outputIDHex string
	var size int64
	var putTimeUnix int64

	// Parse each line
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "outputID:") {
			fmt.Sscanf(line, "outputID:%s", &outputIDHex)
		} else if strings.HasPrefix(line, "size:") {
			fmt.Sscanf(line, "size:%d", &size)
		} else if strings.HasPrefix(line, "time:") {
			fmt.Sscanf(line, "time:%d", &putTimeUnix)
		}
	}

	if outputIDHex == "" {
		return nil, fmt.Errorf("metadata missing outputID field")
	}

	outputID, err := hex.DecodeString(outputIDHex)
	if err != nil {
		return nil, fmt.Errorf("failed to decode outputID: %w", err)
	}

	return &localCacheMetadata{
		OutputID: outputID,
		Size:     size,
		PutTime:  time.Unix(putTimeUnix, 0),
	}, nil
}

// Write atomically writes data from a reader to the local cache.
// Returns the absolute path to the cached file.
func (lc *localCache) write(actionID []byte, body io.Reader) (string, error) {
	diskPath := lc.actionIDToPath(actionID)

	// Write to temp file first for atomic operation.
	tmpPath := diskPath + ".tmp"
	tmpFile, err := os.Create(tmpPath)
	if err != nil {
		return "", fmt.Errorf("failed to create temp file: %w", err)
	}
	defer os.Remove(tmpPath) // Clean up if something goes wrong

	// Copy data to temp file.
	_, err = io.Copy(tmpFile, body)
	closeErr := tmpFile.Close()
	if err != nil {
		return "", fmt.Errorf("failed to write to temp file: %w", err)
	}
	if closeErr != nil {
		return "", fmt.Errorf("failed to close temp file: %w", closeErr)
	}

	// Then atomically rename the temp file to the final destination.
	// This prevents any partial cache files from ever existing, although
	// it increases the number of syscalls we need to perform.
	//
	// NOTE: I'm not sure this is necessary. Even if we write a partial data
	// file, if we don't write the metadata file, then the data file will
	// never be observed. Similarly, there's no worry of data races because
	// localcache.go is running with mutual exclusion over a given action ID
	// as implemented in server.go. That said, I'm leaving this in for now
	// because I think it's safer and probably doesn't hurt performance much.
	if err := os.Rename(tmpPath, diskPath); err != nil {
		return "", fmt.Errorf("failed to rename cache file: %w", err)
	}

	// diskPath is already absolute (cacheDir is absolute)
	return diskPath, nil
}

// WriteWithMetadata writes data and metadata to the local cache.
// Returns the absolute path to the cached file.
func (lc *localCache) writeWithMetadata(actionID []byte, body io.Reader, meta localCacheMetadata) (string, error) {
	// Write data
	diskPath, err := lc.write(actionID, body)
	if err != nil {
		return "", err
	}

	// Write metadata
	if err := lc.writeMetadata(actionID, meta); err != nil {
		lc.logger.Warn("failed to write local cache metadata",
			"actionID", hex.EncodeToString(actionID),
			"error", err)
		// Continue - data is cached, just missing metadata
	}

	return diskPath, nil
}

// Check checks if a file exists in the local cache and returns its metadata.
// Returns nil if not found. Corrupted entries are cleaned up and treated
// as cache misses. This can happen when stale entries from a previous
// gobuildcache version or Go's native cache persist between builds.
func (lc *localCache) check(actionID []byte) *localCacheMetadata {
	// Try to read metadata directly (avoids extra Stat syscall)
	// If the data file doesn't exist, the metadata file likely won't either
	meta, err := lc.readMetadata(actionID)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			// Neither data nor metadata exists - this is a cache miss
			return nil
		}

		// Metadata exists but is corrupted (e.g., missing outputID field).
		// Log at debug level to avoid spam from stale entries that may
		// persist between builds, while still providing observability
		// when debugging.
		lc.logger.Debug("evicting corrupted local cache entry",
			"actionID", hex.EncodeToString(actionID),
			"error", err)
		lc.evict(actionID)
		return nil
	}

	return meta
}

// evict removes both the data file and metadata file for a cache entry.
func (lc *localCache) evict(actionID []byte) {
	diskPath := lc.actionIDToPath(actionID)
	metaPath := lc.metadataPath(actionID)
	if err := os.Remove(diskPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		lc.logger.Warn("failed to evict cached data file",
			"actionID", hex.EncodeToString(actionID),
			"path", diskPath,
			"error", err)
	}
	if err := os.Remove(metaPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		lc.logger.Warn("failed to evict cached metadata file",
			"actionID", hex.EncodeToString(actionID),
			"path", metaPath,
			"error", err)
	}
}

// actionIDToPath converts an actionID to a local cache file path.
// Files are organized into 256 subdirectories (00-ff) based on the first byte
// of the action ID, similar to Go's build cache structure.
func (lc *localCache) actionIDToPath(actionID []byte) string {
	hexActionID := hex.EncodeToString(actionID)
	// Use first two hex characters (first byte) of action ID as subdirectory name
	subdir := hexActionID[:2]
	hexID := fileFormatVersion + hexActionID
	return filepath.Join(lc.cacheDir, subdir, hexID)
}

// metadataPath returns the path to the metadata file for an actionID.
func (lc *localCache) metadataPath(actionID []byte) string {
	return lc.actionIDToPath(actionID) + ".meta"
}

// GetPath returns the absolute path for an actionID in the local cache.
// Does not check if the file actually exists.
func (lc *localCache) getPath(actionID []byte) string {
	// Since cacheDir is already absolute, the path is already absolute
	return lc.actionIDToPath(actionID)
}
