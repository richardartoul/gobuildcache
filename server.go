package main

import (
	"bufio"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"
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
	backend    CacheBackend
	reader     *bufio.Reader
	writer     *bufio.Writer
	writerLock sync.Mutex
	debug      bool
	putCount   atomic.Int64
	getCount   atomic.Int64
	hitCount   atomic.Int64
}

// NewCacheProg creates a new cache program instance.
func NewCacheProg(backend CacheBackend, debug bool) *CacheProg {
	return &CacheProg{
		backend: backend,
		reader:  bufio.NewReader(os.Stdin),
		writer:  bufio.NewWriter(os.Stdout),
		debug:   debug,
	}
}

// SendResponse sends a response to stdout (thread-safe).
func (cp *CacheProg) SendResponse(resp Response) error {
	data, err := json.Marshal(resp)
	if err != nil {
		return fmt.Errorf("failed to marshal response: %w", err)
	}

	cp.writerLock.Lock()
	defer cp.writerLock.Unlock()

	if _, err := cp.writer.Write(data); err != nil {
		return fmt.Errorf("failed to write response: %w", err)
	}

	if err := cp.writer.WriteByte('\n'); err != nil {
		return fmt.Errorf("failed to write newline: %w", err)
	}

	return cp.writer.Flush()
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

// HandleRequest processes a single request and sends a response.
func (cp *CacheProg) HandleRequest(req *Request) error {
	var resp Response
	resp.ID = req.ID

	switch req.Command {
	case CmdPut:
		cp.putCount.Add(1)
		diskPath, err := cp.backend.Put(req.ActionID, req.OutputID, req.Body, req.BodySize)
		if err != nil {
			resp.Err = err.Error()
		} else {
			resp.DiskPath = diskPath
		}

	case CmdGet:
		cp.getCount.Add(1)
		outputID, diskPath, size, putTime, miss, err := cp.backend.Get(req.ActionID)
		if err != nil {
			resp.Err = err.Error()
		} else {
			resp.Miss = miss
			if !miss {
				cp.hitCount.Add(1)
				resp.OutputID = outputID
				resp.DiskPath = diskPath
				resp.Size = size
				resp.Time = putTime
			}
		}

	case CmdClose:
		if err := cp.backend.Close(); err != nil {
			resp.Err = err.Error()
		}
		// Will exit after sending response

	default:
		resp.Err = fmt.Sprintf("unknown command: %s", req.Command)
	}

	return cp.SendResponse(resp)
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

		// Check if this is a close command
		if req.Command == CmdClose {
			// Wait for all pending requests to complete before handling close
			wg.Wait()
			if err := cp.HandleRequest(req); err != nil {
				return fmt.Errorf("failed to handle close request: %w", err)
			}
			break
		}

		// Process request concurrently
		wg.Add(1)
		go func(r *Request) {
			defer wg.Done()
			if err := cp.HandleRequest(r); err != nil {
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
			return fmt.Errorf("failed to handle request: %w", err)
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
		return fmt.Errorf("failed to handle request: %w", err)
	}

	// Print statistics in debug mode
	if cp.debug {
		getCount := cp.getCount.Load()
		hitCount := cp.hitCount.Load()
		putCount := cp.putCount.Load()
		missCount := getCount - hitCount
		hitRate := 0.0
		if getCount > 0 {
			hitRate = float64(hitCount) / float64(getCount) * 100
		}
		fmt.Fprintf(os.Stderr, "[DEBUG] Cache statistics:\n")
		fmt.Fprintf(os.Stderr, "[DEBUG]   GET operations: %d (hits: %d, misses: %d, hit rate: %.1f%%)\n",
			getCount, hitCount, missCount, hitRate)
		fmt.Fprintf(os.Stderr, "[DEBUG]   PUT operations: %d\n", putCount)
		fmt.Fprintf(os.Stderr, "[DEBUG]   Total operations: %d\n", getCount+putCount)
	}

	return nil
}
