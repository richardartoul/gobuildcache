package integrationtests

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

func TestCacheIntegrationGCS(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping GCS integration test in short mode")
	}

	// Get GCS bucket from environment - required for GCS tests
	gcsBucket := os.Getenv("TEST_GCS_BUCKET")
	if gcsBucket == "" {
		t.Fatal("TEST_GCS_BUCKET environment variable not set")
	}

	// Verify GCP credentials are available
	// GCS client uses Application Default Credentials (GOOGLE_APPLICATION_CREDENTIALS
	// or metadata service), so we just check if the env var is set or if we're
	// running in GCP (which would use metadata service)
	if os.Getenv("GOOGLE_APPLICATION_CREDENTIALS") == "" {
		// Check if we're in a GCP environment (metadata service available)
		// For now, we'll just warn - the actual connection will fail if credentials are missing
		t.Log("Warning: GOOGLE_APPLICATION_CREDENTIALS not set. Will attempt to use metadata service if running in GCP.")
	}

	currentDir, err := os.Getwd()
	if err != nil {
		t.Fatalf("Failed to get working directory: %v", err)
	}
	// Go up one directory since we're in integrationtests/
	workspaceDir := filepath.Join(currentDir, "..")

	var (
		buildDir   = filepath.Join(workspaceDir, "builds")
		binaryPath = filepath.Join(buildDir, "gobuildcache")
		testsDir   = filepath.Join(workspaceDir, "faketests")
		// Use a unique bucket prefix to avoid conflicts with concurrent tests
		bucketPrefix = fmt.Sprintf("test-cache-%d", time.Now().Unix())
	)

	t.Logf("Using GCS bucket: %s with prefix: %s", gcsBucket, bucketPrefix)

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
	gcsEnv := baseEnv

	t.Log("Step 2: Clearing the GCS cache...")
	clearCmd := exec.Command(binaryPath, "clear",
		"-debug",
		"-backend=gcs",
		"-gcs-bucket="+gcsBucket,
		"-gcs-prefix="+bucketPrefix+"/")
	clearCmd.Dir = workspaceDir
	clearCmd.Env = gcsEnv
	clearOutput, err := clearCmd.CombinedOutput()
	if err != nil {
		t.Fatalf("Failed to clear GCS cache: %v\nOutput: %s", err, clearOutput)
	}
	t.Logf("✓ GCS cache cleared successfully: %s", strings.TrimSpace(string(clearOutput)))

	// Note: We don't start a separate server. Go's GOCACHEPROG will start
	// the cache server automatically when needed, using the environment
	// variables we set (BACKEND_TYPE, GCS_BUCKET, GCS_PREFIX).

	t.Log("Step 3: Running tests with GCS cache (first run)...")
	firstRunCmd := exec.Command("go", "test", "-v", testsDir)
	firstRunCmd.Dir = workspaceDir
	// Set environment to use GCS backend when Go starts the cache program
	firstRunCmd.Env = append(baseEnv,
		"GOCACHEPROG="+binaryPath,
		"BACKEND_TYPE=gcs",
		"DEBUG=true",
		"GCS_BUCKET="+gcsBucket,
		"GCS_PREFIX="+bucketPrefix+"/")

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

	t.Log("Step 4: Running tests again to verify GCS caching...")
	secondRunCmd := exec.Command("go", "test", "-v", testsDir)
	secondRunCmd.Dir = workspaceDir
	// Set environment to use GCS backend when Go starts the cache program
	secondRunCmd.Env = append(baseEnv,
		"GOCACHEPROG="+binaryPath,
		"BACKEND_TYPE=gcs",
		"DEBUG=true",
		"GCS_BUCKET="+gcsBucket,
		"GCS_PREFIX="+bucketPrefix+"/")

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
		t.Log("✓ Tests results were served from GCS cache!")
	} else {
		t.Fatalf("Tests did not use cached results from GCS. Expected to see '(cached)' in the output.\nOutput:\n%s", secondRunOutput.String())
	}

	// Final cleanup - clear the test data from GCS
	t.Log("Step 5: Cleaning up GCS test data...")
	finalClearCmd := exec.Command(binaryPath, "clear",
		"-debug",
		"-backend=gcs",
		"-gcs-bucket="+gcsBucket,
		"-gcs-prefix="+bucketPrefix+"/")
	finalClearCmd.Dir = workspaceDir
	finalClearCmd.Env = gcsEnv
	if output, err := finalClearCmd.CombinedOutput(); err != nil {
		t.Logf("Warning: Failed to clean up GCS test data: %v\nOutput: %s", err, output)
	} else {
		t.Log("✓ GCS test data cleaned up")
	}

	t.Log("=== All GCS integration tests passed! ===")
}
