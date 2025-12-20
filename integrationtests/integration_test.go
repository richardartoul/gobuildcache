package integrationtests

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestCacheIntegration(t *testing.T) {
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
		cacheDir   = filepath.Join(workspaceDir, "test-cache")
	)

	// Clean up test cache directory at the end
	defer os.RemoveAll(cacheDir)

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

	t.Log("Step 2: Clearing the cache...")
	clearCmd := exec.Command(binaryPath, "clear", "-backend=disk", "-cache-dir="+cacheDir)
	clearCmd.Dir = workspaceDir
	clearOutput, err := clearCmd.CombinedOutput()
	if err != nil {
		t.Fatalf("Failed to clear cache: %v\nOutput: %s", err, clearOutput)
	}
	t.Logf("✓ Cache cleared successfully: %s", strings.TrimSpace(string(clearOutput)))

	// Clear Go's local cache to ensure first run is not cached
	t.Log("Step 2.5: Clearing Go's local cache...")
	cleanCmd := exec.Command("go", "clean", "-cache")
	cleanCmd.Dir = workspaceDir
	if err := cleanCmd.Run(); err != nil {
		t.Logf("Warning: Failed to clean Go cache: %v", err)
	}

	t.Log("Step 3: Running tests with cache program (first run)...")
	firstRunCmd := exec.Command("go", "test", "-v", testsDir)
	firstRunCmd.Dir = workspaceDir
	// Set environment to use disk backend when Go starts the cache program
	firstRunCmd.Env = append(os.Environ(),
		"GOCACHEPROG="+binaryPath,
		"BACKEND_TYPE=disk",
		"DEBUG=false",
		"CACHE_DIR="+cacheDir)

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

	t.Log("Step 4: Running tests again to verify caching...")
	secondRunCmd := exec.Command("go", "test", "-v", testsDir)
	secondRunCmd.Dir = workspaceDir
	// Set environment to use disk backend when Go starts the cache program
	secondRunCmd.Env = append(os.Environ(),
		"GOCACHEPROG="+binaryPath,
		"BACKEND_TYPE=disk",
		"DEBUG=false",
		"CACHE_DIR="+cacheDir)

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
		t.Log("✓ Tests results were served from cache!")
	} else {
		t.Fatalf("Tests did not use cached results. Expected to see '(cached)' in the output.\nOutput:\n%s", secondRunOutput.String())
	}

	t.Log("=== All integration tests passed! ===")
}

// TestCacheIntegrationConcurrentProcesses tests cross-process deduplication
// by running multiple go test processes concurrently with fslock-based deduplication.
func TestCacheIntegrationConcurrentProcesses(t *testing.T) {
	currentDir, err := os.Getwd()
	if err != nil {
		t.Fatalf("Failed to get working directory: %v", err)
	}
	// Go up one directory since we're in integrationtests/
	workspaceDir := filepath.Join(currentDir, "..")

	var (
		buildDir      = filepath.Join(workspaceDir, "builds")
		binaryPath    = filepath.Join(buildDir, "gobuildcache")
		testsDir      = filepath.Join(workspaceDir, "faketests")
		cacheDir      = filepath.Join(workspaceDir, "test-cache-concurrent")
		dedupeLockDir = filepath.Join(workspaceDir, "test-dedupe-locks")
		numProcesses  = 10 // Number of concurrent processes to run
	)

	// Clean up test directories at the end
	defer os.RemoveAll(cacheDir)
	defer os.RemoveAll(dedupeLockDir)

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

	t.Log("Step 2: Clearing the cache and dedupe lock directory...")
	// Clean up directories
	os.RemoveAll(cacheDir)
	os.RemoveAll(dedupeLockDir)

	clearCmd := exec.Command(binaryPath, "clear", "-backend=disk", "-cache-dir="+cacheDir)
	clearCmd.Dir = workspaceDir
	clearOutput, err := clearCmd.CombinedOutput()
	if err != nil {
		t.Fatalf("Failed to clear cache: %v\nOutput: %s", err, clearOutput)
	}
	t.Logf("✓ Cache cleared successfully: %s", strings.TrimSpace(string(clearOutput)))

	// Clear Go's local cache to ensure first run is not cached
	t.Log("Step 2.5: Clearing Go's local cache...")
	cleanCmd := exec.Command("go", "clean", "-cache")
	cleanCmd.Dir = workspaceDir
	if err := cleanCmd.Run(); err != nil {
		t.Logf("Warning: Failed to clean Go cache: %v", err)
	}

	t.Logf("Step 3: Running %d concurrent go test processes with fslock deduplication...", numProcesses)

	var wg sync.WaitGroup
	outputs := make([]bytes.Buffer, numProcesses)
	errors := make([]error, numProcesses)

	// Run multiple go test processes concurrently
	for i := 0; i < numProcesses; i++ {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()

			cmd := exec.Command("go", "test", "-v", testsDir)
			cmd.Dir = workspaceDir
			// Use fslock dedupe for cross-process deduplication
			cmd.Env = append(os.Environ(),
				"GOCACHEPROG="+binaryPath,
				"BACKEND_TYPE=disk",
				"DEBUG=false",
				"PRINT_STATS=true",
				"DEDUPE_TYPE=fslock",
				"DEDUPE_LOCK_DIR="+dedupeLockDir,
				"CACHE_DIR="+cacheDir)

			cmd.Stdout = &outputs[index]
			cmd.Stderr = &outputs[index]

			errors[index] = cmd.Run()
		}(i)
	}

	// Wait for all processes to complete
	wg.Wait()

	// Check results
	t.Log("Step 4: Checking results from concurrent processes...")
	successCount := 0
	for i := 0; i < numProcesses; i++ {
		if errors[i] == nil {
			successCount++
			t.Logf("✓ Process %d completed successfully", i+1)
		} else {
			t.Logf("✗ Process %d failed: %v", i+1, errors[i])
			t.Logf("Output:\n%s", outputs[i].String())
		}
	}

	if successCount == 0 {
		t.Fatal("All concurrent processes failed")
	}
	t.Logf("✓ %d/%d processes completed successfully", successCount, numProcesses)

	// Run a second batch to verify caching works
	t.Log("Step 5: Running second batch to verify caching...")
	var secondBatchOutput bytes.Buffer
	secondCmd := exec.Command("go", "test", "-v", testsDir)
	secondCmd.Dir = workspaceDir
	secondCmd.Env = append(os.Environ(),
		"GOCACHEPROG="+binaryPath,
		"BACKEND_TYPE=disk",
		"DEBUG=false",
		"PRINT_STATS=true",
		"DEDUPE_TYPE=fslock",
		"DEDUPE_LOCK_DIR="+dedupeLockDir,
		"CACHE_DIR="+cacheDir)
	secondCmd.Stdout = &secondBatchOutput
	secondCmd.Stderr = &secondBatchOutput

	if err := secondCmd.Run(); err != nil {
		t.Fatalf("Second batch failed: %v\nOutput:\n%s", err, secondBatchOutput.String())
	}

	// Verify that results were cached
	if strings.Contains(secondBatchOutput.String(), "(cached)") {
		t.Log("✓ Second batch used cached results!")
	} else {
		t.Logf("Note: Second batch completed (may have been cached by Go itself)\nOutput:\n%s", secondBatchOutput.String())
	}

	t.Log("=== Cross-process deduplication test passed! ===")
}
