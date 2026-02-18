package main

import (
	"bufio"
	"bytes"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/richardartoul/gobuildcache/pkg/backends"
	"github.com/richardartoul/gobuildcache/pkg/locking"
	"github.com/richardartoul/gobuildcache/pkg/metrics"

	"github.com/pierrec/lz4/v4"
)

const (
	// Bump this string whenever you make backwards-incompatible changes to the file format.
	fileFormatVersion = "v2"
)

// Cmd represents a cache command type.
type Cmd string

const (
	CmdPut   = Cmd("put")
	CmdGet   = Cmd("get")
	CmdClose = Cmd("close")
)

// Request represents a request from the go command.
type Request struct {
	ID       int64
	Command  Cmd
	ActionID []byte `json:",omitempty"`
	OutputID []byte `json:",omitempty"`
	Body     io.Reader
	BodySize int64 `json:",omitempty"`
}

// Response represents a response to the go command.
type Response struct {
	ID            int64      `json:",omitempty"`
	Err           string     `json:",omitempty"`
	KnownCommands []Cmd      `json:",omitempty"`
	Miss          bool       `json:",omitempty"`
	OutputID      []byte     `json:",omitempty"`
	Size          int64      `json:",omitempty"`
	Time          *time.Time `json:",omitempty"`
	DiskPath      string     `json:",omitempty"`
}

// CacheProg implements the GOCACHEPROG protocol.
// It manages a local disk cache that Go build tools access directly,
// and uses a backend for distributed/persistent storage.
type CacheProg struct {
	backend    backends.Backend
	localCache *localCache
	reader     *bufio.Reader
	writer     struct {
		sync.Mutex
		w *bufio.Writer
	}

	debug       bool
	printStats  bool
	compression bool
	readOnly    bool
	logger      *slog.Logger

	// Latency tracking using DDSketch for quantile estimation.
	latencyTracker *metrics.LatencyTracker

	// both GET and PUT requests are modifying the filesystem to cache
	// files (GET loading from the backend and PUT writing directly), so
	// we need to ensure exclusive access to avoid racing and/or corrupting
	// the filesystem.
	//
	// We have multiple implementations of locker, one of which uses the
	// filesystem itself to do the locking so it works even if there are
	// multiple instances of the cache program running concurrently using
	// the same cache directory.
	locker locking.Group

	// Stats.
	seenActionIDs struct {
		sync.Mutex
		ids map[string]int // Maps action ID to request count
	}
	duplicateGets         atomic.Int64
	duplicatePuts         atomic.Int64
	putCount              atomic.Int64
	skippedPuts           atomic.Int64
	getCount              atomic.Int64
	hitCount              atomic.Int64
	localCacheHits        atomic.Int64
	backendCacheHits      atomic.Int64
	deduplicatedGets      atomic.Int64
	deduplicatedPuts      atomic.Int64
	retriedRequests       atomic.Int64
	totalRetries          atomic.Int64
	backendBytesRead      atomic.Int64 // Total bytes read from backend
	backendBytesWritten   atomic.Int64 // Total bytes written to backend
	compressionBytesIn    atomic.Int64 // Uncompressed bytes before compression
	compressionBytesOut   atomic.Int64 // Compressed bytes after compression
	decompressionBytesIn  atomic.Int64 // Compressed bytes before decompression
	decompressionBytesOut atomic.Int64 // Uncompressed bytes after decompression
}

// NewCacheProg creates a new cache program instance.
// cacheDir is the local directory where cached files are stored for Go build tools to access.
func NewCacheProg(
	backend backends.Backend,
	sfGroup locking.Group,
	cacheDir string,
	debug bool,
	printStats bool,
	compression bool,
	readOnly bool,
) (*CacheProg, error) {
	logLevel := slog.LevelInfo
	if debug {
		logLevel = slog.LevelDebug
	}

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
		Level: logLevel,
	}))

	localCache, err := newLocalCache(cacheDir, logger)
	if err != nil {
		return nil, err
	}

	cp := &CacheProg{
		backend:        backend,
		localCache:     localCache,
		reader:         bufio.NewReader(os.Stdin),
		debug:          debug,
		printStats:     printStats,
		compression:    compression,
		readOnly:       readOnly,
		logger:         logger,
		locker:         sfGroup,
		latencyTracker: metrics.NewLatencyTracker(0.01), // 1% relative accuracy
	}
	cp.writer.w = bufio.NewWriter(os.Stdout)
	cp.seenActionIDs.ids = make(map[string]int)
	return cp, nil
}

