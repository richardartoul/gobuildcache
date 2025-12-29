package main

import (
	"flag"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/richardartoul/gobuildcache/pkg/backends"
	"github.com/richardartoul/gobuildcache/pkg/locking"
)

// Global flags
var (
	debug        bool
	printStats   bool
	backendType  string
	lockingType  string
	lockDir      string
	cacheDir     string
	s3Bucket     string
	s3Prefix     string
	errorRate    float64
	compression  bool
	asyncBackend bool
)

func main() {
	// Check if we have a subcommand
	if len(os.Args) > 1 && !strings.HasPrefix(os.Args[1], "-") {
		subcommand := os.Args[1]

		switch subcommand {
		case "clear":
			runClearCommand()
			return
		case "clear-local":
			runClearLocalCommand()
			return
		case "clear-remote":
			runClearRemoteCommand()
			return
		case "help", "-h", "--help":
			printHelp()
			return
		default:
			fmt.Fprintf(os.Stderr, "Unknown subcommand: %s\n\n", subcommand)
			printHelp()
			os.Exit(1)
		}
	}

	// No subcommand or starts with -, run the server
	runServerCommand()
}

func runServerCommand() {
	// Get defaults from environment variables.
	var (
		serverFlags         = flag.NewFlagSet("server", flag.ExitOnError)
		debugDefault        = getEnvBool("DEBUG", false)
		printStatsDefault   = getEnvBool("PRINT_STATS", true)
		backendDefault      = getEnv("BACKEND_TYPE", getEnv("BACKEND", "disk"))
		lockTypeDefault     = getEnv("LOCK_TYPE", "fslock")
		lockDirDefault      = getEnv("LOCK_DIR", filepath.Join(os.TempDir(), "gobuildcache", "locks"))
		cacheDirDefault     = getEnv("CACHE_DIR", filepath.Join(os.TempDir(), "gobuildcache", "cache"))
		s3BucketDefault     = getEnv("S3_BUCKET", "")
		s3PrefixDefault     = getEnv("S3_PREFIX", "gobuildcache/")
		errorRateDefault    = getEnvFloat("ERROR_RATE", 0.0)
		compressionDefault  = getEnvBool("COMPRESSION", true)
		asyncBackendDefault = getEnvBool("ASYNC_BACKEND", true)
	)
	serverFlags.BoolVar(&debug, "debug", debugDefault, "Enable debug logging to stderr (env: DEBUG)")
	serverFlags.BoolVar(&printStats, "stats", printStatsDefault, "Print cache statistics on exit (env: PRINT_STATS)")
	serverFlags.StringVar(&backendType, "backend", backendDefault, "Backend type: disk (local only), s3 (env: BACKEND_TYPE)")
	serverFlags.StringVar(&lockingType, "lock-type", lockTypeDefault, "Locking type: memory (in-memory), fslock (filesystem) (env: LOCK_TYPE)")
	serverFlags.StringVar(&lockDir, "lock-dir", lockDirDefault, "Lock directory for fslock (env: LOCK_DIR)")
	serverFlags.StringVar(&cacheDir, "cache-dir", cacheDirDefault, "Local cache directory (env: CACHE_DIR)")
	serverFlags.StringVar(&s3Bucket, "s3-bucket", s3BucketDefault, "S3 bucket name (required for s3 backend) (env: S3_BUCKET)")
	serverFlags.StringVar(&s3Prefix, "s3-prefix", s3PrefixDefault, "S3 key prefix (optional) (env: S3_PREFIX)")
	serverFlags.Float64Var(&errorRate, "error-rate", errorRateDefault, "Error injection rate (0.0-1.0) for testing error handling (env: ERROR_RATE)")
	serverFlags.BoolVar(&compression, "compression", compressionDefault, "Enable LZ4 compression for backend storage (env: COMPRESSION)")
	serverFlags.BoolVar(&asyncBackend, "async-backend", asyncBackendDefault, "Enable async backend writer for non-blocking PUT operations (env: ASYNC_BACKEND)")

	serverFlags.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: %s [flags]\n\n", os.Args[0])
		fmt.Fprintf(os.Stderr, "Run the Go build cache server.\n\n")
		fmt.Fprintf(os.Stderr, "Flags (can also be set via environment variables):\n")
		serverFlags.PrintDefaults()
		fmt.Fprintf(os.Stderr, "\nEnvironment Variables:\n")
		fmt.Fprintf(os.Stderr, "  DEBUG            Enable debug logging (true/false)\n")
		fmt.Fprintf(os.Stderr, "  PRINT_STATS      Print cache statistics on exit (true/false)\n")
		fmt.Fprintf(os.Stderr, "  BACKEND_TYPE     Backend type (disk, s3)\n")
		fmt.Fprintf(os.Stderr, "  LOCK_TYPE        Deduplication type (memory, fslock)\n")
		fmt.Fprintf(os.Stderr, "  LOCK_DIR         Lock directory for fslock\n")
		fmt.Fprintf(os.Stderr, "  CACHE_DIR        Local cache directory\n")
		fmt.Fprintf(os.Stderr, "  S3_BUCKET        S3 bucket name\n")
		fmt.Fprintf(os.Stderr, "  S3_PREFIX        S3 key prefix\n")
		fmt.Fprintf(os.Stderr, "  COMPRESSION      Enable LZ4 compression (true/false)\n")
		fmt.Fprintf(os.Stderr, "  ASYNC_BACKEND    Enable async backend writer (true/false)\n")
		fmt.Fprintf(os.Stderr, "\nNote: Command-line flags take precedence over environment variables.\n")
		fmt.Fprintf(os.Stderr, "\nExamples:\n")
		fmt.Fprintf(os.Stderr, "  # Run with disk backend using flags:\n")
		fmt.Fprintf(os.Stderr, "  %s -cache-dir=/var/cache/go\n\n", os.Args[0])
		fmt.Fprintf(os.Stderr, "  # Run with S3 backend using flags:\n")
		fmt.Fprintf(os.Stderr, "  %s -backend=s3 -s3-bucket=my-cache-bucket\n\n", os.Args[0])
		fmt.Fprintf(os.Stderr, "  # Run with environment variables:\n")
		fmt.Fprintf(os.Stderr, "  BACKEND_TYPE=s3 S3_BUCKET=my-cache-bucket %s\n\n", os.Args[0])
		fmt.Fprintf(os.Stderr, "  # Mix environment variables and flags (flags override env):\n")
		fmt.Fprintf(os.Stderr, "  BACKEND_TYPE=s3 %s -s3-bucket=my-cache-bucket -debug\n", os.Args[0])
	}

	serverFlags.Parse(os.Args[1:])
	runServer()
}

