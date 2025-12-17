# Testing Guide

## Running Tests

### Unit Tests

Run all unit tests:
```bash
go test ./...
```

Run with race detector:
```bash
go test -race ./...
```

Run backend tests specifically:
```bash
cd backends
go test -v -race
```

### Integration Tests

#### Disk Backend Integration Test

The disk backend integration test runs by default:

```bash
go test -v -run TestCacheIntegration
```

#### S3 Backend Integration Tests

The S3 integration tests require:
1. AWS/S3-compatible credentials set as environment variables
2. An existing S3 bucket
3. The `TEST_S3_BUCKET` environment variable set

**Basic usage:**

```bash
# Set AWS credentials
export AWS_ACCESS_KEY_ID=your_access_key
export AWS_SECRET_ACCESS_KEY=your_secret_key
export AWS_REGION=us-east-1
export TEST_S3_BUCKET=your-bucket-name

# Run S3 integration test
go test -v -run TestCacheIntegrationS3$ -timeout 5m
```

**Using Tigris:**

```bash
# Source credentials from file
source keys/tigris.txt
export TEST_S3_BUCKET=your-bucket-name

# Run tests
go test -v -run TestCacheIntegrationS3$ -timeout 5m
```

**One-liner:**

```bash
AWS_ACCESS_KEY_ID=xxx AWS_SECRET_ACCESS_KEY=yyy AWS_REGION=us-east-1 TEST_S3_BUCKET=my-bucket go test -v -run TestCacheIntegrationS3$ -timeout 5m
```

**Run S3 concurrent test:**

```bash
go test -v -run TestCacheIntegrationS3Concurrent -timeout 5m
```

**Run all S3 tests:**

```bash
go test -v -run TestCacheIntegrationS3 -timeout 5m
```

**Skip S3 tests:**

S3 tests will only skip when running with the `-short` flag:

```bash
go test -short ./...  # Skips S3 tests
```

If you run S3 tests without credentials set, they will **fail** (not skip):

```bash
# This will fail if credentials not set
go test -run TestCacheIntegrationS3

# To skip S3 tests, use -short
go test -short -run TestCacheIntegrationS3
```

### Test Coverage

Generate coverage report:
```bash
go test -coverprofile=coverage.out ./...
go tool cover -html=coverage.out
```

## Test Structure

### Unit Tests

- `backends/disk_test.go` - Disk backend concurrency and atomicity tests
- Tests verify:
  - Concurrent Put operations on same key
  - Concurrent Put operations on different keys
  - Concurrent Get operations during Put
  - Atomic file operations
  - Cache clearing

### Integration Tests

- `integration_test.go` - Disk backend end-to-end test
- `integration_s3_test.go` - S3 backend end-to-end tests
- Tests verify:
  - Full cache workflow (put, get, cache hits)
  - Go build cache protocol compatibility
  - Concurrent access patterns

## Continuous Integration

For CI environments, you can:

1. **Skip S3 tests** (default if credentials not provided):
```yaml
- name: Run tests
  run: go test -v ./...
```

2. **Run S3 tests with secrets**:
```yaml
- name: Run S3 tests
  env:
    AWS_ACCESS_KEY_ID: ${{ secrets.AWS_ACCESS_KEY_ID }}
    AWS_SECRET_ACCESS_KEY: ${{ secrets.AWS_SECRET_ACCESS_KEY }}
    AWS_REGION: us-east-1
    TEST_S3_BUCKET: ci-test-bucket
  run: go test -v -run TestCacheIntegrationS3
```

## Benchmarks

Run backend benchmarks:
```bash
cd backends
go test -bench=. -benchmem
```

Example output:
```
BenchmarkDiskPutConcurrent-8   	   50000	     35421 ns/op	    1234 B/op	      15 allocs/op
```

## Troubleshooting Tests

### S3 Tests Fail with "bucket not found"

Make sure the bucket exists and you have access:
```bash
# Using AWS CLI
aws s3 ls s3://your-bucket-name

# Or create the bucket
aws s3 mb s3://your-bucket-name
```

### S3 Tests Timeout

Increase timeout:
```bash
go test -v -run TestCacheIntegrationS3 -timeout 10m
```

### Race Detector Issues

If race detector reports issues:
```bash
go test -race -v ./...
```

The disk backend is designed to be race-free. Any race conditions should be investigated.

### Debug Test Failures

Enable verbose output and debug logging:
```bash
# For integration tests
DEBUG=true go test -v -run TestCacheIntegration

# For S3 tests with server output
go test -v -run TestCacheIntegrationS3 2>&1 | tee test.log
```

## Test Credentials

S3 tests use standard AWS environment variables:
- `AWS_ACCESS_KEY_ID` - Your AWS access key
- `AWS_SECRET_ACCESS_KEY` - Your AWS secret key
- `AWS_REGION` - AWS region (e.g., `us-east-1`)
- `AWS_ENDPOINT_URL_S3` - (Optional) Custom S3 endpoint for S3-compatible services
- `TEST_S3_BUCKET` - S3 bucket name for tests

**Note:** Never commit credentials to version control. Use environment variables or a local credentials file that is gitignored.

### Creating Test Buckets

**Tigris:**
```bash
# Using Tigris CLI
tigris bucket create your-bucket-name
```

**AWS S3:**
```bash
aws s3 mb s3://your-bucket-name --region us-east-1
```

## Test Cleanup

S3 integration tests automatically clean up test data after running. If a test is interrupted, you may need to manually clean up:

```bash
# Using the cache server
./builds/gobuildcache clear -backend=s3 -s3-bucket=your-bucket -s3-prefix=test-cache-

# Or using AWS CLI
aws s3 rm s3://your-bucket/test-cache- --recursive
```

