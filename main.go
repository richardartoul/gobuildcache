package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func main() {
	if len(os.Args) < 2 {
		// No subcommand, run the server
		runServer()
		return
	}

	subcommand := os.Args[1]

	switch subcommand {
	case "clear":
		clearCmd := flag.NewFlagSet("clear", flag.ExitOnError)
		clearCmd.Usage = func() {
			fmt.Fprintf(os.Stderr, "Usage: %s clear\n", os.Args[0])
			fmt.Fprintf(os.Stderr, "Clear all entries from the cache.\n")
		}
		clearCmd.Parse(os.Args[2:])
		runClear()

	default:
		fmt.Fprintf(os.Stderr, "Unknown subcommand: %s\n\n", subcommand)
		fmt.Fprintf(os.Stderr, "Usage: %s [command]\n\n", os.Args[0])
		fmt.Fprintf(os.Stderr, "Commands:\n")
		fmt.Fprintf(os.Stderr, "  (no command)  Run the cache server\n")
		fmt.Fprintf(os.Stderr, "  clear         Clear all entries from the cache\n")
		os.Exit(1)
	}
}

func runServer() {
	debug := getDebugMode()

	// Create backend (disk or S3)
	backend, err := createBackend(debug)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error creating cache backend: %v\n", err)
		os.Exit(1)
	}
	defer backend.Close()

	// Create and run cache program
	prog := NewCacheProg(backend, debug)
	if err := prog.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "Error running cache program: %v\n", err)
		os.Exit(1)
	}
}

func runClear() {
	debug := getDebugMode()

	// Create backend (disk or S3)
	backend, err := createBackend(debug)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error creating cache backend: %v\n", err)
		os.Exit(1)
	}
	defer backend.Close()

	// Clear the cache
	if err := backend.Clear(); err != nil {
		fmt.Fprintf(os.Stderr, "Error clearing cache: %v\n", err)
		os.Exit(1)
	}

	fmt.Fprintf(os.Stdout, "Cache cleared successfully\n")
}

func getCacheDir() string {
	cacheDir := os.Getenv("CACHE_DIR")
	if cacheDir == "" {
		cacheDir = filepath.Join(os.TempDir(), "gobuildcache")
	}
	return cacheDir
}

func getDebugMode() bool {
	debugEnv := strings.ToLower(os.Getenv("DEBUG"))
	return debugEnv == "true"
}

func createBackend(debug bool) (CacheBackend, error) {
	backendType := strings.ToLower(os.Getenv("BACKEND_TYPE"))
	
	if backendType == "" {
		backendType = "disk" // default to disk backend
	}

	var backend CacheBackend
	var err error

	switch backendType {
	case "disk":
		cacheDir := getCacheDir()
		backend, err = NewDiskBackend(cacheDir)
	
	case "s3":
		bucket := os.Getenv("S3_BUCKET")
		if bucket == "" {
			return nil, fmt.Errorf("S3_BUCKET environment variable is required for S3 backend")
		}
		
		prefix := os.Getenv("S3_PREFIX")
		tmpDir := os.Getenv("S3_TMP_DIR")
		if tmpDir == "" {
			tmpDir = filepath.Join(os.TempDir(), "gobuildcache-s3")
		}
		
		backend, err = NewS3Backend(bucket, prefix, tmpDir)
	
	default:
		return nil, fmt.Errorf("unknown backend type: %s (supported: disk, s3)", backendType)
	}

	if err != nil {
		return nil, err
	}

	// Wrap with debug backend if debug mode is enabled
	if debug {
		backend = NewDebugBackend(backend)
	}

	return backend, nil
}
