package main

import (
	"strings"
	"testing"
)

func TestGetEnvWithPrefix(t *testing.T) {
	tests := []struct {
		name         string
		key          string
		defaultValue string
		envVars      map[string]string
		expected     string
	}{
		{
			name:         "returns default when neither env var is set",
			key:          "TEST_KEY",
			defaultValue: "default",
			envVars:      map[string]string{},
			expected:     "default",
		},
		{
			name:         "returns unprefixed value when only unprefixed is set",
			key:          "TEST_KEY",
			defaultValue: "default",
			envVars:      map[string]string{"TEST_KEY": "unprefixed_value"},
			expected:     "unprefixed_value",
		},
		{
			name:         "returns prefixed value when only prefixed is set",
			key:          "TEST_KEY",
			defaultValue: "default",
			envVars:      map[string]string{"GOBUILDCACHE_TEST_KEY": "prefixed_value"},
			expected:     "prefixed_value",
		},
		{
			name:         "prefixed value takes precedence over unprefixed",
			key:          "TEST_KEY",
			defaultValue: "default",
			envVars: map[string]string{
				"TEST_KEY":             "unprefixed_value",
				"GOBUILDCACHE_TEST_KEY": "prefixed_value",
			},
			expected: "prefixed_value",
		},
		{
			name:         "works with S3_BUCKET style keys",
			key:          "S3_BUCKET",
			defaultValue: "",
			envVars:      map[string]string{"GOBUILDCACHE_S3_BUCKET": "my-bucket"},
			expected:     "my-bucket",
		},
		{
			name:         "works with BACKEND_TYPE style keys",
			key:          "BACKEND_TYPE",
			defaultValue: "disk",
			envVars:      map[string]string{"GOBUILDCACHE_BACKEND_TYPE": "s3"},
			expected:     "s3",
		},
		{
			name:         "empty prefixed value falls through to unprefixed",
			key:          "TEST_KEY",
			defaultValue: "default",
			envVars: map[string]string{
				"TEST_KEY":              "unprefixed_value",
				"GOBUILDCACHE_TEST_KEY": "",
			},
			expected: "unprefixed_value",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Clear environment variables (t.Setenv auto-restores on test completion)
			t.Setenv(tt.key, "")
			t.Setenv("GOBUILDCACHE_"+tt.key, "")
			// Set test-specific environment variables
			for k, v := range tt.envVars {
				t.Setenv(k, v)
			}

			result := getEnvWithPrefix(tt.key, tt.defaultValue)
			if result != tt.expected {
				t.Errorf("getEnvWithPrefix(%q, %q) = %q, want %q", tt.key, tt.defaultValue, result, tt.expected)
			}
		})
	}
}

func TestGetEnvBoolWithPrefix(t *testing.T) {
	tests := []struct {
		name         string
		key          string
		defaultValue bool
		envVars      map[string]string
		expected     bool
	}{
		{
			name:         "returns default when neither env var is set",
			key:          "TEST_BOOL",
			defaultValue: false,
			envVars:      map[string]string{},
			expected:     false,
		},
		{
			name:         "returns true default when neither env var is set",
			key:          "TEST_BOOL",
			defaultValue: true,
			envVars:      map[string]string{},
			expected:     true,
		},
		{
			name:         "returns unprefixed value when only unprefixed is set",
			key:          "TEST_BOOL",
			defaultValue: false,
			envVars:      map[string]string{"TEST_BOOL": "true"},
			expected:     true,
		},
		{
			name:         "returns prefixed value when only prefixed is set",
			key:          "TEST_BOOL",
			defaultValue: false,
			envVars:      map[string]string{"GOBUILDCACHE_TEST_BOOL": "true"},
			expected:     true,
		},
		{
			name:         "prefixed value takes precedence over unprefixed",
			key:          "TEST_BOOL",
			defaultValue: false,
			envVars: map[string]string{
				"TEST_BOOL":             "false",
				"GOBUILDCACHE_TEST_BOOL": "true",
			},
			expected: true,
		},
		{
			name:         "prefixed false overrides unprefixed true",
			key:          "TEST_BOOL",
			defaultValue: true,
			envVars: map[string]string{
				"TEST_BOOL":             "true",
				"GOBUILDCACHE_TEST_BOOL": "false",
			},
			expected: false,
		},
		{
			name:         "accepts 1 as true",
			key:          "TEST_BOOL",
			defaultValue: false,
			envVars:      map[string]string{"GOBUILDCACHE_TEST_BOOL": "1"},
			expected:     true,
		},
		{
			name:         "accepts yes as true",
			key:          "TEST_BOOL",
			defaultValue: false,
			envVars:      map[string]string{"GOBUILDCACHE_TEST_BOOL": "yes"},
			expected:     true,
		},
		{
			name:         "accepts YES as true (case insensitive)",
			key:          "TEST_BOOL",
			defaultValue: false,
			envVars:      map[string]string{"GOBUILDCACHE_TEST_BOOL": "YES"},
			expected:     true,
		},
		{
			name:         "empty prefixed value falls through to unprefixed",
			key:          "TEST_BOOL",
			defaultValue: false,
			envVars: map[string]string{
				"TEST_BOOL":              "true",
				"GOBUILDCACHE_TEST_BOOL": "",
			},
			expected: true,
		},
		{
			name:         "invalid prefixed value falls through to unprefixed",
			key:          "TEST_BOOL",
			defaultValue: false,
			envVars: map[string]string{
				"TEST_BOOL":              "true",
				"GOBUILDCACHE_TEST_BOOL": "not-a-bool",
			},
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Clear environment variables (t.Setenv auto-restores on test completion)
			t.Setenv(tt.key, "")
			t.Setenv("GOBUILDCACHE_"+tt.key, "")
			// Set test-specific environment variables
			for k, v := range tt.envVars {
				t.Setenv(k, v)
			}

			result := getEnvBoolWithPrefix(tt.key, tt.defaultValue)
			if result != tt.expected {
				t.Errorf("getEnvBoolWithPrefix(%q, %v) = %v, want %v", tt.key, tt.defaultValue, result, tt.expected)
			}
		})
	}
}

