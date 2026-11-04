package backends

import (
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"
)

// scriptedBackend fails or succeeds each call according to fail, and counts calls.
type scriptedBackend struct {
	fail  bool
	gets  int
	puts  int
	clear int
}

func (b *scriptedBackend) Put(actionID, outputID []byte, body io.Reader, bodySize int64) error {
	b.puts++
	if b.fail {
		return errors.New("put failed")
	}
	return nil
}

func (b *scriptedBackend) Get(actionID []byte) ([]byte, io.ReadCloser, int64, *time.Time, bool, error) {
	b.gets++
	if b.fail {
		return nil, nil, 0, nil, true, errors.New("get failed")
	}
	return []byte("out"), io.NopCloser(strings.NewReader("x")), 1, &time.Time{}, false, nil
}

func (b *scriptedBackend) Close() error { return nil }

func (b *scriptedBackend) Clear() error {
	b.clear++
	if b.fail {
		return errors.New("clear failed")
	}
	return nil
}

func TestFallback(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	t.Run("errors become misses and dropped puts", func(t *testing.T) {
		inner := &scriptedBackend{fail: true}
		f := NewFallback(inner, 5, logger)

		_, body, _, _, miss, err := f.Get([]byte("a"))
		if err != nil || !miss || body != nil {
			t.Fatalf("Get = (body %v, miss %v, err %v), want a miss without error", body, miss, err)
		}
		if err := f.Put([]byte("a"), []byte("o"), strings.NewReader("x"), 1); err != nil {
			t.Fatalf("Put err = %v, want nil", err)
		}
	})

	t.Run("stops calling the backend after consecutive errors", func(t *testing.T) {
		inner := &scriptedBackend{fail: true}
		f := NewFallback(inner, 3, logger)

		for range 3 {
			f.Get([]byte("a"))
		}
		inner.fail = false
		for range 10 {
			f.Get([]byte("a"))
			f.Put([]byte("a"), []byte("o"), strings.NewReader("x"), 1)
		}
		if inner.gets != 3 || inner.puts != 0 {
			t.Errorf("backend got %d gets and %d puts after tripping, want 3 and 0", inner.gets, inner.puts)
		}
		if _, _, _, _, miss, _ := f.Get([]byte("a")); !miss {
			t.Errorf("Get after tripping is a hit, want a miss")
		}
	})

	t.Run("a success resets the error count", func(t *testing.T) {
		inner := &scriptedBackend{}
		f := NewFallback(inner, 3, logger)

		for range 5 {
			inner.fail = true
			f.Get([]byte("a"))
			f.Get([]byte("a"))
			inner.fail = false
			f.Get([]byte("a"))
		}
		if inner.gets != 15 {
			t.Errorf("backend got %d gets, want all 15: two errors in a row must not trip a threshold of 3", inner.gets)
		}
		if _, _, _, _, miss, _ := f.Get([]byte("a")); miss {
			t.Errorf("Get is a miss, want the backend's hit")
		}
	})

	t.Run("clear errors are not hidden", func(t *testing.T) {
		f := NewFallback(&scriptedBackend{fail: true}, 1, logger)

		if err := f.Clear(); err == nil {
			t.Errorf("Clear err = nil, want the backend's error")
		}
	})
}