func runClearCommand() {
	// Get defaults from environment variables.
	var (
		clearFlags      = flag.NewFlagSet("clear", flag.ExitOnError)
		debugDefault    = getEnvBool("DEBUG", false)
		backendDefault  = getEnv("BACKEND_TYPE", getEnv("BACKEND", "disk"))
		cacheDirDefault = getEnv("CACHE_DIR", filepath.Join(os.TempDir(), "gobuildcache", "cache"))
		s3BucketDefault = getEnv("S3_BUCKET", "")
		s3PrefixDefault = getEnv("S3_PREFIX", "")
	)
	clearFlags.BoolVar(&debug, "debug", debugDefault, "Enable debug logging to stderr (env: DEBUG)")
	clearFlags.StringVar(&backendType, "backend", backendDefault, "Backend type: disk (local only), s3 (env: BACKEND_TYPE)")
	clearFlags.StringVar(&cacheDir, "cache-dir", cacheDirDefault, "Local cache directory (env: CACHE_DIR)")
	clearFlags.StringVar(&s3Bucket, "s3-bucket", s3BucketDefault, "S3 bucket name (required for s3 backend) (env: S3_BUCKET)")
	clearFlags.StringVar(&s3Prefix, "s3-prefix", s3PrefixDefault, "S3 key prefix (optional) (env: S3_PREFIX)")

	clearFlags.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: %s clear [flags]\n\n", os.Args[0])
		fmt.Fprintf(os.Stderr, "Clear all entries from the cache.\n\n")
		fmt.Fprintf(os.Stderr, "Flags (can also be set via environment variables):\n")
		clearFlags.PrintDefaults()
		fmt.Fprintf(os.Stderr, "\nEnvironment Variables:\n")
		fmt.Fprintf(os.Stderr, "  DEBUG          Enable debug logging (true/false)\n")
		fmt.Fprintf(os.Stderr, "  PRINT_STATS    Print cache statistics on exit (true/false)\n")
		fmt.Fprintf(os.Stderr, "  BACKEND_TYPE   Backend type (disk, s3)\n")
		fmt.Fprintf(os.Stderr, "  CACHE_DIR      Local cache directory\n")
		fmt.Fprintf(os.Stderr, "  S3_BUCKET      S3 bucket name\n")
		fmt.Fprintf(os.Stderr, "  S3_PREFIX      S3 key prefix\n")
		fmt.Fprintf(os.Stderr, "  S3_TMP_DIR     Local temp directory for S3 backend\n")
		fmt.Fprintf(os.Stderr, "\nNote: Command-line flags take precedence over environment variables.\n")
		fmt.Fprintf(os.Stderr, "\nExamples:\n")
		fmt.Fprintf(os.Stderr, "  # Clear disk cache using flags:\n")
		fmt.Fprintf(os.Stderr, "  %s clear -cache-dir=/var/cache/go\n\n", os.Args[0])
		fmt.Fprintf(os.Stderr, "  # Clear S3 cache using flags:\n")
		fmt.Fprintf(os.Stderr, "  %s clear -backend=s3 -s3-bucket=my-cache-bucket\n\n", os.Args[0])
		fmt.Fprintf(os.Stderr, "  # Clear using environment variables:\n")
		fmt.Fprintf(os.Stderr, "  BACKEND_TYPE=s3 S3_BUCKET=my-cache-bucket %s clear\n", os.Args[0])
	}

	clearFlags.Parse(os.Args[2:])
	runClear()
}