// Run starts the cache program and processes requests concurrently.
func (cp *CacheProg) Run() error {
	// Send initial response with capabilities
	if err := cp.sendInitialResponse(); err != nil {
		return fmt.Errorf("failed to send initial response: %w", err)
	}

	var wg sync.WaitGroup
	errChan := make(chan error, 1)
	done := make(chan struct{})

	// Process requests concurrently
	for {
		req, err := cp.readRequest()
		if err == io.EOF {
			break
		}
		if err != nil {
			// Wait for any in-flight requests to complete
			wg.Wait()
			return fmt.Errorf("failed to read request: %w", err)
		}

		requestLogger := cp.logger.With("command", req.Command, "actionID", hex.EncodeToString(req.ActionID))

		// Check if this is a close command
		if req.Command == CmdClose {
			requestLogger.Debug("close command received, waiting for pending requests to complete")
			// Wait for all pending requests to complete before handling close
			wg.Wait()
			requestLogger.Debug("pending requests completed, handling close command in backend")
			resp, err := cp.handleRequest(req)
			if err != nil {
				requestLogger.Error("failed to handle close request in backend", "error", err)
				// Complation / testing will fail if cleanup fails, but we've already done all the
				// work and logged it, so just tell the compiler everything is fine so it can exit
				// cleanly.
				resp.Err = ""
			} else {
				requestLogger.Debug("close command handled in backend")
			}

			if err := cp.sendResponse(resp); err != nil {
				requestLogger.Error("failed to send close response, exiting...", "error", err)
				return fmt.Errorf("failed to send close response: %w", err)
			}
			requestLogger.Debug("close command received, exited successfully")
			break
		}

		// Process request concurrently
		wg.Add(1)
		go func(r *Request) {
			defer wg.Done()
			start := time.Now()
			resp, err := cp.handleRequest(r)
			if err != nil {
				requestLogger.Error("failed to handle request in backend", "command", req.Command, "error", err)
				resp.Err = err.Error()
			} else {
				requestLogger.Debug("command handled in backend", "duration", time.Since(start))
			}
			if err := cp.sendResponse(resp); err != nil {
				select {
				case errChan <- err:
				default:
				}
			}
		}(req)

		// Check for errors from goroutines
		select {
		case err := <-errChan:
			wg.Wait()
			return fmt.Errorf("failed to send response: %w", err)
		default:
		}
	}

	// Wait for all goroutines to complete
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
	case err := <-errChan:
		wg.Wait()
		return fmt.Errorf("failed to send response: %w", err)
	}

	// Print statistics if enabled
	if cp.printStats {
		var (
			getCount              = cp.getCount.Load()
			hitCount              = cp.hitCount.Load()
			localCacheHits        = cp.localCacheHits.Load()
			backendCacheHits      = cp.backendCacheHits.Load()
			putCount              = cp.putCount.Load()
			skippedPuts           = cp.skippedPuts.Load()
			duplicateGets         = cp.duplicateGets.Load()
			duplicatePuts         = cp.duplicatePuts.Load()
			deduplicatedGets      = cp.deduplicatedGets.Load()
			deduplicatedPuts      = cp.deduplicatedPuts.Load()
			retriedRequests       = cp.retriedRequests.Load()
			totalRetries          = cp.totalRetries.Load()
			backendBytesRead      = cp.backendBytesRead.Load()
			backendBytesWritten   = cp.backendBytesWritten.Load()
			compressionBytesIn    = cp.compressionBytesIn.Load()
			compressionBytesOut   = cp.compressionBytesOut.Load()
			decompressionBytesIn  = cp.decompressionBytesIn.Load()
			decompressionBytesOut = cp.decompressionBytesOut.Load()
			missCount             = getCount - hitCount
			hitRate               = 0.0
			localHitRate          = 0.0
			backendHitRate        = 0.0
		)
		if getCount > 0 {
			hitRate = float64(hitCount) / float64(getCount) * 100
			localHitRate = float64(localCacheHits) / float64(getCount) * 100
			backendHitRate = float64(backendCacheHits) / float64(getCount) * 100
		}

		cp.seenActionIDs.Lock()
		uniqueActionIDs := len(cp.seenActionIDs.ids)
		cp.seenActionIDs.Unlock()

		totalOps := getCount + putCount

		fmt.Fprintf(os.Stderr, "Cache statistics:\n")
		fmt.Fprintf(os.Stderr, "  GET operations: %d (hits: %d, misses: %d, hit rate: %.1f%%)\n",
			getCount, hitCount, missCount, hitRate)
		fmt.Fprintf(os.Stderr, "    Local cache hits: %d (%.1f%% of GETs)\n",
			localCacheHits, localHitRate)
		fmt.Fprintf(os.Stderr, "    Backend cache hits: %d (%.1f%% of GETs)\n",
			backendCacheHits, backendHitRate)
		fmt.Fprintf(os.Stderr, "    Duplicate GETs: %d (%.1f%% of GETs)\n",
			duplicateGets, float64(duplicateGets)/float64(getCount)*100)
		fmt.Fprintf(os.Stderr, "    Deduplicated GETs (singleflight): %d (%.1f%% of GETs)\n",
			deduplicatedGets, float64(deduplicatedGets)/float64(getCount)*100)
		fmt.Fprintf(os.Stderr, "    Backend bytes read: %s\n", formatBytes(backendBytesRead))
		fmt.Fprintf(os.Stderr, "  PUT operations: %d\n", putCount)
		if skippedPuts > 0 {
			fmt.Fprintf(os.Stderr, "    Skipped PUTs (read-only mode): %d\n", skippedPuts)
		}
		fmt.Fprintf(os.Stderr, "    Duplicate PUTs: %d (%.1f%% of PUTs)\n",
			duplicatePuts, float64(duplicatePuts)/float64(putCount)*100)
		fmt.Fprintf(os.Stderr, "    Deduplicated PUTs (singleflight): %d (%.1f%% of PUTs)\n",
			deduplicatedPuts, float64(deduplicatedPuts)/float64(putCount)*100)
		fmt.Fprintf(os.Stderr, "    Backend bytes written: %s\n", formatBytes(backendBytesWritten))
		fmt.Fprintf(os.Stderr, "  Total operations: %d\n", totalOps)
		fmt.Fprintf(os.Stderr, "  Unique action IDs: %d\n", uniqueActionIDs)
		fmt.Fprintf(os.Stderr, "  Total backend bytes transferred: %s\n", formatBytes(backendBytesRead+backendBytesWritten))

		// Print compression statistics if compression is enabled
		if cp.compression {
			fmt.Fprintf(os.Stderr, "\nCompression statistics:\n")
			if compressionBytesIn > 0 {
				compressionRatio := float64(compressionBytesOut) / float64(compressionBytesIn) * 100
				spaceSaved := compressionBytesIn - compressionBytesOut
				fmt.Fprintf(os.Stderr, "  Compression (PUT): %s -> %s (%.1f%%, saved %s)\n",
					formatBytes(compressionBytesIn), formatBytes(compressionBytesOut),
					compressionRatio, formatBytes(spaceSaved))
			}
			if decompressionBytesIn > 0 {
				decompressionRatio := float64(decompressionBytesOut) / float64(decompressionBytesIn) * 100
				fmt.Fprintf(os.Stderr, "  Decompression (GET): %s -> %s (%.1f%% expansion)\n",
					formatBytes(decompressionBytesIn), formatBytes(decompressionBytesOut),
					decompressionRatio)
			}
			if compressionBytesIn == 0 && decompressionBytesIn == 0 {
				fmt.Fprintf(os.Stderr, "  No compression activity (compression enabled but no data compressed/decompressed)\n")
			}
		}
		if retriedRequests > 0 {
			avgRetries := float64(totalRetries) / float64(retriedRequests)
			fmt.Fprintf(os.Stderr, "  Retried requests: %d (%.1f%% of operations)\n",
				retriedRequests, float64(retriedRequests)/float64(totalOps)*100)
			fmt.Fprintf(os.Stderr, "  Total retries: %d (avg %.1f retries per failed request)\n",
				totalRetries, avgRetries)
		}

		// Print latency quantiles
		fmt.Fprintf(os.Stderr, "\nLatency quantiles (ms):\n")
		allStats := cp.latencyTracker.GetAllStats()
		if len(allStats) == 0 {
			fmt.Fprintf(os.Stderr, "  No latency data collected\n")
		} else {
			for _, stat := range allStats {
				fmt.Fprintf(os.Stderr, "%s\n", stat.String())
			}
		}
	}

	return nil
}

