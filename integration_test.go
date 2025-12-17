package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestCacheIntegration(t *testing.T) {
	workspaceDir, err := os.Getwd()
	if err != nil {
		t.Fatalf("Failed to get working directory: %v", err)
	}

	var (
		buildDir   = filepath.Join(workspaceDir, "builds")
		binaryPath = filepath.Join(buildDir, "gobuildcache")
		testsDir   = filepath.Join(workspaceDir, "tests")
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
