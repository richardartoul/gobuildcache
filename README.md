# Go Build Cache Server

A remote caching server for Go builds that supports multiple storage backends.

## Features

- **Multiple Storage Backends**: Choose between local disk storage or S3 cloud storage
- **Go Build Cache Protocol**: Compatible with Go's remote cache protocol (`GOCACHEPROG`)
- **Flexible Configuration**: Use command-line flags or environment variables (or both!)
- **Debug Mode**: Optional debug logging for troubleshooting

## Configuration

All configuration options can be set via **command-line flags** or **environment variables**. Command-line flags take precedence over environment variables.

### Available Options

| Flag | Environment Variable | Default | Description |
|------|---------------------|---------|-------------|
| `-backend` | `BACKEND_TYPE` | `disk` | Backend type: `disk` or `s3` |
| `-cache-dir` | `CACHE_DIR` | `/tmp/gobuildcache` | Cache directory for disk backend |
| `-s3-bucket` | `S3_BUCKET` | (none) | S3 bucket name (required for S3) |
| `-s3-prefix` | `S3_PREFIX` | (empty) | S3 key prefix |
| `-s3-tmp-dir` | `S3_TMP_DIR` | `/tmp/gobuildcache-s3` | Local temp directory for S3 |
| `-debug` | `DEBUG` | `false` | Enable debug logging |

## Storage Backends

### Disk Backend (Default)

Stores cache files on the local filesystem.

**Using Flags:**
```bash
./builds/gobuildcache -cache-dir=/path/to/cache
```

**Using Environment Variables:**
```bash
export CACHE_DIR=/path/to/cache
./builds/gobuildcache
```

**Mixed (flags override env vars):**
```bash
export CACHE_DIR=/default/path
./builds/gobuildcache -cache-dir=/override/path -debug
```

### S3 Backend

Stores cache files in Amazon S3 (or S3-compatible storage).

**Using Flags:**
```bash
./builds/gobuildcache -backend=s3 -s3-bucket=my-bucket
```

**Using Environment Variables:**
```bash
export BACKEND_TYPE=s3
export S3_BUCKET=my-bucket
export S3_PREFIX=cache/
./builds/gobuildcache
```

**Mixed (flags override env vars):**
```bash
export BACKEND_TYPE=s3
export S3_BUCKET=default-bucket
./builds/gobuildcache -s3-bucket=override-bucket -debug
```

**AWS Credentials:**
AWS credentials are always configured via standard AWS environment variables or `~/.aws/credentials`:
```bash
export AWS_REGION=us-east-1
export AWS_ACCESS_KEY_ID=your_access_key
export AWS_SECRET_ACCESS_KEY=your_secret_key
# Or use AWS profiles:
export AWS_PROFILE=your-profile
```

**How S3 Backend Works:**
1. Cache objects are stored in S3 with metadata (outputID, size, timestamp)
2. On cache hits, files are downloaded to a local temp directory for Go to access
3. The local temp directory acts as a secondary cache to avoid repeated S3 downloads
4. The `diskPath` returned to Go points to the locally cached file

## Usage

### Getting Help

View available commands and flags:
```bash
./builds/gobuildcache help
./builds/gobuildcache -h
./builds/gobuildcache clear -h
```

### Running the Server

Start the cache server:
```bash
# With disk backend (default)
./builds/gobuildcache

# With custom cache directory
./builds/gobuildcache -cache-dir=/var/cache/go

# With S3 backend
./builds/gobuildcache -backend=s3 -s3-bucket=my-cache-bucket

# With debug logging
./builds/gobuildcache -debug
```

The server will:
1. Read from stdin (Go sends cache requests)
2. Write responses to stdout
3. Log debug information to stderr (if `-debug` flag is set)

### Configuring Go to Use the Cache

Set the `GOCACHEPROG` environment variable to point to the cache server:

```bash
export GOCACHEPROG=/path/to/gobuildcache/builds/gobuildcache
go build ./...
```

### Clearing the Cache

Clear all cache entries:
```bash
# Clear disk cache
./builds/gobuildcache clear -cache-dir=/var/cache/go

# Clear S3 cache
./builds/gobuildcache clear -backend=s3 -s3-bucket=my-cache-bucket

# Clear with debug logging
./builds/gobuildcache clear -debug
```

The clear command uses the same backend flags as the server command.

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
# Using flags
./builds/gobuildcache -debug -cache-dir=/tmp/my-go-cache

# Or using environment variables
export DEBUG=true
export CACHE_DIR=/tmp/my-go-cache
./builds/gobuildcache
```

### Team Build Cache with S3

```bash
# Option 1: Using environment variables (easier for team consistency)
export BACKEND_TYPE=s3
export S3_BUCKET=team-build-cache
export S3_PREFIX=go/
export AWS_REGION=us-east-1
export GOCACHEPROG=/usr/local/bin/gobuildcache
go build ./...

# Option 2: Using flags
gobuildcache -backend=s3 -s3-bucket=team-build-cache -s3-prefix=go/
```

### CI/CD Pipeline with S3

```yaml
# Example GitHub Actions workflow
env:
  # Use environment variables for cleaner configuration
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

**Alternative using flags:**
```yaml
steps:
  # ... (download and AWS setup same as above)
  
  - name: Start cache server
    run: |
      ./gobuildcache -backend=s3 \
        -s3-bucket=ci-build-cache \
        -s3-prefix=${{ github.repository }}/ &
  
  - name: Build with remote cache
    run: go build ./...
```

## Troubleshooting

### Enable Debug Logging

Using flags:
```bash
./builds/gobuildcache -debug
```

Using environment variable:
```bash
export DEBUG=true
./builds/gobuildcache
```

### Check S3 Connectivity

Using flags:
```bash
./builds/gobuildcache clear -backend=s3 -s3-bucket=your-bucket -debug
```

Using environment variables:
```bash
export BACKEND_TYPE=s3
export S3_BUCKET=your-bucket
export DEBUG=true
./builds/gobuildcache clear
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

