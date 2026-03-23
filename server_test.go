package main

import (
	"encoding/hex"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/richardartoul/gobuildcache/pkg/backends"
	"github.com/richardartoul/gobuildcache/pkg/locking"
)

// emptyOutputIDBackend is a test backend that returns hits with empty outputID,
// simulating S3 objects with missing outputID metadata.
type emptyOutputIDBackend struct {
	backends.Noop
}

func (b *emptyOutputIDBackend) Get(actionID []byte) ([]byte, io.ReadCloser, int64, *time.Time, bool, error) {
	now := time.Now()
	body := io.NopCloser(strings.NewReader("test data"))
	// Return a hit with empty outputID -- simulates corrupted S3 metadata
	return []byte{}, body, 9, &now, false, nil
}

func TestFormatBytes(t *testing.T) {
	tests := []struct {
		bytes    int64
		expected string
	}{
		{0, "0 B"},
		{1, "1 B"},
		{1023, "1023 B"},
		{1024, "1.00 KB"},
		{1536, "1.50 KB"},
		{1024 * 1024, "1.00 MB"},
		{1536 * 1024, "1.50 MB"},
		{1024 * 1024 * 1024, "1.00 GB"},
		{1024*1024*1024*2 + 1024*1024*512, "2.50 GB"},
		{1024 * 1024 * 1024 * 1024, "1.00 TB"},
		{1024*1024*1024*1024*3 + 1024*1024*1024*716, "3.70 TB"},
	}

	for _, tt := range tests {
		result := formatBytes(tt.bytes)
		if result != tt.expected {
			t.Errorf("formatBytes(%d) = %s, expected %s", tt.bytes, result, tt.expected)
		}
	}
}

// createTestCacheProg creates a CacheProg for testing with the specified readOnly setting.
func createTestCacheProg(t *testing.T, readOnly bool) (*CacheProg, string) {
	t.Helper()

	// Create a temporary directory for the cache
	cacheDir, err := os.MkdirTemp("", "gobuildcache-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp directory: %v", err)
	}

	backend := backends.NewNoop()
	locker := locking.NewNoOpGroup()

	cp, err := NewCacheProg(backend, locker, cacheDir, false, false, false, readOnly)
	if err != nil {
		os.RemoveAll(cacheDir)
		t.Fatalf("Failed to create CacheProg: %v", err)
	}

	return cp, cacheDir
}

func TestReadOnlyMode_SkipsPut(t *testing.T) {
	cp, cacheDir := createTestCacheProg(t, true) // readOnly = true
	defer os.RemoveAll(cacheDir)

	// Create a PUT request
	req := &Request{
		ID:       1,
		Command:  CmdPut,
		ActionID: []byte("test-action-id-12345678"),
		OutputID: []byte("test-output-id-12345678"),
		Body:     strings.NewReader("test body content"),
		BodySize: 17,
	}

	// Execute the PUT
	resp, err := cp.handlePut(req)
	if err != nil {
		t.Fatalf("handlePut returned error: %v", err)
	}

	// Verify response is successful (no error)
	if resp.Err != "" {
		t.Errorf("Expected no error in response, got: %s", resp.Err)
	}

	// Verify skippedPuts counter was incremented (backend write skipped)
	if cp.skippedPuts.Load() != 1 {
		t.Errorf("Expected skippedPuts to be 1, got: %d", cp.skippedPuts.Load())
	}

	// Verify putCount WAS incremented (local cache write still happens)
	if cp.putCount.Load() != 1 {
		t.Errorf("Expected putCount to be 1, got: %d", cp.putCount.Load())
	}

	// Verify local cache was written to (DiskPath should be set)
	if resp.DiskPath == "" {
		t.Error("Expected DiskPath to be set (local cache should be written)")
	}

	// Verify backendBytesWritten is 0 (no backend write)
	if cp.backendBytesWritten.Load() != 0 {
		t.Errorf("Expected backendBytesWritten to be 0, got: %d", cp.backendBytesWritten.Load())
	}
}

func TestReadOnlyMode_Disabled_AllowsPut(t *testing.T) {
	cp, cacheDir := createTestCacheProg(t, false) // readOnly = false
	defer os.RemoveAll(cacheDir)

	// Create a PUT request
	req := &Request{
		ID:       1,
		Command:  CmdPut,
		ActionID: []byte("test-action-id-12345678"),
		OutputID: []byte("test-output-id-12345678"),
		Body:     strings.NewReader("test body content"),
		BodySize: 17,
	}

	// Execute the PUT
	resp, err := cp.handlePut(req)
	if err != nil {
		t.Fatalf("handlePut returned error: %v", err)
	}

	// Verify response is successful (no error)
	if resp.Err != "" {
		t.Errorf("Expected no error in response, got: %s", resp.Err)
	}

	// Verify skippedPuts was NOT incremented
	if cp.skippedPuts.Load() != 0 {
		t.Errorf("Expected skippedPuts to be 0, got: %d", cp.skippedPuts.Load())
	}

	// Verify putCount WAS incremented
	if cp.putCount.Load() != 1 {
		t.Errorf("Expected putCount to be 1, got: %d", cp.putCount.Load())
	}
}

