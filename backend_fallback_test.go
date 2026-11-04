package main

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/richardartoul/gobuildcache/pkg/backends"
)

func TestCreateBackendStartupFallback(t *testing.T) {
	// An S3 endpoint that rejects every request, so the bucket check in NewS3 fails.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer srv.Close()

	t.Setenv("AWS_ENDPOINT_URL_S3", srv.URL)
	t.Setenv("GOBUILDCACHE_AWS_REGION", "us-east-1")
	t.Setenv("GOBUILDCACHE_AWS_ACCESS_KEY_ID", "test")
	t.Setenv("GOBUILDCACHE_AWS_SECRET_ACCESS_KEY", "test")
	t.Setenv("GOBUILDCACHE_AWS_S3_USE_PATH_STYLE", "true")

	oldType, oldBucket, oldAsync, oldFallback := backendType, s3Bucket, asyncBackend, backendFallback
	t.Cleanup(func() {
		backendType, s3Bucket, asyncBackend, backendFallback = oldType, oldBucket, oldAsync, oldFallback
	})
	backendType, s3Bucket, asyncBackend = "s3", "missing-bucket", false

	t.Run("fails without fallback", func(t *testing.T) {
		backendFallback = false
		if _, err := createBackend(); err == nil {
			t.Fatal("createBackend err = nil, want the bucket check error")
		}
	})

	t.Run("uses only the local cache with fallback", func(t *testing.T) {
		backendFallback = true
		backend, err := createBackend()
		if err != nil {
			t.Fatalf("createBackend err = %v, want nil", err)
		}
		defer backend.Close()

		if _, ok := backend.(*backends.Fallback); !ok {
			t.Fatalf("backend is %T, want *backends.Fallback", backend)
		}
		if _, _, _, _, miss, err := backend.Get([]byte("action")); err != nil || !miss {
			t.Errorf("Get = (miss %v, err %v), want a miss without error", miss, err)
		}
	})
}