// handleRequest processes a single request and returns a response.
func (cp *CacheProg) handleRequest(req *Request) (Response, error) {
	var resp Response
	resp.ID = req.ID

	switch req.Command {
	case CmdPut:
		return cp.handlePut(req)

	case CmdGet:
		return cp.handleGet(req)

	case CmdClose:
		if err := cp.backend.Close(); err != nil {
			resp.Err = err.Error()
			return resp, err
		}
		return resp, nil

	default:
		resp.Err = fmt.Sprintf("unknown command: %s", req.Command)
		return resp, fmt.Errorf("unknown command: %s", req.Command)
	}
}

// putResult holds the result of a Put operation for singleflight
type putResult struct {
	diskPath string
}

// handlePut processes a PUT request.
func (cp *CacheProg) handlePut(req *Request) (Response, error) {
	overallStart := time.Now()
	defer func() {
		cp.latencyTracker.Record("put_overall", time.Since(overallStart))
	}()

	var resp Response
	resp.ID = req.ID

	cp.putCount.Add(1)
	isDuplicate := cp.trackActionID(req.ActionID)
	if isDuplicate {
		cp.duplicatePuts.Add(1)
		if cp.debug {
			fmt.Fprintf(os.Stderr, "[DEBUG] PUT duplicate action ID: %s\n", hex.EncodeToString(req.ActionID))
		}
	}

	key := hex.EncodeToString(req.ActionID)
	v, err := cp.locker.DoWithLock(key, func() (interface{}, error) {
		// Someone may have cached the result already, so check the local cache first
		// before doing anything expensive.
		localCacheCheckStart := time.Now()
		existingMeta := cp.localCache.check(req.ActionID)
		cp.latencyTracker.Record("put_local_cache_check", time.Since(localCacheCheckStart))

		if existingMeta != nil {
			return &putResult{diskPath: cp.localCache.getPath(req.ActionID)}, nil
		}

		// Read body into memory (we need to write it to both local cache and backend)
		var bodyData []byte
		if req.BodySize > 0 && req.Body != nil {
			bodyData = make([]byte, req.BodySize)
			n, err := io.ReadFull(req.Body, bodyData)
			if err != nil && err != io.EOF {
				return nil, fmt.Errorf("failed to read body: %w", err)
			}
			if int64(n) != req.BodySize {
				return nil, fmt.Errorf("size mismatch: expected %d, read %d", req.BodySize, n)
			}
		}

		// Write to local cache with metadata
		meta := localCacheMetadata{
			OutputID: req.OutputID,
			Size:     req.BodySize,
			PutTime:  time.Now(),
		}

		localCacheWriteStart := time.Now()
		diskPath, err := cp.localCache.writeWithMetadata(req.ActionID, bytes.NewReader(bodyData), meta)
		cp.latencyTracker.Record("put_local_cache_write", time.Since(localCacheWriteStart))

		if err != nil {
			return nil, fmt.Errorf("failed to write to local cache: %w", err)
		}

		// In read-only mode, skip backend write but keep local cache
		if cp.readOnly {
			cp.skippedPuts.Add(1)
			if cp.debug {
				fmt.Fprintf(os.Stderr, "[DEBUG] PUT backend write skipped (read-only mode): %s\n", hex.EncodeToString(req.ActionID))
			}
			return &putResult{diskPath: diskPath}, nil
		}

		var (
			backendPutStart = time.Now()
			dataToStore     []byte
			dataSize        int64
		)
		if cp.compression && req.BodySize > 0 {
			compressStart := time.Now()
			compressed, err := compressData(bodyData)
			cp.latencyTracker.Record("put_compression", time.Since(compressStart))

			if err != nil {
				return nil, fmt.Errorf("failed to compress data: %w", err)
			}

			dataToStore = compressed
			dataSize = int64(len(compressed))

			cp.compressionBytesIn.Add(req.BodySize)
			cp.compressionBytesOut.Add(dataSize)
		} else {
			dataToStore = bodyData
			dataSize = req.BodySize
		}

		backendKey := cp.generateBackendKey(req.ActionID)
		err = cp.backend.Put(backendKey, req.OutputID, bytes.NewReader(dataToStore), dataSize)
		cp.latencyTracker.Record("put_backend", time.Since(backendPutStart))

		if err != nil {
			// Local cache is still valid even if backend fails
			cp.logger.Warn("backend PUT failed, but local cache succeeded",
				"actionID", hex.EncodeToString(req.ActionID),
				"error", err)
		} else {
			// Track bytes written to backend on success (actual bytes transferred)
			cp.backendBytesWritten.Add(dataSize)
		}

		return &putResult{diskPath: diskPath}, nil
	})

	if err != nil {
		resp.Err = err.Error()
		return resp, err
	}

	result := v.(*putResult)
	resp.DiskPath = result.diskPath
	return resp, nil
}