func runClearLocalCommand() {
	// Get defaults from environment variables
	var (
		clearLocalFlags = flag.NewFlagSet("clear-local", flag.ExitOnError)
		debugDefault    = getEnvBool("DEBUG", false)
		cacheDirDefault = getEnv("CACHE_DIR", filepath.Join(os.TempDir(), "gobuildcache", "cache"))
	)
	clearLocalFlags.BoolVar(&debug, "debug", debugDefault, "Enable debug logging to stderr (env: DEBUG)")
	clearLocalFlags.StringVar(&cacheDir, "cache-dir", cacheDirDefault, "Local cache directory (env: CACHE_DIR)")

	clearLocalFlags.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: %s clear-local [flags]\n\n", os.Args[0])
		fmt.Fprintf(os.Stderr, "Clear only the local filesystem cache directory.\n\n")
		fmt.Fprintf(os.Stderr, "Flags (can also be set via environment variables):\n")
		clearLocalFlags.PrintDefaults()
		fmt.Fprintf(os.Stderr, "\nEnvironment Variables:\n")
		fmt.Fprintf(os.Stderr, "  DEBUG          Enable debug logging (true/false)\n")
		fmt.Fprintf(os.Stderr, "  CACHE_DIR      Local cache directory\n")
		fmt.Fprintf(os.Stderr, "\nNote: Command-line flags take precedence over environment variables.\n")
		fmt.Fprintf(os.Stderr, "\nExamples:\n")
		fmt.Fprintf(os.Stderr, "  # Clear local cache using default directory:\n")
		fmt.Fprintf(os.Stderr, "  %s clear-local\n\n", os.Args[0])
		fmt.Fprintf(os.Stderr, "  # Clear local cache using custom directory:\n")
		fmt.Fprintf(os.Stderr, "  %s clear-local -cache-dir=/var/cache/go\n\n", os.Args[0])
		fmt.Fprintf(os.Stderr, "  # Clear using environment variables:\n")
		fmt.Fprintf(os.Stderr, "  CACHE_DIR=/var/cache/go %s clear-local\n", os.Args[0])
	}

	clearLocalFlags.Parse(os.Args[2:])

	// Clear the local cache directory
	if err := clearLocalCache(cacheDir); err != nil {
		fmt.Fprintf(os.Stderr, "Error clearing local cache: %v\n", err)
		os.Exit(1)
	}

	fmt.Fprintf(os.Stdout, "Local cache cleared successfully\n")
}

