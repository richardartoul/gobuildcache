package main

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestCacheIntegrationS3(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping S3 integration test in short mode")
	}

	// Get S3 bucket from environment - required for S3 tests
	s3Bucket := os.Getenv("TEST_S3_BUCKET")
	if s3Bucket == "" {
		t.Fatal("TEST_S3_BUCKET environment variable not set")
	}

	// Verify AWS credentials are set
	if os.Getenv("AWS_ACCESS_KEY_ID") == "" {
		t.Fatal("AWS_ACCESS_KEY_ID environment variable not set")
	}
	if os.Getenv("AWS_SECRET_ACCESS_KEY") == "" {
		t.Fatal("AWS_SECRET_ACCESS_KEY environment variable not set")
	}

	workspaceDir, err := os.Getwd()
	if err != nil {
		t.Fatalf("Failed to get working directory: %v", err)
	}

	var (
		buildDir   = filepath.Join(workspaceDir, "builds")
		binaryPath = filepath.Join(buildDir, "gobuildcache")
		testsDir   = filepath.Join(workspaceDir, "tests")
		// Use a unique bucket prefix to avoid conflicts with concurrent tests
		bucketPrefix = fmt.Sprintf("test-cache-%d", time.Now().Unix())
	)

	t.Logf("Using S3 bucket: %s with prefix: %s", s3Bucket, bucketPrefix)

	t.Log("Step 1: Compiling the binary...")
	if err := os.MkdirAll(buildDir, 0755); err != nil {
		t.Fatalf("Failed to create build directory: %v", err)
	}

	buildCmd := exec.Command("go", "build", "-o", binaryPath, ".")
	buildCmd.Dir = workspaceDir
	buildOutput, err := buildCmd.CombinedOutput()
	if err != nil {
		t.Fatalf("Failed to compile binary: %v\nOutput: %s", err, buildOutput)
	}
	t.Log("✓ Binary compiled successfully")

	// Use current environment for all commands
	baseEnv := os.Environ()
	s3Env := baseEnv

	t.Log("Step 2: Clearing the S3 cache...")
	clearCmd := exec.Command(binaryPath, "clear",
		"-debug",
		"-backend=s3",
		"-s3-bucket="+s3Bucket,
		"-s3-prefix="+bucketPrefix+"/")
	clearCmd.Dir = workspaceDir
	clearCmd.Env = s3Env
	clearOutput, err := clearCmd.CombinedOutput()
	if err != nil {
		t.Fatalf("Failed to clear S3 cache: %v\nOutput: %s", err, clearOutput)
	}
	t.Logf("✓ S3 cache cleared successfully: %s", strings.TrimSpace(string(clearOutput)))

	// Note: We don't start a separate server. Go's GOCACHEPROG will start
	// the cache server automatically when needed, using the environment
	// variables we set (BACKEND_TYPE, S3_BUCKET, S3_PREFIX).

	t.Log("Step 3: Running tests with S3 cache (first run)...")
	firstRunCmd := exec.Command("go", "test", "-v", testsDir)
	firstRunCmd.Dir = workspaceDir
	// Set environment to use S3 backend when Go starts the cache program
	firstRunCmd.Env = append(baseEnv,
		"GOCACHEPROG="+binaryPath,
		"BACKEND_TYPE=s3",
		"S3_BUCKET="+s3Bucket,
		"S3_PREFIX="+bucketPrefix+"/")

	var firstRunOutput bytes.Buffer
	firstRunCmd.Stdout = &firstRunOutput
	firstRunCmd.Stderr = &firstRunOutput

	if err := firstRunCmd.Run(); err != nil {
		t.Fatalf("Tests failed on first run: %v\nOutput:\n%s", err, firstRunOutput.String())
	}

	t.Logf("First run output:\n%s", firstRunOutput.String())
	t.Log("✓ Tests passed on first run")

	if strings.Contains(firstRunOutput.String(), "(cached)") {
		t.Fatal("First run should not be cached, but found '(cached)' in output")
	}
	t.Log("✓ First run was not cached (as expected)")

	t.Log("Step 4: Running tests again to verify S3 caching...")
	secondRunCmd := exec.Command("go", "test", "-v", testsDir)
	secondRunCmd.Dir = workspaceDir
	// Set environment to use S3 backend when Go starts the cache program
	secondRunCmd.Env = append(baseEnv,
		"GOCACHEPROG="+binaryPath,
		"BACKEND_TYPE=s3",
		"S3_BUCKET="+s3Bucket,
		"S3_PREFIX="+bucketPrefix+"/")

	var secondRunOutput bytes.Buffer
	secondRunCmd.Stdout = &secondRunOutput
	secondRunCmd.Stderr = &secondRunOutput

	if err := secondRunCmd.Run(); err != nil {
		t.Fatalf("Tests failed on second run: %v\nOutput:\n%s", err, secondRunOutput.String())
	}

	t.Logf("Second run output:\n%s", secondRunOutput.String())
	t.Log("✓ Tests passed on second run")

	// Verify that results were cached
	if strings.Contains(secondRunOutput.String(), "(cached)") {
		t.Log("✓ Tests results were served from S3 cache!")
	} else {
		t.Fatalf("Tests did not use cached results from S3. Expected to see '(cached)' in the output.\nOutput:\n%s", secondRunOutput.String())
	}

	// Final cleanup - clear the test data from S3
	t.Log("Step 5: Cleaning up S3 test data...")
	finalClearCmd := exec.Command(binaryPath, "clear",
		"-debug",
		"-backend=s3",
		"-s3-bucket="+s3Bucket,
		"-s3-prefix="+bucketPrefix+"/")
	finalClearCmd.Dir = workspaceDir
	finalClearCmd.Env = s3Env
	if output, err := finalClearCmd.CombinedOutput(); err != nil {
		t.Logf("Warning: Failed to clean up S3 test data: %v\nOutput: %s", err, output)
	} else {
		t.Log("✓ S3 test data cleaned up")
	}

	t.Log("=== All S3 integration tests passed! ===")
}