// getResult holds the result of a Get operation for singleflight
type getResult struct {
	outputID       []byte
	diskPath       string
	size           int64
	putTime        *time.Time
	miss           bool
	fromLocalCache bool // true if hit was from local cache, false if from backend
}

// handleGet processes a GET request.
func (cp *CacheProg) handleGet(req *Request) (Response, error) {
	overallStart := time.Now()
	defer func() {
		cp.latencyTracker.Record("get_overall", time.Since(overallStart))
	}()

	var resp Response
	resp.ID = req.ID

	cp.getCount.Add(1)
	isDuplicate := cp.trackActionID(req.ActionID)
	if isDuplicate {
		cp.duplicateGets.Add(1)
		if cp.debug {
			fmt.Fprintf(os.Stderr, "[DEBUG] GET duplicate action ID: %s\n", hex.EncodeToString(req.ActionID))
		}
	}

	key := hex.EncodeToString(req.ActionID)
	v, err := cp.locker.DoWithLock(key, func() (interface{}, error) {
		// Check local cache first
		localCacheCheckStart := time.Now()
		meta := cp.localCache.check(req.ActionID)
		cp.latencyTracker.Record("get_local_cache_check", time.Since(localCacheCheckStart))

		if meta != nil {
			// Local cache hit with metadata
			diskPath := cp.localCache.getPath(req.ActionID)

			return &getResult{
				outputID:       meta.OutputID,
				diskPath:       diskPath,
				size:           meta.Size,
				putTime:        &meta.PutTime,
				miss:           false,
				fromLocalCache: true,
			}, nil
		}

		// Local cache miss - get from backend
		backendGetStart := time.Now()
		backendKey := cp.generateBackendKey(req.ActionID)
		outputID, body, size, putTime, miss, err := cp.backend.Get(backendKey)
		cp.latencyTracker.Record("get_backend", time.Since(backendGetStart))

		if err != nil {
			return nil, err
		}

		if miss {
			// Backend miss
			return &getResult{
				miss: true,
			}, nil
		}

		// Backend hit - track bytes read from backend (compressed size)
		cp.backendBytesRead.Add(size)

		// Backend hit - decompress if needed, then write to local cache with metadata
		defer body.Close()

		var dataToCache io.Reader
		var actualSize int64

		if cp.compression && size > 0 {
			// Read compressed data from backend
			compressedData, err := io.ReadAll(body)
			if err != nil {
				return nil, fmt.Errorf("failed to read compressed data from backend: %w", err)
			}

			// Decompress data
			decompressStart := time.Now()
			decompressed, err := decompressData(compressedData)
			cp.latencyTracker.Record("get_decompression", time.Since(decompressStart))

			if err != nil {
				return nil, fmt.Errorf("failed to decompress data: %w", err)
			}

			// Track decompression statistics
			cp.decompressionBytesIn.Add(size)
			cp.decompressionBytesOut.Add(int64(len(decompressed)))

			dataToCache = bytes.NewReader(decompressed)
			actualSize = int64(len(decompressed))
		} else {
			dataToCache = body
			actualSize = size
		}

		metaForWrite := localCacheMetadata{
			OutputID: outputID,
			Size:     actualSize,
			PutTime:  *putTime,
		}

		localCacheWriteStart := time.Now()
		diskPath, err := cp.localCache.writeWithMetadata(req.ActionID, dataToCache, metaForWrite)
		cp.latencyTracker.Record("get_local_cache_write", time.Since(localCacheWriteStart))

		if err != nil {
			cp.logger.Warn("failed to write to local cache after backend hit",
				"actionID", hex.EncodeToString(req.ActionID),
				"error", err)
			// We got data from backend but couldn't cache it locally
			// This is not fatal - we can still serve from backend
			return nil, fmt.Errorf("failed to cache locally: %w", err)
		}

		return &getResult{
			outputID:       outputID,
			diskPath:       diskPath,
			size:           actualSize,
			putTime:        putTime,
			miss:           false,
			fromLocalCache: false,
		}, nil
	})

	if err != nil {
		resp.Err = err.Error()
		resp.Miss = true
		return resp, err
	}

	result := v.(*getResult)
	resp.Miss = result.miss
	if !result.miss {
		cp.hitCount.Add(1)
		if result.fromLocalCache {
			cp.localCacheHits.Add(1)
		} else {
			cp.backendCacheHits.Add(1)
		}
		resp.OutputID = result.outputID
		resp.DiskPath = result.diskPath
		resp.Size = result.size
		resp.Time = result.putTime
	}
	return resp, nil
}