func TestReadOnlyMode_MultipleSkippedPuts(t *testing.T) {
	cp, cacheDir := createTestCacheProg(t, true) // readOnly = true
	defer os.RemoveAll(cacheDir)

	// Execute multiple PUTs with unique action IDs
	for i := 0; i < 5; i++ {
		actionID := make([]byte, 24)
		copy(actionID, []byte("test-action-id-1234567"))
		actionID[23] = byte('0' + i)

		req := &Request{
			ID:       int64(i),
			Command:  CmdPut,
			ActionID: actionID,
			OutputID: []byte("test-output-id-12345678"),
			Body:     strings.NewReader("test body"),
			BodySize: 9,
		}

		_, err := cp.handlePut(req)
		if err != nil {
			t.Fatalf("handlePut %d returned error: %v", i, err)
		}
	}

	// Verify all 5 backend writes were skipped
	if cp.skippedPuts.Load() != 5 {
		t.Errorf("Expected skippedPuts to be 5, got: %d", cp.skippedPuts.Load())
	}

	// Verify putCount is 5 (local cache writes still happen)
	if cp.putCount.Load() != 5 {
		t.Errorf("Expected putCount to be 5, got: %d", cp.putCount.Load())
	}
}

func TestReadOnlyMode_GetAfterPut(t *testing.T) {
	cp, cacheDir := createTestCacheProg(t, true) // readOnly = true
	defer os.RemoveAll(cacheDir)

	actionID := []byte("test-action-id-12345678")
	outputID := []byte("test-output-id-12345678")

	// PUT first (writes to local cache, skips backend)
	putReq := &Request{
		ID:       1,
		Command:  CmdPut,
		ActionID: actionID,
		OutputID: outputID,
		Body:     strings.NewReader("test body content"),
		BodySize: 17,
	}

	_, err := cp.handlePut(putReq)
	if err != nil {
		t.Fatalf("handlePut returned error: %v", err)
	}

	// GET should hit the local cache
	getReq := &Request{
		ID:       2,
		Command:  CmdGet,
		ActionID: actionID,
	}

	resp, err := cp.handleGet(getReq)
	if err != nil {
		t.Fatalf("handleGet returned error: %v", err)
	}

	if resp.Miss {
		t.Error("Expected cache hit, got miss")
	}

	if resp.DiskPath == "" {
		t.Error("Expected DiskPath to be set on cache hit")
	}

	if cp.localCacheHits.Load() != 1 {
		t.Errorf("Expected localCacheHits to be 1, got: %d", cp.localCacheHits.Load())
	}
}

func TestCorruptedMetadata_EvictsEntry(t *testing.T) {
	cp, cacheDir := createTestCacheProg(t, false)
	defer os.RemoveAll(cacheDir)

	actionID := []byte("test-action-id-12345678")

	// Write a data file and a corrupted metadata file directly to disk
	hexActionID := hex.EncodeToString(actionID)
	subdir := hexActionID[:2]
	hexID := fileFormatVersion + hexActionID
	dataPath := filepath.Join(cacheDir, subdir, hexID)
	metaPath := dataPath + ".meta"

	// Write data file
	if err := os.WriteFile(dataPath, []byte("cached data"), 0644); err != nil {
		t.Fatalf("Failed to write data file: %v", err)
	}

	// Write corrupted metadata (missing outputID field)
	if err := os.WriteFile(metaPath, []byte("size:100\ntime:1234567890\n"), 0644); err != nil {
		t.Fatalf("Failed to write metadata file: %v", err)
	}

	// Verify files exist before check
	if _, err := os.Stat(dataPath); err != nil {
		t.Fatalf("Data file should exist before check: %v", err)
	}
	if _, err := os.Stat(metaPath); err != nil {
		t.Fatalf("Meta file should exist before check: %v", err)
	}

	// check() should return nil (cache miss) and evict both files
	meta := cp.localCache.check(actionID)
	if meta != nil {
		t.Error("Expected nil (cache miss) for corrupted metadata, got non-nil")
	}

	// Verify both files were evicted
	if _, err := os.Stat(dataPath); !os.IsNotExist(err) {
		t.Error("Expected data file to be evicted (removed)")
	}
	if _, err := os.Stat(metaPath); !os.IsNotExist(err) {
		t.Error("Expected metadata file to be evicted (removed)")
	}
}