// // TestCacheIntegrationS3Concurrent tests concurrent access with S3 backend
// func TestCacheIntegrationS3Concurrent(t *testing.T) {
// 	if testing.Short() {
// 		t.Skip("Skipping S3 concurrent integration test in short mode")
// 	}

// 	// Get S3 bucket from environment - required for S3 tests
// 	s3Bucket := os.Getenv("TEST_S3_BUCKET")
// 	if s3Bucket == "" {
// 		t.Fatal("TEST_S3_BUCKET environment variable not set")
// 	}

// 	// Verify AWS credentials are set
// 	if os.Getenv("AWS_ACCESS_KEY_ID") == "" {
// 		t.Fatal("AWS_ACCESS_KEY_ID environment variable not set")
// 	}
// 	if os.Getenv("AWS_SECRET_ACCESS_KEY") == "" {
// 		t.Fatal("AWS_SECRET_ACCESS_KEY environment variable not set")
// 	}

// 	workspaceDir, err := os.Getwd()
// 	if err != nil {
// 		t.Fatalf("Failed to get working directory: %v", err)
// 	}

// 	var (
// 		buildDir     = filepath.Join(workspaceDir, "builds")
// 		binaryPath   = filepath.Join(buildDir, "gobuildcache")
// 		testsDir     = filepath.Join(workspaceDir, "tests")
// 		bucketPrefix = fmt.Sprintf("test-concurrent-%d", time.Now().Unix())
// 	)

// 	t.Logf("Using S3 bucket: %s with prefix: %s", s3Bucket, bucketPrefix)

// 	// Use current environment for all commands
// 	baseEnv := os.Environ()
// 	s3Env := baseEnv

// 	// Clear the cache first
// 	clearCmd := exec.Command(binaryPath, "clear",
// 		"-backend=s3",
// 		"-s3-bucket="+s3Bucket,
// 		"-s3-prefix="+bucketPrefix+"/")
// 	clearCmd.Env = s3Env
// 	if err := clearCmd.Run(); err != nil {
// 		t.Fatalf("Failed to clear cache: %v", err)
// 	}

// 	t.Log("Running multiple concurrent test builds with S3 backend...")

// 	// Run 5 concurrent test builds
// 	const numRuns = 5
// 	type result struct {
// 		index  int
// 		output string
// 		err    error
// 	}

// 	results := make(chan result, numRuns)

// 	for i := 0; i < numRuns; i++ {
// 		go func(index int) {
// 			cmd := exec.Command("go", "test", "-v", testsDir)
// 			cmd.Dir = workspaceDir
// 			cmd.Env = append(baseEnv, "GOCACHEPROG="+binaryPath)

// 			var output bytes.Buffer
// 			cmd.Stdout = &output
// 			cmd.Stderr = &output

// 			err := cmd.Run()
// 			results <- result{index: index, output: output.String(), err: err}
// 		}(i)
// 	}

// 	// Collect results
// 	var cachedCount, uncachedCount int
// 	for i := 0; i < numRuns; i++ {
// 		res := <-results
// 		if res.err != nil {
// 			t.Errorf("Concurrent run %d failed: %v\nOutput:\n%s", res.index, res.err, res.output)
// 			continue
// 		}

// 		if strings.Contains(res.output, "(cached)") {
// 			cachedCount++
// 			t.Logf("Run %d: Used cache ✓", res.index)
// 		} else {
// 			uncachedCount++
// 			t.Logf("Run %d: Fresh build", res.index)
// 		}
// 	}

// 	t.Logf("Results: %d cached, %d uncached", cachedCount, uncachedCount)

// 	// We expect at least some caching (not all runs will be cached due to race conditions)
// 	if cachedCount == 0 {
// 		t.Error("Expected at least some cached results in concurrent runs")
// 	}

// 	// Cleanup
// 	finalClearCmd := exec.Command(binaryPath, "clear",
// 		"-debug",
// 		"-backend=s3",
// 		"-s3-bucket="+s3Bucket,
// 		"-s3-prefix="+bucketPrefix+"/")
// 	finalClearCmd.Env = baseEnv
// 	finalClearCmd.Run()

// 	t.Log("=== S3 concurrent integration test passed! ===")
// }
