package main

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestCacheIntegrationErrorBackend(t *testing.T) {
	workspaceDir, err := os.Getwd()
	if err != nil {
		t.Fatalf("Failed to get working directory: %v", err)
	}

	var (
		buildDir   = filepath.Join(workspaceDir, "builds")
		binaryPath = filepath.Join(buildDir, "gobuildcache")
		testsDir   = filepath.Join(workspaceDir, "tests")
		cacheDir   = filepath.Join(workspaceDir, "test-cache-error")
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

	// Test with 50% error rate - some operations should fail, but not all
	errorRate := 0.5
	t.Logf("Step 2: Running tests with %.2f%% error rate...", errorRate*100)

	// Clear Go's local cache to ensure clean state
	cleanCmd := exec.Command("go", "clean", "-cache")
	cleanCmd.Dir = workspaceDir
	if err := cleanCmd.Run(); err != nil {
		t.Logf("Warning: Failed to clean Go cache: %v", err)
	}

	// Run tests with error backend enabled
	testCmd := exec.Command("go", "test", "-v", testsDir)
	testCmd.Dir = workspaceDir
	testCmd.Env = append(os.Environ(),
		"GOCACHEPROG="+binaryPath,
		"BACKEND_TYPE=disk",
		"CACHE_DIR="+cacheDir,
		"ERROR_RATE="+fmt.Sprintf("%f", errorRate),
		"DEBUG=false")

	var testOutput bytes.Buffer
	testCmd.Stdout = &testOutput
	testCmd.Stderr = &testOutput

	// Note: Tests may fail due to cache errors, which is expected
	err = testCmd.Run()
	if err != nil {
		t.Fatalf("Tests failed with %.2f%% error rate: %v\nOutput:\n%s", errorRate*100, err, testOutput.String())
	}
	output := testOutput.String()
	t.Logf("Test output:\n%s", output)

	// Check that the error backend was activated
	if !strings.Contains(output, "Error injection enabled") {
		t.Errorf("Expected to see error injection message in output")
	}

	// Check for simulated errors in the output
	if strings.Contains(output, "simulated") && strings.Contains(output, "error") {
		t.Log("✓ Error backend successfully injected errors")
	} else {
		t.Log("Note: No simulated errors detected in output (this can happen with low error rates)")
	}

	t.Log("✓ Tests passed despite error injection")
	t.Log("=== Error backend integration tests passed! ===")
}
