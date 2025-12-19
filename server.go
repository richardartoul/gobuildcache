package main

import (
	"bufio"
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

	"gobuildcache/backends"

	"golang.org/x/sync/singleflight"
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
type CacheProg struct {
	backend backends.Backend
	reader  *bufio.Reader
	writer  struct {
		sync.Mutex
		w *bufio.Writer
	}

	debug  bool
	logger *slog.Logger

	// Singleflight group to deduplicate concurrent requests
	sfGroup singleflight.Group

	// Stats.
	seenActionIDs struct {
		sync.Mutex
		ids map[string]int // Maps action ID to request count
	}
	duplicateGets    atomic.Int64
	duplicatePuts    atomic.Int64
	putCount         atomic.Int64
	getCount         atomic.Int64
	hitCount         atomic.Int64
	deduplicatedGets atomic.Int64
	deduplicatedPuts atomic.Int64
	retriedRequests  atomic.Int64
	totalRetries     atomic.Int64
}

// NewCacheProg creates a new cache program instance.
func NewCacheProg(backend backends.Backend, debug bool) *CacheProg {
	// Configure logger level based on debug flag
	logLevel := slog.LevelInfo
	if debug {
		logLevel = slog.LevelDebug
	}

	// Create logger that writes to stderr
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
		Level: logLevel,
	}))

	cp := &CacheProg{
		backend: backend,
		reader:  bufio.NewReader(os.Stdin),
		debug:   debug,
		logger:  logger,
	}
	cp.writer.w = bufio.NewWriter(os.Stdout)
	cp.seenActionIDs.ids = make(map[string]int)
	return cp
}