func TestGetEnvFloatWithPrefix(t *testing.T) {
	tests := []struct {
		name         string
		key          string
		defaultValue float64
		envVars      map[string]string
		expected     float64
	}{
		{
			name:         "returns default when neither env var is set",
			key:          "TEST_FLOAT",
			defaultValue: 0.5,
			envVars:      map[string]string{},
			expected:     0.5,
		},
		{
			name:         "returns unprefixed value when only unprefixed is set",
			key:          "TEST_FLOAT",
			defaultValue: 0.0,
			envVars:      map[string]string{"TEST_FLOAT": "0.75"},
			expected:     0.75,
		},
		{
			name:         "returns prefixed value when only prefixed is set",
			key:          "TEST_FLOAT",
			defaultValue: 0.0,
			envVars:      map[string]string{"GOBUILDCACHE_TEST_FLOAT": "0.25"},
			expected:     0.25,
		},
		{
			name:         "prefixed value takes precedence over unprefixed",
			key:          "TEST_FLOAT",
			defaultValue: 0.0,
			envVars: map[string]string{
				"TEST_FLOAT":             "0.5",
				"GOBUILDCACHE_TEST_FLOAT": "0.9",
			},
			expected: 0.9,
		},
		{
			name:         "returns default for invalid prefixed value, falls back to unprefixed",
			key:          "TEST_FLOAT",
			defaultValue: 0.0,
			envVars: map[string]string{
				"TEST_FLOAT":              "0.5",
				"GOBUILDCACHE_TEST_FLOAT": "not-a-number",
			},
			expected: 0.5,
		},
		{
			name:         "empty prefixed value falls through to unprefixed",
			key:          "TEST_FLOAT",
			defaultValue: 0.0,
			envVars: map[string]string{
				"TEST_FLOAT":              "0.75",
				"GOBUILDCACHE_TEST_FLOAT": "",
			},
			expected: 0.75,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Clear environment variables (t.Setenv auto-restores on test completion)
			t.Setenv(tt.key, "")
			t.Setenv("GOBUILDCACHE_"+tt.key, "")
			// Set test-specific environment variables
			for k, v := range tt.envVars {
				t.Setenv(k, v)
			}

			result := getEnvFloatWithPrefix(tt.key, tt.defaultValue)
			if result != tt.expected {
				t.Errorf("getEnvFloatWithPrefix(%q, %v) = %v, want %v", tt.key, tt.defaultValue, result, tt.expected)
			}
		})
	}
}