// sendResponse sends a response to stdout (thread-safe).
func (cp *CacheProg) sendResponse(resp Response) error {
	data, err := json.Marshal(resp)
	if err != nil {
		return fmt.Errorf("failed to marshal response: %w", err)
	}

	cp.writer.Lock()
	defer cp.writer.Unlock()

	if _, err := cp.writer.w.Write(data); err != nil {
		return fmt.Errorf("failed to write response: %w", err)
	}

	if err := cp.writer.w.WriteByte('\n'); err != nil {
		return fmt.Errorf("failed to write newline: %w", err)
	}

	return cp.writer.w.Flush()
}

// sendInitialResponse sends the initial response with capabilities.
func (cp *CacheProg) sendInitialResponse() error {
	return cp.sendResponse(Response{
		ID:            0,
		KnownCommands: []Cmd{CmdPut, CmdGet, CmdClose},
	})
}

// readLine reads a line from stdin, skipping empty lines.
func (cp *CacheProg) readLine() ([]byte, error) {
	for {
		line, err := cp.reader.ReadBytes('\n')
		if err != nil {
			return nil, err
		}

		// Remove trailing newline
		line = line[:len(line)-1]

		// Skip empty lines
		if len(strings.TrimSpace(string(line))) > 0 {
			return line, nil
		}
	}
}

