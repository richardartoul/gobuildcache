package backends

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"strconv"
	"time"

	"cloud.google.com/go/storage"
	"google.golang.org/api/iterator"
)

// GCS implements Backend using Google Cloud Storage.
// This backend only handles GCS operations; local disk caching is handled by server.go.
type GCS struct {
	client *storage.Client
	bucket *storage.BucketHandle
	prefix string
	ctx    context.Context
}

// NewGCS creates a new GCS-based cache backend.
// bucket is the GCS bucket name where cache files will be stored.
// prefix is an optional prefix for all GCS object names (e.g., "cache/" or "").
func NewGCS(bucket, prefix string) (*GCS, error) {
	ctx := context.Background()

	// Create GCS client using Application Default Credentials.
	// WithJSONReads forces the JSON API for downloads (default is XML).
	// This is required for GCS Anywhere Cache compatibility.
	client, err := storage.NewClient(ctx, storage.WithJSONReads())
	if err != nil {
		return nil, fmt.Errorf("failed to create GCS client: %w", err)
	}

	bucketHandle := client.Bucket(bucket)

	backend := &GCS{
		client: client,
		bucket: bucketHandle,
		prefix: prefix,
		ctx:    ctx,
	}

	// Test bucket access by checking if bucket exists
	_, err = bucketHandle.Attrs(ctx)
	if err != nil {
		client.Close()
		return nil, fmt.Errorf("failed to access GCS bucket %s: %w", bucket, err)
	}

	return backend, nil
}

// Put stores an object in GCS.
func (g *GCS) Put(actionID, outputID []byte, body io.Reader, bodySize int64) error {
	key := g.actionIDToKey(actionID)
	obj := g.bucket.Object(key)

	// Create a writer for the object
	writer := obj.NewWriter(g.ctx)
	defer writer.Close()

	// Set metadata
	writer.Metadata = map[string]string{
		"outputid": hex.EncodeToString(outputID),
		"size":     strconv.FormatInt(bodySize, 10),
		"time":     strconv.FormatInt(time.Now().Unix(), 10),
	}

	// Copy the body to the writer
	if bodySize > 0 && body != nil {
		written, err := io.CopyN(writer, body, bodySize)
		if err != nil && err != io.EOF {
			return fmt.Errorf("failed to write body to GCS: %w", err)
		}
		if written != bodySize {
			return fmt.Errorf("size mismatch: expected %d, wrote %d", bodySize, written)
		}
	}

	// Close the writer to finalize the upload
	if err := writer.Close(); err != nil {
		return fmt.Errorf("failed to close GCS writer: %w", err)
	}

	return nil
}

// Get retrieves an object from GCS.
// Returns the object data as an io.ReadCloser that must be closed by the caller.
func (g *GCS) Get(actionID []byte) ([]byte, io.ReadCloser, int64, *time.Time, bool, error) {
	key := g.actionIDToKey(actionID)
	obj := g.bucket.Object(key)

	// Get object attributes first to check if it exists and get metadata
	attrs, err := obj.Attrs(g.ctx)
	if err != nil {
		if errors.Is(err, storage.ErrObjectNotExist) {
			return nil, nil, 0, nil, true, nil
		}
		return nil, nil, 0, nil, true, fmt.Errorf("failed to get GCS object attrs: %w", err)
	}

	// Parse metadata
	outputIDHex := attrs.Metadata["outputid"]
	sizeStr := attrs.Metadata["size"]
	timeStr := attrs.Metadata["time"]

	outputID, err := hex.DecodeString(outputIDHex)
	if err != nil {
		return nil, nil, 0, nil, true, nil
	}

	size, err := strconv.ParseInt(sizeStr, 10, 64)
	if err != nil {
		// Fallback to actual object size if metadata is missing
		size = attrs.Size
	}

	var putTime *time.Time
	if timeStr != "" {
		putTimeUnix, err := strconv.ParseInt(timeStr, 10, 64)
		if err == nil {
			t := time.Unix(putTimeUnix, 0)
			putTime = &t
		}
	}
	// Fallback to object creation time if metadata is missing
	if putTime == nil {
		putTime = &attrs.Created
	}

	// Get a reader for the object
	reader, err := obj.NewReader(g.ctx)
	if err != nil {
		if errors.Is(err, storage.ErrObjectNotExist) {
			return nil, nil, 0, nil, true, nil
		}
		return nil, nil, 0, nil, true, fmt.Errorf("failed to get GCS object reader: %w", err)
	}

	// Return the GCS object body as a ReadCloser
	// The caller is responsible for closing it
	return outputID, reader, size, putTime, false, nil
}

// Close performs cleanup operations.
func (g *GCS) Close() error {
	if g.client != nil {
		return g.client.Close()
	}
	return nil
}

// Clear removes all entries from the cache in GCS.
func (g *GCS) Clear() error {
	// List all objects with the prefix
	query := &storage.Query{
		Prefix: g.prefix,
	}

	it := g.bucket.Objects(g.ctx, query)

	// Collect objects to delete (GCS allows up to 100 objects per batch delete)
	var objectsToDelete []string
	for {
		attrs, err := it.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			return fmt.Errorf("failed to list GCS objects: %w", err)
		}
		objectsToDelete = append(objectsToDelete, attrs.Name)
	}

	if len(objectsToDelete) == 0 {
		return nil
	}

	// Delete objects in batches (GCS allows up to 100 objects per batch)
	batchSize := 100
	for i := 0; i < len(objectsToDelete); i += batchSize {
		end := i + batchSize
		if end > len(objectsToDelete) {
			end = len(objectsToDelete)
		}
		batch := objectsToDelete[i:end]

		// Delete each object in the batch
		for _, objName := range batch {
			obj := g.bucket.Object(objName)
			if err := obj.Delete(g.ctx); err != nil {
				// Continue deleting other objects even if one fails
				// Log error but don't fail the entire operation
				_ = err
			}
		}
	}

	return nil
}

// actionIDToKey converts an actionID to a GCS object name.
func (g *GCS) actionIDToKey(actionID []byte) string {
	hexID := hex.EncodeToString(actionID)
	if g.prefix != "" {
		return g.prefix + hexID
	}
	return hexID
}