func TestResolveS3Config(t *testing.T) {
	// Helper to clear all AWS env vars for a test.
	clearAWSEnv := func(t *testing.T) {
		t.Helper()
		for _, key := range []string{
			"AWS_REGION", "GOBUILDCACHE_AWS_REGION",
			"AWS_ACCESS_KEY_ID", "GOBUILDCACHE_AWS_ACCESS_KEY_ID",
			"AWS_SECRET_ACCESS_KEY", "GOBUILDCACHE_AWS_SECRET_ACCESS_KEY",
			"AWS_SESSION_TOKEN", "GOBUILDCACHE_AWS_SESSION_TOKEN",
		} {
			t.Setenv(key, "")
		}
	}

	t.Run("returns empty config when no env vars set", func(t *testing.T) {
		clearAWSEnv(t)
		cfg, err := resolveS3Config()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if cfg.Region != "" || cfg.AccessKeyID != "" || cfg.SecretAccessKey != "" || cfg.SessionToken != "" {
			t.Errorf("expected empty config, got %+v", cfg)
		}
	})

	t.Run("prefixed env vars take precedence", func(t *testing.T) {
		clearAWSEnv(t)
		t.Setenv("AWS_REGION", "us-east-1")
		t.Setenv("GOBUILDCACHE_AWS_REGION", "us-west-2")
		t.Setenv("AWS_ACCESS_KEY_ID", "old-key")
		t.Setenv("GOBUILDCACHE_AWS_ACCESS_KEY_ID", "new-key")
		t.Setenv("AWS_SECRET_ACCESS_KEY", "old-secret")
		t.Setenv("GOBUILDCACHE_AWS_SECRET_ACCESS_KEY", "new-secret")
		t.Setenv("AWS_SESSION_TOKEN", "old-token")
		t.Setenv("GOBUILDCACHE_AWS_SESSION_TOKEN", "new-token")

		cfg, err := resolveS3Config()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if cfg.Region != "us-west-2" {
			t.Errorf("Region = %q, want %q", cfg.Region, "us-west-2")
		}
		if cfg.AccessKeyID != "new-key" {
			t.Errorf("AccessKeyID = %q, want %q", cfg.AccessKeyID, "new-key")
		}
		if cfg.SecretAccessKey != "new-secret" {
			t.Errorf("SecretAccessKey = %q, want %q", cfg.SecretAccessKey, "new-secret")
		}
		if cfg.SessionToken != "new-token" {
			t.Errorf("SessionToken = %q, want %q", cfg.SessionToken, "new-token")
		}
	})

	t.Run("falls back to unprefixed env vars", func(t *testing.T) {
		clearAWSEnv(t)
		t.Setenv("AWS_REGION", "eu-west-1")
		t.Setenv("AWS_ACCESS_KEY_ID", "fallback-key")
		t.Setenv("AWS_SECRET_ACCESS_KEY", "fallback-secret")

		cfg, err := resolveS3Config()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if cfg.Region != "eu-west-1" {
			t.Errorf("Region = %q, want %q", cfg.Region, "eu-west-1")
		}
		if cfg.AccessKeyID != "fallback-key" {
			t.Errorf("AccessKeyID = %q, want %q", cfg.AccessKeyID, "fallback-key")
		}
	})

	t.Run("errors when only access key is set", func(t *testing.T) {
		clearAWSEnv(t)
		t.Setenv("GOBUILDCACHE_AWS_ACCESS_KEY_ID", "some-key")

		_, err := resolveS3Config()
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if !strings.Contains(err.Error(), "AWS_ACCESS_KEY_ID") {
			t.Errorf("error should mention AWS_ACCESS_KEY_ID, got: %v", err)
		}
	})

	t.Run("errors when only secret key is set", func(t *testing.T) {
		clearAWSEnv(t)
		t.Setenv("GOBUILDCACHE_AWS_SECRET_ACCESS_KEY", "some-secret")

		_, err := resolveS3Config()
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if !strings.Contains(err.Error(), "AWS_SECRET_ACCESS_KEY") {
			t.Errorf("error should mention AWS_SECRET_ACCESS_KEY, got: %v", err)
		}
	})

	t.Run("session token is optional with full credentials", func(t *testing.T) {
		clearAWSEnv(t)
		t.Setenv("GOBUILDCACHE_AWS_ACCESS_KEY_ID", "key")
		t.Setenv("GOBUILDCACHE_AWS_SECRET_ACCESS_KEY", "secret")

		cfg, err := resolveS3Config()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if cfg.SessionToken != "" {
			t.Errorf("SessionToken = %q, want empty", cfg.SessionToken)
		}
	})
}