// readRequest reads a request from stdin.
func (cp *CacheProg) readRequest() (*Request, error) {
	// Read the request line
	line, err := cp.readLine()
	if err != nil {
		if err == io.EOF {
			return nil, io.EOF
		}
		return nil, fmt.Errorf("failed to read request: %w", err)
	}

	var req Request
	if err := json.Unmarshal(line, &req); err != nil {
		return nil, fmt.Errorf("failed to unmarshal request: %w (line: %q)", err, string(line))
	}

	// For "put" commands with BodySize > 0, read the base64 body on the next line
	if req.Command == CmdPut && req.BodySize > 0 {
		// Read the body line
		bodyLine, err := cp.readLine()
		if err != nil {
			if err == io.EOF {
				// EOF reached without finding body - connection closed
				return nil, io.EOF
			}
			return nil, fmt.Errorf("error reading body line: %w", err)
		}

		// The body is sent as a base64-encoded JSON string (a JSON string literal)
		var base64Str string
		if err := json.Unmarshal(bodyLine, &base64Str); err != nil {
			return nil, fmt.Errorf("failed to unmarshal body as JSON string: %w (line: %q)", err, string(bodyLine))
		}

		bodyData, err := base64.StdEncoding.DecodeString(base64Str)
		if err != nil {
			return nil, fmt.Errorf("failed to decode base64 body: %w", err)
		}

		req.Body = strings.NewReader(string(bodyData))
	}

	return &req, nil
}

