package main

import (
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"path/filepath"
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

	cp, err := NewCacheProg(backend, locker, cacheDir, false, false, false, readOnly, "")
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

func TestPut_ExistingEntryWithDifferentOutput(t *testing.T) {
	cp, cacheDir := createTestCacheProg(t, true)
	defer os.RemoveAll(cacheDir)

	put := func(outputID []byte, body string) Response {
		t.Helper()
		resp, err := cp.handlePut(&Request{
			Command:  CmdPut,
			ActionID: []byte("test-action-id-12345678"),
			OutputID: outputID,
			Body:     strings.NewReader(body),
			BodySize: int64(len(body)),
		})
		if err != nil {
			t.Fatalf("handlePut returned error: %v", err)
		}
		if resp.Err != "" {
			t.Fatalf("Expected no error in response, got: %s", resp.Err)
		}
		return resp
	}

	// Go records the returned DiskPath as the file for the OutputID it sent, so a second PUT
	// of the same action ID with a different output must return a file holding that output.
	put([]byte("test-output-id-aaaaaaaa"), "first body")
	resp := put([]byte("test-output-id-bbbbbbbb"), "other body")

	got, err := os.ReadFile(resp.DiskPath)
	if err != nil {
		t.Fatalf("Failed to read DiskPath: %v", err)
	}
	if string(got) != "other body" {
		t.Errorf("DiskPath holds %q, expected %q", got, "other body")
	}
}

func TestWriteStatsFile(t *testing.T) {
	cp, cacheDir := createTestCacheProg(t, false)
	defer os.RemoveAll(cacheDir)

	actionID := []byte("test-action-id-12345678")
	get := func() Response {
		t.Helper()
		resp, err := cp.handleGet(&Request{Command: CmdGet, ActionID: actionID})
		if err != nil {
			t.Fatalf("handleGet returned error: %v", err)
		}
		return resp
	}

	if resp := get(); !resp.Miss {
		t.Fatalf("Expected first GET to miss")
	}
	body := "test body content"
	if _, err := cp.handlePut(&Request{
		Command:  CmdPut,
		ActionID: actionID,
		OutputID: []byte("test-output-id-12345678"),
		Body:     strings.NewReader(body),
		BodySize: int64(len(body)),
	}); err != nil {
		t.Fatalf("handlePut returned error: %v", err)
	}
	if resp := get(); resp.Miss {
		t.Fatalf("Expected second GET to hit")
	}

	got := writeAndReadStatsFile(t, cp)

	want := map[string]float64{
		"version":           1,
		"pid":               float64(os.Getpid()),
		"gets":              2,
		"hits":              1,
		"misses":            1,
		"local_hits":        1,
		"backend_hits":      0,
		"duplicate_gets":    1,
		"puts":              1,
		"duplicate_puts":    1,
		"unique_action_ids": 1,
		"failed_gets":       0,
		"failed_puts":       0,
	}
	for key, wantVal := range want {
		if got[key] != wantVal {
			t.Errorf("%s = %v, expected %v", key, got[key], wantVal)
		}
	}

	latencies, ok := got["latencies"].([]any)
	if !ok {
		t.Fatalf("latencies is %T, expected an array", got["latencies"])
	}
	var getOverall map[string]any
	for _, l := range latencies {
		if entry := l.(map[string]any); entry["operation"] == "get_overall" {
			getOverall = entry
		}
	}
	if getOverall == nil {
		t.Fatalf("No get_overall latency entry in %v", latencies)
	}
	if getOverall["count"] != float64(2) {
		t.Errorf("get_overall count = %v, expected 2", getOverall["count"])
	}
}

// writeAndReadStatsFile writes cp's stats file into a fresh, not yet existing
// directory, checks that it is the only file there, and decodes it into a
// generic map so callers check the JSON field names.
func writeAndReadStatsFile(t *testing.T, cp *CacheProg) map[string]any {
	t.Helper()

	statsDir := filepath.Join(t.TempDir(), "nested", "stats")
	path, err := cp.writeStatsFile(statsDir)
	if err != nil {
		t.Fatalf("writeStatsFile returned error: %v", err)
	}

	entries, err := os.ReadDir(statsDir)
	if err != nil {
		t.Fatalf("Failed to read stats dir: %v", err)
	}
	if len(entries) != 1 || filepath.Join(statsDir, entries[0].Name()) != path {
		t.Fatalf("Expected exactly one file %s in stats dir, got %v", path, entries)
	}
	if matched, _ := filepath.Match("gobuildcache-stats-*-*.json", entries[0].Name()); !matched {
		t.Errorf("Unexpected stats file name: %s", entries[0].Name())
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("Failed to read stats file: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("Failed to parse stats file: %v\n%s", err, data)
	}
	return got
}

func TestWriteStatsFile_FailureCounters(t *testing.T) {
	tests := []struct {
		name    string
		backend func() backends.Backend
	}{
		// The backend PUT fails synchronously inside handlePut.
		{"sync backend", func() backends.Backend {
			return backends.NewError(backends.NewNoop(), 1.0)
		}},
		// The backend PUT fails later in the async writer, which -debug hides
		// behind the Debug wrapper, as createBackend arranges them.
		{"async backend behind debug", func() backends.Backend {
			logger := slog.New(slog.NewTextHandler(io.Discard, nil))
			return backends.NewDebug(backends.NewAsyncBackendWriter(backends.NewError(backends.NewNoop(), 1.0), logger))
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			backend := tt.backend()
			cp, err := NewCacheProg(backend, locking.NewNoOpGroup(), t.TempDir(), false, false, false, false, "")
			if err != nil {
				t.Fatalf("Failed to create CacheProg: %v", err)
			}

			// Local miss, then the backend GET fails.
			if _, err := cp.handleGet(&Request{Command: CmdGet, ActionID: []byte("action-get")}); err == nil {
				t.Fatalf("Expected handleGet to fail")
			}
			// The local write succeeds, then the backend PUT fails.
			body := "test body content"
			if _, err := cp.handlePut(&Request{
				Command:  CmdPut,
				ActionID: []byte("action-backend-put"),
				OutputID: []byte("output-1"),
				Body:     strings.NewReader(body),
				BodySize: int64(len(body)),
			}); err != nil {
				t.Fatalf("handlePut returned error: %v", err)
			}
			// The body is shorter than BodySize, so handlePut itself fails.
			if _, err := cp.handlePut(&Request{
				Command:  CmdPut,
				ActionID: []byte("action-bad-put"),
				OutputID: []byte("output-2"),
				Body:     strings.NewReader("short"),
				BodySize: 100,
			}); err == nil {
				t.Fatalf("Expected handlePut to fail on a short body")
			}

			// Run handles the close command, which waits for async PUTs,
			// before the stats file is written. The error backend also fails
			// Close, which doesn't matter here.
			backend.Close()

			got := writeAndReadStatsFile(t, cp)
			want := map[string]float64{
				"gets":                1,
				"misses":              1,
				"failed_gets":         1,
				"puts":                2,
				"failed_puts":         1,
				"failed_backend_puts": 1,
			}
			for key, wantVal := range want {
				if got[key] != wantVal {
					t.Errorf("%s = %v, expected %v", key, got[key], wantVal)
				}
			}
		})
	}
}
