package main

import (
	"testing"
)

func TestFormatBytes(t *testing.T) {
	tests := []struct {
		bytes    int64
		expected string
	}{
		{0, "0 B"},
		{1, "1 B"},
		{1023, "1023 B"},
		{1024, "1.00 KB"},
		{1536, "1.50 KB"},
		{1024 * 1024, "1.00 MB"},
		{1536 * 1024, "1.50 MB"},
		{1024 * 1024 * 1024, "1.00 GB"},
		{1024*1024*1024*2 + 1024*1024*512, "2.50 GB"},
		{1024 * 1024 * 1024 * 1024, "1.00 TB"},
		{1024*1024*1024*1024*3 + 1024*1024*1024*716, "3.70 TB"},
	}

	for _, tt := range tests {
		result := formatBytes(tt.bytes)
		if result != tt.expected {
			t.Errorf("formatBytes(%d) = %s, expected %s", tt.bytes, result, tt.expected)
		}
	}
}