func TestCorruptedMetadata_ValidEntryNotEvicted(t *testing.T) {
	cp, cacheDir := createTestCacheProg(t, false)
	defer os.RemoveAll(cacheDir)

	actionID := []byte("test-action-id-12345678")
	outputID := []byte("test-output-id-12345678")

	// Write a valid entry via the normal path
	putReq := &Request{
		ID:       1,
		Command:  CmdPut,
		ActionID: actionID,
		OutputID: outputID,
		Body:     strings.NewReader("test body content"),
		BodySize: 17,
	}

	_, err := cp.handlePut(putReq)
	if err != nil {
		t.Fatalf("handlePut returned error: %v", err)
	}

	// check() should return valid metadata, not evict
	meta := cp.localCache.check(actionID)
	if meta == nil {
		t.Fatal("Expected valid metadata, got nil")
	}

	if hex.EncodeToString(meta.OutputID) != hex.EncodeToString(outputID) {
		t.Errorf("Expected outputID %s, got %s",
			hex.EncodeToString(outputID), hex.EncodeToString(meta.OutputID))
	}
}

func TestEmptyOutputID_BackendHit_LocalCacheSelfHeals(t *testing.T) {
	cacheDir, err := os.MkdirTemp("", "gobuildcache-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp directory: %v", err)
	}
	defer os.RemoveAll(cacheDir)

	backend := &emptyOutputIDBackend{}
	locker := locking.NewNoOpGroup()

	cp, err := NewCacheProg(backend, locker, cacheDir, false, false, false, false)
	if err != nil {
		t.Fatalf("Failed to create CacheProg: %v", err)
	}

	actionID := []byte("test-action-id-12345678")

	// GET hits the backend which returns empty outputID.
	// The S3 guard (s3.go) would normally catch this and return a miss,
	// but this tests the defense-in-depth: if a backend returns empty
	// outputID, the local cache entry is corrupted and self-heals via eviction.
	getReq := &Request{
		ID:       1,
		Command:  CmdGet,
		ActionID: actionID,
	}

	resp, err := cp.handleGet(getReq)
	if err != nil {
		t.Fatalf("handleGet returned error: %v", err)
	}

	// The backend returned data, so handleGet treats it as a hit
	if resp.Miss {
		t.Error("Expected backend hit to flow through")
	}

	// The local cache entry has corrupted metadata (empty outputID).
	// On next check(), readMetadata returns "metadata missing outputID field",
	// triggering eviction. This verifies the self-healing behavior.
	meta := cp.localCache.check(actionID)
	if meta != nil {
		t.Error("Expected corrupted local cache entry to be evicted on check(), got non-nil")
	}
}

func TestEvict_PermissionError_LogsWarningAndContinues(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Permission-based test not reliable on Windows")
	}

	cp, cacheDir := createTestCacheProg(t, false)
	defer func() {
		// Restore permissions so cleanup can succeed
		filepath.Walk(cacheDir, func(path string, info os.FileInfo, err error) error {
			if err == nil && info.IsDir() {
				os.Chmod(path, 0755)
			}
			return nil
		})
		os.RemoveAll(cacheDir)
	}()

	actionID := []byte("test-action-id-12345678")

	// Write a corrupted metadata file
	hexActionID := hex.EncodeToString(actionID)
	subdir := hexActionID[:2]
	hexID := fileFormatVersion + hexActionID
	dataPath := filepath.Join(cacheDir, subdir, hexID)
	metaPath := dataPath + ".meta"

	if err := os.WriteFile(dataPath, []byte("cached data"), 0644); err != nil {
		t.Fatalf("Failed to write data file: %v", err)
	}
	if err := os.WriteFile(metaPath, []byte("size:100\ntime:1234567890\n"), 0644); err != nil {
		t.Fatalf("Failed to write metadata file: %v", err)
	}

	// Make the subdirectory read-only so os.Remove fails with permission error
	subdirPath := filepath.Join(cacheDir, subdir)
	if err := os.Chmod(subdirPath, 0555); err != nil {
		t.Fatalf("Failed to chmod directory: %v", err)
	}

	// check() should return nil (cache miss) and attempt eviction.
	// evict() should log warnings but not panic.
	meta := cp.localCache.check(actionID)
	if meta != nil {
		t.Error("Expected nil (cache miss) for corrupted metadata, got non-nil")
	}

	// Files should still exist (eviction failed due to permissions)
	if _, err := os.Stat(dataPath); err != nil {
		t.Errorf("Expected data file to still exist (eviction should have failed): %v", err)
	}
	if _, err := os.Stat(metaPath); err != nil {
		t.Errorf("Expected meta file to still exist (eviction should have failed): %v", err)
	}
}