func runClearRemoteCommand() {
	// Get defaults from environment variables.
	var (
		clearRemoteFlags = flag.NewFlagSet("clear-remote", flag.ExitOnError)
		debugDefault     = getEnvBool("DEBUG", false)
		backendDefault   = getEnv("BACKEND_TYPE", getEnv("BACKEND", "disk"))
		s3BucketDefault  = getEnv("S3_BUCKET", "")
		s3PrefixDefault  = getEnv("S3_PREFIX", "")
	)
	clearRemoteFlags.BoolVar(&debug, "debug", debugDefault, "Enable debug logging to stderr (env: DEBUG)")
	clearRemoteFlags.StringVar(&backendType, "backend", backendDefault, "Backend type: disk, s3 (env: BACKEND_TYPE)")
	clearRemoteFlags.StringVar(&s3Bucket, "s3-bucket", s3BucketDefault, "S3 bucket name (required for s3 backend) (env: S3_BUCKET)")
	clearRemoteFlags.StringVar(&s3Prefix, "s3-prefix", s3PrefixDefault, "S3 key prefix (optional) (env: S3_PREFIX)")

	clearRemoteFlags.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: %s clear-remote [flags]\n\n", os.Args[0])
		fmt.Fprintf(os.Stderr, "Clear only the remote backend cache (e.g., S3).\n\n")
		fmt.Fprintf(os.Stderr, "Flags (can also be set via environment variables):\n")
		clearRemoteFlags.PrintDefaults()
		fmt.Fprintf(os.Stderr, "\nEnvironment Variables:\n")
		fmt.Fprintf(os.Stderr, "  DEBUG          Enable debug logging (true/false)\n")
		fmt.Fprintf(os.Stderr, "  BACKEND_TYPE   Backend type (disk, s3)\n")
		fmt.Fprintf(os.Stderr, "  S3_BUCKET      S3 bucket name\n")
		fmt.Fprintf(os.Stderr, "  S3_PREFIX      S3 key prefix\n")
		fmt.Fprintf(os.Stderr, "\nNote: Command-line flags take precedence over environment variables.\n")
		fmt.Fprintf(os.Stderr, "\nExamples:\n")
		fmt.Fprintf(os.Stderr, "  # Clear S3 cache using flags:\n")
		fmt.Fprintf(os.Stderr, "  %s clear-remote -backend=s3 -s3-bucket=my-cache-bucket\n\n", os.Args[0])
		fmt.Fprintf(os.Stderr, "  # Clear S3 cache with prefix:\n")
		fmt.Fprintf(os.Stderr, "  %s clear-remote -backend=s3 -s3-bucket=my-cache-bucket -s3-prefix=myproject/\n\n", os.Args[0])
		fmt.Fprintf(os.Stderr, "  # Clear using environment variables:\n")
		fmt.Fprintf(os.Stderr, "  BACKEND_TYPE=s3 S3_BUCKET=my-cache-bucket %s clear-remote\n", os.Args[0])
	}

	clearRemoteFlags.Parse(os.Args[2:])

	// Create backend
	backend, err := createBackend()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error creating backend: %v\n", err)
		os.Exit(1)
	}
	defer backend.Close()

	// Clear the backend (remote storage)
	if err := backend.Clear(); err != nil {
		fmt.Fprintf(os.Stderr, "Error clearing backend cache: %v\n", err)
		os.Exit(1)
	}

	fmt.Fprintf(os.Stdout, "Remote cache cleared successfully\n")
}

func printHelp() {
	fmt.Fprintf(os.Stderr, "Usage: %s [command] [flags]\n\n", os.Args[0])
	fmt.Fprintf(os.Stderr, "A remote caching server for Go builds.\n\n")
	fmt.Fprintf(os.Stderr, "Commands:\n")
	fmt.Fprintf(os.Stderr, "  (no command)  Run the cache server (default)\n")
	fmt.Fprintf(os.Stderr, "  clear         Clear both local and remote cache entries\n")
	fmt.Fprintf(os.Stderr, "  clear-local   Clear only local cache directory\n")
	fmt.Fprintf(os.Stderr, "  clear-remote  Clear only remote backend cache\n")
	fmt.Fprintf(os.Stderr, "  help          Show this help message\n\n")
	fmt.Fprintf(os.Stderr, "Configuration:\n")
	fmt.Fprintf(os.Stderr, "  Flags can be set via command-line arguments or environment variables.\n")
	fmt.Fprintf(os.Stderr, "  Command-line flags take precedence over environment variables.\n\n")
	fmt.Fprintf(os.Stderr, "Run '%s [command] -h' for more information about a command.\n", os.Args[0])
}

func runServer() {
	// Create backend
	backend, err := createBackend()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error creating cache backend: %v\n", err)
		os.Exit(1)
	}
	defer backend.Close()

	lockingGroup, err := createLockingGroup()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error creating lock group: %v\n", err)
		os.Exit(1)
	}

	prog, err := NewCacheProg(backend, lockingGroup, cacheDir, debug, printStats, compression)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error creating cache program: %v\n", err)
		os.Exit(1)
	}
	if err := prog.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "Error running cache program: %v\n", err)
		os.Exit(1)
	}
}