// trackActionID records an action ID and returns whether it's a duplicate.
func (cp *CacheProg) trackActionID(actionID []byte) bool {
	actionIDStr := hex.EncodeToString(actionID)

	cp.seenActionIDs.Lock()
	defer cp.seenActionIDs.Unlock()

	count := cp.seenActionIDs.ids[actionIDStr]
	cp.seenActionIDs.ids[actionIDStr] = count + 1

	return count > 0 // It's a duplicate if we've seen it before
}

// generateBackendKey generates the key to use for backend storage operations.
// This allows for versioning, prefixing, or other key transformations.
func (cp *CacheProg) generateBackendKey(actionID []byte) []byte {
	return []byte(fileFormatVersion + hex.EncodeToString(actionID))
}

// formatBytes formats a byte count as a human-readable string.
func formatBytes(bytes int64) string {
	const (
		KB = 1024
		MB = KB * 1024
		GB = MB * 1024
		TB = GB * 1024
	)

	if bytes < KB {
		return fmt.Sprintf("%d B", bytes)
	} else if bytes < MB {
		return fmt.Sprintf("%.2f KB", float64(bytes)/KB)
	} else if bytes < GB {
		return fmt.Sprintf("%.2f MB", float64(bytes)/MB)
	} else if bytes < TB {
		return fmt.Sprintf("%.2f GB", float64(bytes)/GB)
	}
	return fmt.Sprintf("%.2f TB", float64(bytes)/TB)
}

// compressData compresses data using LZ4 and returns the compressed bytes.
func compressData(data []byte) ([]byte, error) {
	var buf bytes.Buffer
	writer := lz4.NewWriter(&buf)

	if _, err := writer.Write(data); err != nil {
		writer.Close()
		return nil, fmt.Errorf("failed to write to LZ4 compressor: %w", err)
	}

	if err := writer.Close(); err != nil {
		return nil, fmt.Errorf("failed to close LZ4 compressor: %w", err)
	}

	return buf.Bytes(), nil
}

// decompressData decompresses LZ4-compressed data.
func decompressData(data []byte) ([]byte, error) {
	reader := lz4.NewReader(bytes.NewReader(data))

	var buf bytes.Buffer
	if _, err := io.Copy(&buf, reader); err != nil {
		return nil, fmt.Errorf("failed to decompress LZ4 data: %w", err)
	}

	return buf.Bytes(), nil
}