// SendResponse sends a response to stdout (thread-safe).
func (cp *CacheProg) SendResponse(resp Response) error {
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

// SendInitialResponse sends the initial response with capabilities.
func (cp *CacheProg) SendInitialResponse() error {
	return cp.SendResponse(Response{
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

// ReadRequest reads a request from stdin.
func (cp *CacheProg) ReadRequest() (*Request, error) {
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
	if !cp.debug {
		return false
	}

	actionIDStr := hex.EncodeToString(actionID)

	cp.seenActionIDs.Lock()
	defer cp.seenActionIDs.Unlock()

	count := cp.seenActionIDs.ids[actionIDStr]
	cp.seenActionIDs.ids[actionIDStr] = count + 1

	return count > 0 // It's a duplicate if we've seen it before
}

// getResult holds the result of a Get operation for singleflight
type getResult struct {
	outputID []byte
	diskPath string
	size     int64
	putTime  *time.Time
	miss     bool
}

// putResult holds the result of a Put operation for singleflight
type putResult struct {
	diskPath string
}

// HandleRequest processes a single request and returns a response.
func (cp *CacheProg) HandleRequest(req *Request) (Response, error) {
	var resp Response
	resp.ID = req.ID

	switch req.Command {
	case CmdPut:
		cp.putCount.Add(1)
		isDuplicate := cp.trackActionID(req.ActionID)
		if isDuplicate {
			cp.duplicatePuts.Add(1)
			if cp.debug {
				fmt.Fprintf(os.Stderr, "[DEBUG] PUT duplicate action ID: %s\n", hex.EncodeToString(req.ActionID))
			}
		}

		key := "put:" + hex.EncodeToString(req.ActionID)
		v, err, shared := cp.sfGroup.Do(key, func() (interface{}, error) {
			diskPath, err := cp.backend.Put(req.ActionID, req.OutputID, req.Body, req.BodySize)
			return &putResult{diskPath: diskPath}, err
		})

		if shared {
			cp.deduplicatedPuts.Add(1)
			if cp.debug {
				fmt.Fprintf(os.Stderr, "[DEBUG] PUT deduplicated (shared result): %s\n", hex.EncodeToString(req.ActionID))
			}
		}

		if err != nil {
			resp.Err = err.Error()
			resp.Miss = true
			return resp, err
		}

		result := v.(*putResult)
		resp.DiskPath = result.diskPath
		return resp, nil

	case CmdGet:
		cp.getCount.Add(1)
		isDuplicate := cp.trackActionID(req.ActionID)
		if isDuplicate {
			cp.duplicateGets.Add(1)
			if cp.debug {
				fmt.Fprintf(os.Stderr, "[DEBUG] GET duplicate action ID: %s\n", hex.EncodeToString(req.ActionID))
			}
		}

		key := "get:" + hex.EncodeToString(req.ActionID)
		v, err, shared := cp.sfGroup.Do(key, func() (interface{}, error) {
			outputID, diskPath, size, putTime, miss, err := cp.backend.Get(req.ActionID)
			return &getResult{
				outputID: outputID,
				diskPath: diskPath,
				size:     size,
				putTime:  putTime,
				miss:     miss,
			}, err
		})

		if shared {
			cp.deduplicatedGets.Add(1)
			if cp.debug {
				fmt.Fprintf(os.Stderr, "[DEBUG] GET deduplicated (shared result): %s\n", hex.EncodeToString(req.ActionID))
			}
		}

		if err != nil {
			resp.Err = err.Error()
			resp.Miss = true
			return resp, err
		}

		result := v.(*getResult)
		resp.Miss = result.miss
		if !result.miss {
			cp.hitCount.Add(1)
			resp.OutputID = result.outputID
			resp.DiskPath = result.diskPath
			resp.Size = result.size
			resp.Time = result.putTime
		}
		return resp, nil

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

// HandleRequestWithRetries wraps HandleRequest with retry logic.
// It will retry failed requests up to maxRetries times with exponential backoff.
// maxRetries of 0 means no retries (same as calling HandleRequest directly).
// Returns the final response and error after all retries are exhausted.
func (cp *CacheProg) HandleRequestWithRetries(req *Request, maxRetries int) (Response, error) {

	var (
		resp    Response
		err     error
		attempt int
		// Calculate base delay for exponential backoff (starting at 10ms)
		baseDelay = 10 * time.Millisecond
	)
	for attempt = 0; attempt <= maxRetries; attempt++ {
		// Call the actual handler
		resp, err = cp.HandleRequest(req)

		// If successful or if it's a Close command, return immediately
		if err == nil || req.Command == CmdClose {
			if attempt > 0 {
				cp.logger.Debug("request succeeded after retries",
					"command", req.Command,
					"actionID", hex.EncodeToString(req.ActionID),
					"attempt", attempt+1)
			}
			return resp, err
		}

		// If we've exhausted retries, return the error
		if attempt >= maxRetries {
			if maxRetries > 0 {
				cp.logger.Warn("request failed after all retries",
					"command", req.Command,
					"actionID", hex.EncodeToString(req.ActionID),
					"attempts", attempt+1,
					"error", err)
			}
			return resp, err
		}

		// Track retry statistics
		if attempt == 0 {
			cp.retriedRequests.Add(1)
		}
		cp.totalRetries.Add(1)

		// Calculate exponential backoff delay: baseDelay * 2^attempt
		delay := baseDelay * time.Duration(1<<uint(attempt))

		cp.logger.Debug("retrying request after error",
			"command", req.Command,
			"actionID", hex.EncodeToString(req.ActionID),
			"attempt", attempt+1,
			"maxRetries", maxRetries,
			"delay", delay,
			"error", err)

		// Wait before retrying
		time.Sleep(delay)
	}

	return resp, err
}

// Run starts the cache program and processes requests concurrently.
func (cp *CacheProg) Run() error {
	// Send initial response with capabilities
	if err := cp.SendInitialResponse(); err != nil {
		return fmt.Errorf("failed to send initial response: %w", err)
	}

	var wg sync.WaitGroup
	errChan := make(chan error, 1)
	done := make(chan struct{})

	// Process requests concurrently
	for {
		req, err := cp.ReadRequest()
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
			resp, err := cp.HandleRequest(req)
			if err != nil {
				requestLogger.Error("failed to handle close request in backend", "error", err)
			} else {
				requestLogger.Debug("close command handled in backend")
			}
			if err != nil {
				resp.Err = err.Error()
			}
			if err := cp.SendResponse(resp); err != nil {
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
			resp, err := cp.HandleRequest(r)
			if err != nil {
				requestLogger.Error("failed to handle request in backend", "command", req.Command, "error", err)
				resp.Err = err.Error()
			} else {
				requestLogger.Debug("command handled in backend")
			}
			if err := cp.SendResponse(resp); err != nil {
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

	// Print statistics in debug mode
	if cp.debug {
		getCount := cp.getCount.Load()
		hitCount := cp.hitCount.Load()
		putCount := cp.putCount.Load()
		duplicateGets := cp.duplicateGets.Load()
		duplicatePuts := cp.duplicatePuts.Load()
		deduplicatedGets := cp.deduplicatedGets.Load()
		deduplicatedPuts := cp.deduplicatedPuts.Load()
		retriedRequests := cp.retriedRequests.Load()
		totalRetries := cp.totalRetries.Load()
		missCount := getCount - hitCount
		hitRate := 0.0
		if getCount > 0 {
			hitRate = float64(hitCount) / float64(getCount) * 100
		}

		cp.seenActionIDs.Lock()
		uniqueActionIDs := len(cp.seenActionIDs.ids)
		cp.seenActionIDs.Unlock()

		totalOps := getCount + putCount

		fmt.Fprintf(os.Stderr, "[DEBUG] Cache statistics:\n")
		fmt.Fprintf(os.Stderr, "[DEBUG]   GET operations: %d (hits: %d, misses: %d, hit rate: %.1f%%)\n",
			getCount, hitCount, missCount, hitRate)
		fmt.Fprintf(os.Stderr, "[DEBUG]     Duplicate GETs: %d (%.1f%% of GETs)\n",
			duplicateGets, float64(duplicateGets)/float64(getCount)*100)
		fmt.Fprintf(os.Stderr, "[DEBUG]     Deduplicated GETs (singleflight): %d (%.1f%% of GETs)\n",
			deduplicatedGets, float64(deduplicatedGets)/float64(getCount)*100)
		fmt.Fprintf(os.Stderr, "[DEBUG]   PUT operations: %d\n", putCount)
		fmt.Fprintf(os.Stderr, "[DEBUG]     Duplicate PUTs: %d (%.1f%% of PUTs)\n",
			duplicatePuts, float64(duplicatePuts)/float64(putCount)*100)
		fmt.Fprintf(os.Stderr, "[DEBUG]     Deduplicated PUTs (singleflight): %d (%.1f%% of PUTs)\n",
			deduplicatedPuts, float64(deduplicatedPuts)/float64(putCount)*100)
		fmt.Fprintf(os.Stderr, "[DEBUG]   Total operations: %d\n", totalOps)
		fmt.Fprintf(os.Stderr, "[DEBUG]   Unique action IDs: %d\n", uniqueActionIDs)
		if retriedRequests > 0 {
			avgRetries := float64(totalRetries) / float64(retriedRequests)
			fmt.Fprintf(os.Stderr, "[DEBUG]   Retried requests: %d (%.1f%% of operations)\n",
				retriedRequests, float64(retriedRequests)/float64(totalOps)*100)
			fmt.Fprintf(os.Stderr, "[DEBUG]   Total retries: %d (avg %.1f retries per failed request)\n",
				totalRetries, avgRetries)
		}
	}

	return nil
}
