package main

import (
	"encoding/hex"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// LocalCache manages the local disk cache where Go build tools access cached files.
// It handles writing, reading, and metadata management for cached entries.
type LocalCache struct {
	cacheDir string
	logger   *slog.Logger
}

// localCacheMetadata holds metadata for a cached entry.
type localCacheMetadata struct {
	OutputID []byte
	Size     int64
	PutTime  time.Time
}

// NewLocalCache creates a new local cache instance.
// cacheDir is the directory where cached files will be stored.
func NewLocalCache(cacheDir string, logger *slog.Logger) (*LocalCache, error) {
	// Ensure cache directory exists
	if err := os.MkdirAll(cacheDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create cache directory: %w", err)
	}

	return &LocalCache{
		cacheDir: cacheDir,
		logger:   logger,
	}, nil
}

// actionIDToPath converts an actionID to a local cache file path.
func (lc *LocalCache) actionIDToPath(actionID []byte) string {
	hexID := hex.EncodeToString(actionID)
	return filepath.Join(lc.cacheDir, hexID)
}

// metadataPath returns the path to the metadata file for an actionID.
func (lc *LocalCache) metadataPath(actionID []byte) string {
	return lc.actionIDToPath(actionID) + ".meta"
}

// writeMetadata writes metadata for a cache entry.
func (lc *LocalCache) writeMetadata(actionID []byte, meta localCacheMetadata) error {
	metaPath := lc.metadataPath(actionID)

	// Format: outputID:hex\nsize:num\ntime:unix\n
	content := fmt.Sprintf("outputID:%s\nsize:%d\ntime:%d\n",
		hex.EncodeToString(meta.OutputID),
		meta.Size,
		meta.PutTime.Unix())

	// Write to temp file first for atomic operation
	tmpPath := metaPath + ".tmp"
	if err := os.WriteFile(tmpPath, []byte(content), 0644); err != nil {
		return fmt.Errorf("failed to write temp metadata: %w", err)
	}

	// Atomically rename
	if err := os.Rename(tmpPath, metaPath); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("failed to rename metadata: %w", err)
	}

	return nil
}

// readMetadata reads metadata for a cache entry.
// Returns an error if metadata doesn't exist or is corrupted.
func (lc *LocalCache) readMetadata(actionID []byte) (*localCacheMetadata, error) {
	metaPath := lc.metadataPath(actionID)

	data, err := os.ReadFile(metaPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("metadata file not found")
		}
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
func (lc *LocalCache) Write(actionID []byte, body io.Reader) (string, error) {
	diskPath := lc.actionIDToPath(actionID)

	// Create a temporary file in the same directory for atomic write
	tmpFile, err := os.CreateTemp(lc.cacheDir, ".tmp-*")
	if err != nil {
		return "", fmt.Errorf("failed to create temp file: %w", err)
	}
	tmpPath := tmpFile.Name()
	defer os.Remove(tmpPath) // Clean up if something goes wrong

	// Copy data to temp file
	_, err = io.Copy(tmpFile, body)
	closeErr := tmpFile.Close()
	if err != nil {
		return "", fmt.Errorf("failed to write to temp file: %w", err)
	}
	if closeErr != nil {
		return "", fmt.Errorf("failed to close temp file: %w", closeErr)
	}

	// Atomically rename temp file to final destination
	if err := os.Rename(tmpPath, diskPath); err != nil {
		return "", fmt.Errorf("failed to rename cache file: %w", err)
	}

	absPath, err := filepath.Abs(diskPath)
	if err != nil {
		return diskPath, nil // fallback to relative path
	}

	return absPath, nil
}

// WriteWithMetadata writes data and metadata to the local cache.
// Returns the absolute path to the cached file.
func (lc *LocalCache) WriteWithMetadata(actionID []byte, body io.Reader, meta localCacheMetadata) (string, error) {
	// Write data
	diskPath, err := lc.Write(actionID, body)
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
// Returns nil if not found, and logs a warning if metadata is missing/corrupted.
func (lc *LocalCache) Check(actionID []byte) *localCacheMetadata {
	diskPath := lc.actionIDToPath(actionID)
	if _, err := os.Stat(diskPath); err != nil {
		// File doesn't exist in cache
		return nil
	}

	// Read metadata
	meta, err := lc.readMetadata(actionID)
	if err != nil {
		// File exists but metadata is missing or corrupted
		lc.logger.Warn("local cache file exists but metadata is missing/corrupted",
			"actionID", hex.EncodeToString(actionID),
			"error", err)
		return nil
	}

	return meta
}

// GetPath returns the absolute path for an actionID in the local cache.
// Does not check if the file actually exists.
func (lc *LocalCache) GetPath(actionID []byte) string {
	diskPath := lc.actionIDToPath(actionID)
	absPath, err := filepath.Abs(diskPath)
	if err != nil {
		return diskPath
	}
	return absPath
}
