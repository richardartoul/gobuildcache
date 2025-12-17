package main

import (
	"bytes"
	"context"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
)

// S3Backend implements CacheBackend using AWS S3.
type S3Backend struct {
	client    *s3.Client
	bucket    string
	prefix    string
	tmpDir    string
	ctx       context.Context
	awsConfig aws.Config
}

// NewS3Backend creates a new S3-based cache backend.
// bucket is the S3 bucket name where cache files will be stored.
// prefix is an optional prefix for all S3 keys (e.g., "cache/" or "").
// tmpDir is the local directory for downloading files (for Go to access).
func NewS3Backend(bucket, prefix, tmpDir string) (*S3Backend, error) {
	ctx := context.Background()

	// Load AWS config from environment/credentials
	cfg, err := config.LoadDefaultConfig(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to load AWS config: %w", err)
	}

	client := s3.NewFromConfig(cfg)

	// Create temp directory if it doesn't exist
	if tmpDir == "" {
		tmpDir = filepath.Join(os.TempDir(), "gobuildcache-s3")
	}
	if err := os.MkdirAll(tmpDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create temp directory: %w", err)
	}

	backend := &S3Backend{
		client:    client,
		bucket:    bucket,
		prefix:    prefix,
		tmpDir:    tmpDir,
		ctx:       ctx,
		awsConfig: cfg,
	}

	// Test bucket access
	_, err = client.HeadBucket(ctx, &s3.HeadBucketInput{
		Bucket: aws.String(bucket),
	})
	if err != nil {
		return nil, fmt.Errorf("failed to access S3 bucket %s: %w", bucket, err)
	}

	return backend, nil
}

// Put stores an object in S3.
func (s *S3Backend) Put(actionID, outputID []byte, body io.Reader, bodySize int64) (string, error) {
	key := s.actionIDToKey(actionID)

	// Read the body into a buffer (needed for S3 SDK)
	var bodyData []byte
	if bodySize > 0 && body != nil {
		bodyData = make([]byte, bodySize)
		n, err := io.ReadFull(body, bodyData)
		if err != nil && err != io.EOF {
			return "", fmt.Errorf("failed to read body: %w", err)
		}
		if int64(n) != bodySize {
			return "", fmt.Errorf("size mismatch: expected %d, read %d", bodySize, n)
		}
	}

	// Prepare metadata
	now := time.Now()
	metadata := map[string]string{
		"outputid": hex.EncodeToString(outputID),
		"size":     strconv.FormatInt(bodySize, 10),
		"time":     strconv.FormatInt(now.Unix(), 10),
	}

	// Upload to S3
	putInput := &s3.PutObjectInput{
		Bucket:   aws.String(s.bucket),
		Key:      aws.String(key),
		Body:     bytes.NewReader(bodyData),
		Metadata: metadata,
	}

	_, err := s.client.PutObject(s.ctx, putInput)
	if err != nil {
		return "", fmt.Errorf("failed to upload to S3: %w", err)
	}

	// Download to local temp file for Go to access
	diskPath := s.actionIDToLocalPath(actionID)
	if err := os.MkdirAll(filepath.Dir(diskPath), 0755); err != nil {
		return "", fmt.Errorf("failed to create local directory: %w", err)
	}

	if err := os.WriteFile(diskPath, bodyData, 0644); err != nil {
		return "", fmt.Errorf("failed to write local file: %w", err)
	}

	absPath, err := filepath.Abs(diskPath)
	if err != nil {
		absPath = diskPath
	}

	return absPath, nil
}

// Get retrieves an object from S3.
func (s *S3Backend) Get(actionID []byte) ([]byte, string, int64, *time.Time, bool, error) {
	key := s.actionIDToKey(actionID)

	// Try to get object metadata from S3
	headInput := &s3.HeadObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(key),
	}

	headOutput, err := s.client.HeadObject(s.ctx, headInput)
	if err != nil {
		// Check if it's a not found error
		if s.isNotFoundError(err) {
			return nil, "", 0, nil, true, nil
		}
		return nil, "", 0, nil, true, fmt.Errorf("failed to check S3 object: %w", err)
	}

	// Parse metadata
	outputIDHex := headOutput.Metadata["outputid"]
	sizeStr := headOutput.Metadata["size"]
	timeStr := headOutput.Metadata["time"]

	outputID, err := hex.DecodeString(outputIDHex)
	if err != nil {
		return nil, "", 0, nil, true, nil
	}

	size, err := strconv.ParseInt(sizeStr, 10, 64)
	if err != nil {
		return nil, "", 0, nil, true, nil
	}

	putTimeUnix, err := strconv.ParseInt(timeStr, 10, 64)
	if err != nil {
		return nil, "", 0, nil, true, nil
	}
	putTime := time.Unix(putTimeUnix, 0)

	// Check if we have the file locally
	diskPath := s.actionIDToLocalPath(actionID)
	if _, err := os.Stat(diskPath); os.IsNotExist(err) {
		// Download from S3 to local temp file
		if err := s.downloadFromS3(key, diskPath); err != nil {
			return nil, "", 0, nil, true, fmt.Errorf("failed to download from S3: %w", err)
		}
	}

	absPath, err := filepath.Abs(diskPath)
	if err != nil {
		absPath = diskPath
	}

	return outputID, absPath, size, &putTime, false, nil
}