func runClear() {
	// Create backend
	backend, err := createBackend()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error creating cache backend: %v\n", err)
		os.Exit(1)
	}
	defer backend.Close()

	// Clear the backend (remote storage)
	if err := backend.Clear(); err != nil {
		fmt.Fprintf(os.Stderr, "Error clearing backend cache: %v\n", err)
		os.Exit(1)
	}

	// Clear the local cache directory
	if err := clearLocalCache(cacheDir); err != nil {
		fmt.Fprintf(os.Stderr, "Error clearing local cache: %v\n", err)
		os.Exit(1)
	}

	fmt.Fprintf(os.Stdout, "Cache cleared successfully\n")
}

// clearLocalCache removes all entries from the local cache directory.
func clearLocalCache(cacheDir string) error {
	// Remove the entire directory and recreate it
	// os.RemoveAll is idempotent - it doesn't error if path doesn't exist
	if err := os.RemoveAll(cacheDir); err != nil {
		return fmt.Errorf("failed to remove cache directory: %w", err)
	}

	// Recreate the directory
	if err := os.MkdirAll(cacheDir, 0755); err != nil {
		return fmt.Errorf("failed to recreate cache directory: %w", err)
	}

	return nil
}

func createBackend() (backends.Backend, error) {
	backendType = strings.ToLower(backendType)

	var backend backends.Backend
	var err error

	switch backendType {
	case "disk":
		// Use no-op backend - local caching is handled by server.go
		backend = backends.NewNoop()

	case "s3":
		if s3Bucket == "" {
			return nil, fmt.Errorf("S3 bucket is required for S3 backend (set via -s3-bucket flag or S3_BUCKET env var)")
		}

		backend, err = backends.NewS3(s3Bucket, s3Prefix)

	default:
		return nil, fmt.Errorf("unknown backend type: %s (supported: disk, s3)", backendType)
	}

	if err != nil {
		return nil, err
	}

	// Wrap with error backend if error rate is configured
	if errorRate > 0 {
		backend = backends.NewError(backend, errorRate)
		fmt.Fprintf(os.Stderr, "[INFO] Error injection enabled with rate: %.2f%%\n", errorRate*100)
	}

	// Wrap with async backend if enabled
	if asyncBackend {
		// Create logger for async backend
		logLevel := slog.LevelInfo
		if debug {
			logLevel = slog.LevelDebug
		}
		logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
			Level: logLevel,
		}))
		backend = backends.NewAsyncBackendWriter(backend, logger)
		fmt.Fprintf(os.Stderr, "[INFO] Async backend writer enabled\n")
	}

	// Wrap with debug backend if debug mode is enabled
	if debug {
		backend = backends.NewDebug(backend)
	}

	return backend, nil
}

func createLockingGroup() (locking.Group, error) {
	lockingType = strings.ToLower(lockingType)

	switch lockingType {
	case "memory", "":
		// Default: in-memory singleflight
		return locking.NewMemLock(), nil

	case "fslock", "fs":
		// Filesystem-backed deduplication
		group, err := locking.NewFlockGroup(lockDir)
		if err != nil {
			return nil, fmt.Errorf("failed to create fslock group: %w", err)
		}
		return group, nil

	case "noop":
		// No deduplication (useful for testing)
		return locking.NewNoOpGroup(), nil

	default:
		return nil, fmt.Errorf("unknown locking type: %s (supported: memory, fslock, noop)", lockingType)
	}
}

// getEnv gets an environment variable or returns a default value.
func getEnv(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

// getEnvBool gets a boolean environment variable or returns a default value.
// Accepts: true, false, 1, 0, yes, no (case insensitive).
func getEnvBool(key string, defaultValue bool) bool {
	value := strings.ToLower(os.Getenv(key))
	if value == "" {
		return defaultValue
	}
	return value == "true" || value == "1" || value == "yes"
}

// getEnvFloat gets a float64 environment variable or returns a default value.
func getEnvFloat(key string, defaultValue float64) float64 {
	value := os.Getenv(key)
	if value == "" {
		return defaultValue
	}
	var f float64
	if _, err := fmt.Sscanf(value, "%f", &f); err != nil {
		return defaultValue
	}
	return f
}
