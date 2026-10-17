package main

import (
	"bytes"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// TestLocalCacheConcurrentWritesOfOneActionID writes the same entry from many goroutines at once,
// as separate gobuildcache processes do when nothing serializes them (LOCK_TYPE=memory, or a
// writer that gave up waiting for the file lock). Every write must succeed, the entry must hold
// the complete body, and no temporary file may be left behind.
func TestLocalCacheConcurrentWritesOfOneActionID(t *testing.T) {
	lc, cacheErr := newLocalCache(t.TempDir(), slog.New(slog.NewTextHandler(io.Discard, nil)))
	if cacheErr != nil {
		t.Fatalf("newLocalCache: %v", cacheErr)
	}

	var (
		actionID = bytes.Repeat([]byte{0xab}, 32)
		body     = bytes.Repeat([]byte("payload "), 1<<16)
		meta     = localCacheMetadata{
			OutputID: bytes.Repeat([]byte{0xcd}, 32),
			Size:     int64(len(body)),
			PutTime:  time.Unix(1700000000, 0),
		}
		writers = 32
		wg      sync.WaitGroup
		errs    = make(chan error, writers)
	)
	for range writers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, writeErr := lc.writeWithMetadata(actionID, bytes.NewReader(body), meta); writeErr != nil {
				errs <- writeErr
			}
		}()
	}
	wg.Wait()
	close(errs)
	for writeErr := range errs {
		t.Errorf("concurrent write failed: %v", writeErr)
	}

	got, readErr := os.ReadFile(lc.getPath(actionID))
	if readErr != nil {
		t.Fatalf("reading the entry: %v", readErr)
	}
	if !bytes.Equal(got, body) {
		t.Errorf("entry holds %d bytes, want the complete %d-byte body", len(got), len(body))
	}
	if gotMeta := lc.check(actionID); gotMeta == nil || !bytes.Equal(gotMeta.OutputID, meta.OutputID) || gotMeta.Size != meta.Size {
		t.Errorf("metadata = %+v, want %+v", gotMeta, meta)
	}

	entries, readDirErr := os.ReadDir(filepath.Dir(lc.getPath(actionID)))
	if readDirErr != nil {
		t.Fatalf("reading the cache subdirectory: %v", readDirErr)
	}
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".tmp") {
			t.Errorf("temporary file left behind: %s", e.Name())
		}
	}
}