// Close performs cleanup operations.
func (s *S3Backend) Close() error {
	// Optionally clean up temp files
	// We'll leave them for now as they might be useful
	return nil
}

// Clear removes all entries from the cache in S3.
func (s *S3Backend) Clear() error {
	// List all objects with the prefix
	listInput := &s3.ListObjectsV2Input{
		Bucket: aws.String(s.bucket),
		Prefix: aws.String(s.prefix),
	}

	paginator := s3.NewListObjectsV2Paginator(s.client, listInput)

	var deleteObjects []types.ObjectIdentifier
	for paginator.HasMorePages() {
		page, err := paginator.NextPage(s.ctx)
		if err != nil {
			return fmt.Errorf("failed to list S3 objects: %w", err)
		}

		for _, obj := range page.Contents {
			deleteObjects = append(deleteObjects, types.ObjectIdentifier{
				Key: obj.Key,
			})
		}
	}

	if len(deleteObjects) == 0 {
		return nil
	}

	// Delete objects (S3 allows up to 1000 objects per request)
	for i := 0; i < len(deleteObjects); i += 1000 {
		end := i + 1000
		if end > len(deleteObjects) {
			end = len(deleteObjects)
		}
		batch := deleteObjects[i:end]

		deleteInput := &s3.DeleteObjectsInput{
			Bucket: aws.String(s.bucket),
			Delete: &types.Delete{
				Objects: batch,
				Quiet:   aws.Bool(true),
			},
		}

		_, err := s.client.DeleteObjects(s.ctx, deleteInput)
		if err != nil {
			return fmt.Errorf("failed to delete S3 objects: %w", err)
		}
	}

	// Also clear local temp files
	if err := os.RemoveAll(s.tmpDir); err != nil && !os.IsNotExist(err) {
		// Ignore error, temp files are just a cache
	}
	if err := os.MkdirAll(s.tmpDir, 0755); err != nil {
		// Ignore error, will be created on next Put
	}

	return nil
}

// actionIDToKey converts an actionID to an S3 key.
func (s *S3Backend) actionIDToKey(actionID []byte) string {
	hexID := hex.EncodeToString(actionID)
	if s.prefix != "" {
		return s.prefix + hexID
	}
	return hexID
}

// actionIDToLocalPath converts an actionID to a local file path.
func (s *S3Backend) actionIDToLocalPath(actionID []byte) string {
	hexID := hex.EncodeToString(actionID)
	return filepath.Join(s.tmpDir, hexID)
}

// downloadFromS3 downloads an object from S3 to a local file.
func (s *S3Backend) downloadFromS3(key, localPath string) error {
	getInput := &s3.GetObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(key),
	}

	result, err := s.client.GetObject(s.ctx, getInput)
	if err != nil {
		return fmt.Errorf("failed to get object from S3: %w", err)
	}
	defer result.Body.Close()

	// Create local file
	if err := os.MkdirAll(filepath.Dir(localPath), 0755); err != nil {
		return fmt.Errorf("failed to create local directory: %w", err)
	}

	file, err := os.Create(localPath)
	if err != nil {
		return fmt.Errorf("failed to create local file: %w", err)
	}
	defer file.Close()

	// Copy from S3 to local file
	_, err = io.Copy(file, result.Body)
	if err != nil {
		os.Remove(localPath)
		return fmt.Errorf("failed to write local file: %w", err)
	}

	return nil
}

// isNotFoundError checks if an error is a "not found" error from S3.
func (s *S3Backend) isNotFoundError(err error) bool {
	if err == nil {
		return false
	}
	// Check for common not found error types
	errMsg := err.Error()
	return bytes.Contains([]byte(errMsg), []byte("NotFound")) ||
		bytes.Contains([]byte(errMsg), []byte("NoSuchKey"))
}
