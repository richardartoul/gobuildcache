# Go Build Cache Server

A remote caching server for Go builds that supports multiple storage backends.

## Features

- **Multiple Storage Backends**: Choose between local disk storage or S3 cloud storage
- **Go Build Cache Protocol**: Compatible with Go's remote cache protocol (`GOCACHEPROG`)
- **Simple Configuration**: Environment variable-based configuration
- **Debug Mode**: Optional debug logging for troubleshooting

## Storage Backends

### Disk Backend (Default)

Stores cache files on the local filesystem.

**Configuration:**
```bash
export BACKEND_TYPE=disk          # Optional, disk is the default
export CACHE_DIR=/path/to/cache   # Optional, defaults to /tmp/gobuildcache
export DEBUG=true                 # Optional, enables debug logging
```

**Example:**
```bash
export CACHE_DIR=/var/cache/gobuildcache
./builds/gobuildcache
```

### S3 Backend

Stores cache files in Amazon S3 (or S3-compatible storage).

**Configuration:**
```bash
export BACKEND_TYPE=s3
export S3_BUCKET=my-build-cache-bucket      # Required
export S3_PREFIX=cache/                     # Optional, defaults to ""
export S3_TMP_DIR=/tmp/gobuildcache-s3      # Optional, for local file cache
export DEBUG=true                           # Optional, enables debug logging

# AWS credentials (use standard AWS environment variables or ~/.aws/credentials)
export AWS_REGION=us-east-1
export AWS_ACCESS_KEY_ID=your_access_key
export AWS_SECRET_ACCESS_KEY=your_secret_key
# Or use AWS profiles:
export AWS_PROFILE=your-profile
```

**Example:**
```bash
export BACKEND_TYPE=s3
export S3_BUCKET=my-team-build-cache
export S3_PREFIX=go-builds/
export AWS_REGION=us-east-1
./builds/gobuildcache
```

**How S3 Backend Works:**
1. Cache objects are stored in S3 with metadata (outputID, size, timestamp)
2. On cache hits, files are downloaded to a local temp directory for Go to access
3. The local temp directory acts as a secondary cache to avoid repeated S3 downloads
4. The `diskPath` returned to Go points to the locally cached file

## Usage

### Running the Server

Start the cache server:
```bash
./builds/gobuildcache
```

The server will:
1. Read from stdin (Go sends cache requests)
2. Write responses to stdout
3. Log debug information to stderr (if `DEBUG=true`)

### Configuring Go to Use the Cache

Set the `GOCACHEPROG` environment variable to point to the cache server:

```bash
export GOCACHEPROG=/path/to/gobuildcache/builds/gobuildcache
go build ./...
```

### Clearing the Cache

Clear all cache entries:
```bash
./builds/gobuildcache clear
```

This works with both disk and S3 backends based on your `BACKEND_TYPE` configuration.

## Building

Build the cache server:
```bash
make build
```

Or manually:
```bash
go build -o builds/gobuildcache
```

## Architecture

### CacheBackend Interface

All backends implement the `CacheBackend` interface:

```go
type CacheBackend interface {
    // Put stores an object in the cache
    Put(actionID, outputID []byte, body io.Reader, bodySize int64) (diskPath string, err error)
    
    // Get retrieves an object from the cache
    Get(actionID []byte) (outputID []byte, diskPath string, size int64, putTime *time.Time, miss bool, err error)
    
    // Close performs cleanup operations
    Close() error
    
    // Clear removes all cache entries
    Clear() error
}
```

### Available Backends

- **DiskBackend** (`disk_backend.go`): Local filesystem storage
- **S3Backend** (`s3_backend.go`): AWS S3 storage with local caching

## AWS Configuration

The S3 backend uses the AWS SDK for Go v2 and supports all standard AWS credential sources:

1. **Environment Variables**: `AWS_ACCESS_KEY_ID`, `AWS_SECRET_ACCESS_KEY`, `AWS_SESSION_TOKEN`
2. **Shared Credentials File**: `~/.aws/credentials`
3. **IAM Roles**: For EC2 instances, ECS tasks, Lambda functions
4. **SSO**: AWS IAM Identity Center (formerly AWS SSO)

### Required IAM Permissions

The IAM user/role needs the following S3 permissions:

```json
{
  "Version": "2012-10-17",
  "Statement": [
    {
      "Effect": "Allow",
      "Action": [
        "s3:GetObject",
        "s3:PutObject",
        "s3:DeleteObject",
        "s3:ListBucket",
        "s3:HeadBucket",
        "s3:HeadObject"
      ],
      "Resource": [
        "arn:aws:s3:::your-bucket-name",
        "arn:aws:s3:::your-bucket-name/*"
      ]
    }
  ]
}
```

## Examples

### Local Development with Disk Backend

```bash
# Terminal 1: Start the server
export DEBUG=true
export CACHE_DIR=/tmp/my-go-cache
./builds/gobuildcache

# Terminal 2: Use the cache
export GOCACHEPROG="$(pwd)/builds/gobuildcache"
cd /path/to/your/go/project
go build ./...
```

### Team Build Cache with S3

```bash
# All team members use the same configuration:
export BACKEND_TYPE=s3
export S3_BUCKET=team-build-cache
export S3_PREFIX=go/
export AWS_REGION=us-east-1
export GOCACHEPROG=/usr/local/bin/gobuildcache

# Now all builds share the same cache
go build ./...
```

### CI/CD Pipeline with S3

```yaml
# Example GitHub Actions workflow
env:
  BACKEND_TYPE: s3
  S3_BUCKET: ci-build-cache
  S3_PREFIX: ${{ github.repository }}/
  AWS_REGION: us-east-1
  GOCACHEPROG: ./gobuildcache

steps:
  - name: Download cache server
    run: |
      curl -L -o gobuildcache https://example.com/gobuildcache
      chmod +x gobuildcache
  
  - name: Configure AWS credentials
    uses: aws-actions/configure-aws-credentials@v4
    with:
      role-to-assume: arn:aws:iam::123456789012:role/GithubActionsRole
      aws-region: us-east-1
  
  - name: Build with remote cache
    run: go build ./...
```

## Troubleshooting

### Enable Debug Logging

```bash
export DEBUG=true
./builds/gobuildcache
```

### Check S3 Connectivity

```bash
export BACKEND_TYPE=s3
export S3_BUCKET=your-bucket
export DEBUG=true
./builds/gobuildcache clear  # This will test S3 access
```

### Verify Cache is Being Used

Look for cache hits in your Go build output:
```bash
go build -x ./...  # Shows detailed build steps including cache usage
```

## Performance Considerations

### Disk Backend
- **Pros**: Very fast, no network latency
- **Cons**: Not shared across machines, limited by disk space

### S3 Backend
- **Pros**: Shared across team/CI, scalable, durable
- **Cons**: Network latency for downloads, S3 API costs
- **Optimization**: Local temp cache reduces repeated S3 downloads

## License

MIT License (or your chosen license)

