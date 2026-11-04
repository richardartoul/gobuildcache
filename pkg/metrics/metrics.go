package metrics

import (
	"fmt"
	"sync"
	"time"

	"github.com/DataDog/sketches-go/ddsketch"
)

// LatencyTracker tracks latency quantiles using DDSketch.
type LatencyTracker struct {
	mu               sync.Mutex
	sketches         map[string]*ddsketch.DDSketch
	relativeAccuracy float64
}

// NewLatencyTracker creates a new latency tracker with DDSketch.
// relativeAccuracy determines the accuracy of quantile estimates (e.g., 0.01 = 1% accuracy)
func NewLatencyTracker(relativeAccuracy float64) *LatencyTracker {
	return &LatencyTracker{
		sketches:         make(map[string]*ddsketch.DDSketch),
		relativeAccuracy: relativeAccuracy,
	}
}

// Record records a duration for the given operation.
func (lt *LatencyTracker) Record(operation string, duration time.Duration) {
	lt.mu.Lock()
	defer lt.mu.Unlock()

	sketch, exists := lt.sketches[operation]
	if !exists {
		// Create a new sketch with the configured relative accuracy
		var err error
		sketch, err = ddsketch.LogUnboundedDenseDDSketch(lt.relativeAccuracy)
		if err != nil {
			// Fallback to default sketch if there's an error
			sketch, _ = ddsketch.NewDefaultDDSketch(lt.relativeAccuracy)
		}
		lt.sketches[operation] = sketch
	}

	// Record duration in milliseconds
	sketch.Add(float64(duration.Microseconds()) / 1000.0)
}

// RecordFunc wraps a function and records its execution time.
func (lt *LatencyTracker) RecordFunc(operation string, fn func() error) error {
	start := time.Now()
	err := fn()
	lt.Record(operation, time.Since(start))
	return err
}

// RecordFuncWithResult wraps a function that returns a result and records its execution time.
func (lt *LatencyTracker) RecordFuncWithResult(operation string, fn func() (interface{}, error)) (interface{}, error) {
	start := time.Now()
	result, err := fn()
	lt.Record(operation, time.Since(start))
	return result, err
}

// GetQuantile returns the value at the given quantile for the operation.
// quantile should be between 0 and 1 (e.g., 0.5 for median, 0.99 for p99).
func (lt *LatencyTracker) GetQuantile(operation string, quantile float64) (float64, error) {
	lt.mu.Lock()
	defer lt.mu.Unlock()

	sketch, exists := lt.sketches[operation]
	if !exists {
		return 0, fmt.Errorf("no data for operation: %s", operation)
	}

	return sketch.GetValueAtQuantile(quantile)
}

// GetStats returns common statistics for an operation.
type Stats struct {
	Operation string
	Count     int64
	Sum       float64
	Min       float64
	P50       float64
	P90       float64
	P95       float64
	P99       float64
	Max       float64
}

// GetStats returns statistics for the given operation.
func (lt *LatencyTracker) GetStats(operation string) (Stats, error) {
	lt.mu.Lock()
	defer lt.mu.Unlock()

	sketch, exists := lt.sketches[operation]
	if !exists {
		return Stats{}, fmt.Errorf("no data for operation: %s", operation)
	}

	count := sketch.GetCount()
	if count == 0 {
		return Stats{Operation: operation}, nil
	}

	min, _ := sketch.GetMinValue()
	p50, _ := sketch.GetValueAtQuantile(0.50)
	p90, _ := sketch.GetValueAtQuantile(0.90)
	p95, _ := sketch.GetValueAtQuantile(0.95)
	p99, _ := sketch.GetValueAtQuantile(0.99)
	max, _ := sketch.GetMaxValue()

	return Stats{
		Operation: operation,
		Count:     int64(count),
		Sum:       sketch.GetSum(),
		Min:       min,
		P50:       p50,
		P90:       p90,
		P95:       p95,
		P99:       p99,
		Max:       max,
	}, nil
}

// GetAllStats returns statistics for all tracked operations.
func (lt *LatencyTracker) GetAllStats() []Stats {
	lt.mu.Lock()
	defer lt.mu.Unlock()

	stats := make([]Stats, 0, len(lt.sketches))
	for operation := range lt.sketches {
		lt.mu.Unlock() // Unlock before calling GetStats which locks again
		stat, err := lt.GetStats(operation)
		lt.mu.Lock()
		if err == nil {
			stats = append(stats, stat)
		}
	}

	return stats
}

// FormatStats returns a human-readable string of the statistics.
func (s Stats) String() string {
	if s.Count == 0 {
		return fmt.Sprintf("  %s: no data", s.Operation)
	}
	return fmt.Sprintf("  %s (n=%d): min=%.2fms p50=%.2fms p90=%.2fms p95=%.2fms p99=%.2fms max=%.2fms",
		s.Operation, s.Count, s.Min, s.P50, s.P90, s.P95, s.P99, s.Max)
}
