package backends

import (
	"io"
	"time"
)

// Backend defines the interface for cache storage backends.
//
// Implementations can be swapped to use different storage mechanisms.
// The backend is responsible ONLY for storing/retrieving data from its storage system.
// The server (server.go) is responsible for managing the local disk cache that Go accesses.
//
// Implementations must be thread-safe and support concurrent operations,
// but the caller (server.go) guarantees that there will never be two
// inflight operations of the same type for the same actionID (singleflight)
// which makes implementing the backends simpler (no need to worry about
// locking at the filesystem layer).
type Backend interface {
	// Put stores an object in the backend storage.
	// actionID is the cache key, outputID is stored with the body,
	// body is the content to store, and bodySize is the size in bytes.
	// The backend stores the data in its storage system and returns nil on success.
	Put(actionID, outputID []byte, body io.Reader, bodySize int64) error

	// Get retrieves an object from the backend storage.
	// actionID is the cache key to look up.
	// Returns outputID, body (as io.ReadCloser), size, putTime, and whether it was a miss.
	// The caller is responsible for closing the returned ReadCloser.
	// On a cache miss, returns miss=true and body=nil.
	Get(actionID []byte) (outputID []byte, body io.ReadCloser, size int64, putTime *time.Time, miss bool, err error)

	// Close performs any cleanup operations needed by the backend.
	Close() error

	// Clear removes all entries from the cache backend storage.
	Clear() error
}
