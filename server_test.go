package main

import (
	"os"
	"strings"
	"testing"

	"github.com/richardartoul/gobuildcache/pkg/backends"
	"github.com/richardartoul/gobuildcache/pkg/locking"
)

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

	// Verify skippedPuts counter was incremented
	if cp.skippedPuts.Load() != 1 {
		t.Errorf("Expected skippedPuts to be 1, got: %d", cp.skippedPuts.Load())
	}

	// Verify putCount was NOT incremented (since we skipped)
	if cp.putCount.Load() != 0 {
		t.Errorf("Expected putCount to be 0 (skipped), got: %d", cp.putCount.Load())
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

	// Execute multiple PUTs
	for i := 0; i < 5; i++ {
		req := &Request{
			ID:       int64(i),
			Command:  CmdPut,
			ActionID: []byte("test-action-id-12345678"),
			OutputID: []byte("test-output-id-12345678"),
			Body:     strings.NewReader("test body"),
			BodySize: 9,
		}

		_, err := cp.handlePut(req)
		if err != nil {
			t.Fatalf("handlePut %d returned error: %v", i, err)
		}
	}

	// Verify all 5 puts were skipped
	if cp.skippedPuts.Load() != 5 {
		t.Errorf("Expected skippedPuts to be 5, got: %d", cp.skippedPuts.Load())
	}

	// Verify putCount is still 0
	if cp.putCount.Load() != 0 {
		t.Errorf("Expected putCount to be 0, got: %d", cp.putCount.Load())
	}
}
