/*
Package dmt implements performance regression tracking for the distributed radix tree system.
This tracks performance over time and detects when performance degrades significantly.
*/
package dmt

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

// RegressionTracker tracks performance regressions over time
type RegressionTracker struct {
	dataDir    string
	baselines  map[string][]PerformanceSample
	thresholds RegressionThresholds
	mu         sync.RWMutex
}

// PerformanceSample represents a single performance measurement
type PerformanceSample struct {
	Timestamp   time.Time              `json:"timestamp"`
	Operation   string                 `json:"operation"`
	Duration    time.Duration          `json:"duration"`
	Throughput  float64                `json:"throughput"`
	MemoryUsage uint64                 `json:"memory_usage"`
	CPUUsage    float64                `json:"cpu_usage"`
	Metadata    map[string]interface{} `json:"metadata"`
}

// RegressionThresholds defines thresholds for regression detection
type RegressionThresholds struct {
	LatencyIncrease    float64 // Percentage increase in latency to consider regression
	ThroughputDecrease float64 // Percentage decrease in throughput to consider regression
	MemoryIncrease     float64 // Percentage increase in memory to consider regression
	MinSamples         int     // Minimum samples needed for regression detection
	WindowSize         int     // Number of recent samples to compare against baseline
}

// RegressionAlert represents a detected regression
type RegressionAlert struct {
	Operation     string                 `json:"operation"`
	DetectedAt    time.Time              `json:"detected_at"`
	Severity      string                 `json:"severity"` // "minor", "major", "critical"
	Type          string                 `json:"type"`     // "latency", "throughput", "memory"
	BaselineValue float64                `json:"baseline_value"`
	CurrentValue  float64                `json:"current_value"`
	Change        float64                `json:"change"` // Percentage change
	Description   string                 `json:"description"`
	Metadata      map[string]interface{} `json:"metadata"`
}

// NewRegressionTracker creates a new regression tracker
func NewRegressionTracker(dataDir string) *RegressionTracker {
	if dataDir == "" {
		dataDir = "performance_data"
	}

	// Ensure data directory exists
	os.MkdirAll(dataDir, 0755)

	tracker := &RegressionTracker{
		dataDir:   dataDir,
		baselines: make(map[string][]PerformanceSample),
		thresholds: RegressionThresholds{
			LatencyIncrease:    20.0, // 20% increase
			ThroughputDecrease: 15.0, // 15% decrease
			MemoryIncrease:     25.0, // 25% increase
			MinSamples:         10,
			WindowSize:         5,
		},
	}

	// Load existing data
	tracker.loadBaselines()

	return tracker
}

// RecordSample records a new performance sample
func (rt *RegressionTracker) RecordSample(sample PerformanceSample) error {
	rt.mu.Lock()
	defer rt.mu.Unlock()

	if rt.baselines[sample.Operation] == nil {
		rt.baselines[sample.Operation] = make([]PerformanceSample, 0)
	}

	rt.baselines[sample.Operation] = append(rt.baselines[sample.Operation], sample)

	// Keep only the last 1000 samples per operation to prevent unbounded growth
	if len(rt.baselines[sample.Operation]) > 1000 {
		rt.baselines[sample.Operation] = rt.baselines[sample.Operation][len(rt.baselines[sample.Operation])-1000:]
	}

	// Persist the sample
	return rt.persistSample(sample)
}

// DetectRegressions analyzes recent performance and detects regressions
func (rt *RegressionTracker) DetectRegressions() ([]RegressionAlert, error) {
	rt.mu.RLock()
	defer rt.mu.RUnlock()

	var alerts []RegressionAlert

	for operation, samples := range rt.baselines {
		if len(samples) < rt.thresholds.MinSamples {
			continue
		}

		// Sort samples by timestamp
		sort.Slice(samples, func(i, j int) bool {
			return samples[i].Timestamp.Before(samples[j].Timestamp)
		})

		// Get baseline (older samples) and recent samples
		totalSamples := len(samples)
		baselineEnd := totalSamples - rt.thresholds.WindowSize*2
		recentStart := totalSamples - rt.thresholds.WindowSize

		if baselineEnd <= 0 || recentStart <= baselineEnd {
			continue
		}

		baselineSamples := samples[:baselineEnd]
		recentSamples := samples[recentStart:]

		// Calculate baseline metrics
		baselineLatency := calculateAverageLatency(baselineSamples)
		baselineThroughput := calculateAverageThroughput(baselineSamples)
		baselineMemory := calculateAverageMemory(baselineSamples)

		// Calculate recent metrics
		recentLatency := calculateAverageLatency(recentSamples)
		recentThroughput := calculateAverageThroughput(recentSamples)
		recentMemory := calculateAverageMemory(recentSamples)

		// Check for latency regression
		if baselineLatency > 0 {
			latencyChange := ((float64(recentLatency) - float64(baselineLatency)) / float64(baselineLatency)) * 100
			if latencyChange > rt.thresholds.LatencyIncrease {
				severity := rt.calculateSeverity(latencyChange, rt.thresholds.LatencyIncrease)
				alerts = append(alerts, RegressionAlert{
					Operation:     operation,
					DetectedAt:    time.Now(),
					Severity:      severity,
					Type:          "latency",
					BaselineValue: float64(baselineLatency),
					CurrentValue:  float64(recentLatency),
					Change:        latencyChange,
					Description:   fmt.Sprintf("Latency increased by %.1f%% for operation %s", latencyChange, operation),
				})
			}
		}

		// Check for throughput regression
		if baselineThroughput > 0 {
			throughputChange := ((baselineThroughput - recentThroughput) / baselineThroughput) * 100
			if throughputChange > rt.thresholds.ThroughputDecrease {
				severity := rt.calculateSeverity(throughputChange, rt.thresholds.ThroughputDecrease)
				alerts = append(alerts, RegressionAlert{
					Operation:     operation,
					DetectedAt:    time.Now(),
					Severity:      severity,
					Type:          "throughput",
					BaselineValue: baselineThroughput,
					CurrentValue:  recentThroughput,
					Change:        throughputChange,
					Description:   fmt.Sprintf("Throughput decreased by %.1f%% for operation %s", throughputChange, operation),
				})
			}
		}

		// Check for memory regression
		if baselineMemory > 0 {
			memoryChange := ((float64(recentMemory) - float64(baselineMemory)) / float64(baselineMemory)) * 100
			if memoryChange > rt.thresholds.MemoryIncrease {
				severity := rt.calculateSeverity(memoryChange, rt.thresholds.MemoryIncrease)
				alerts = append(alerts, RegressionAlert{
					Operation:     operation,
					DetectedAt:    time.Now(),
					Severity:      severity,
					Type:          "memory",
					BaselineValue: float64(baselineMemory),
					CurrentValue:  float64(recentMemory),
					Change:        memoryChange,
					Description:   fmt.Sprintf("Memory usage increased by %.1f%% for operation %s", memoryChange, operation),
				})
			}
		}
	}

	return alerts, nil
}

// calculateSeverity determines the severity of a regression based on the change percentage
func (rt *RegressionTracker) calculateSeverity(change, threshold float64) string {
	if change > threshold*3 {
		return "critical"
	} else if change > threshold*2 {
		return "major"
	} else {
		return "minor"
	}
}

// GetPerformanceHistory returns performance history for an operation
func (rt *RegressionTracker) GetPerformanceHistory(operation string, since time.Time) []PerformanceSample {
	rt.mu.RLock()
	defer rt.mu.RUnlock()

	samples := rt.baselines[operation]
	if samples == nil {
		return nil
	}

	var filtered []PerformanceSample
	for _, sample := range samples {
		if sample.Timestamp.After(since) {
			filtered = append(filtered, sample)
		}
	}

	return filtered
}

// GetOperationStats returns statistics for an operation
func (rt *RegressionTracker) GetOperationStats(operation string) OperationStats {
	rt.mu.RLock()
	defer rt.mu.RUnlock()

	samples := rt.baselines[operation]
	if len(samples) == 0 {
		return OperationStats{}
	}

	// Sort by timestamp
	sort.Slice(samples, func(i, j int) bool {
		return samples[i].Timestamp.Before(samples[j].Timestamp)
	})

	stats := OperationStats{
		Operation:   operation,
		SampleCount: len(samples),
		FirstSample: samples[0].Timestamp,
		LastSample:  samples[len(samples)-1].Timestamp,
	}

	// Calculate statistics
	var durations []time.Duration
	var throughputs []float64
	var memoryUsages []uint64

	for _, sample := range samples {
		durations = append(durations, sample.Duration)
		throughputs = append(throughputs, sample.Throughput)
		memoryUsages = append(memoryUsages, sample.MemoryUsage)
	}

	stats.LatencyStats = calculateLatencyStats(durations)
	stats.ThroughputStats = calculateThroughputStats(throughputs)
	stats.MemoryStats = calculateMemoryStats(memoryUsages)

	return stats
}

// OperationStats contains statistical information about an operation
type OperationStats struct {
	Operation       string          `json:"operation"`
	SampleCount     int             `json:"sample_count"`
	FirstSample     time.Time       `json:"first_sample"`
	LastSample      time.Time       `json:"last_sample"`
	LatencyStats    LatencyStats    `json:"latency_stats"`
	ThroughputStats ThroughputStats `json:"throughput_stats"`
	MemoryStats     MemoryStats     `json:"memory_stats"`
}

// LatencyStats contains latency statistics
type LatencyStats struct {
	Min    time.Duration `json:"min"`
	Max    time.Duration `json:"max"`
	Mean   time.Duration `json:"mean"`
	Median time.Duration `json:"median"`
	P95    time.Duration `json:"p95"`
	P99    time.Duration `json:"p99"`
}

// ThroughputStats contains throughput statistics
type ThroughputStats struct {
	Min    float64 `json:"min"`
	Max    float64 `json:"max"`
	Mean   float64 `json:"mean"`
	Median float64 `json:"median"`
}

// MemoryStats contains memory usage statistics
type MemoryStats struct {
	Min    uint64 `json:"min"`
	Max    uint64 `json:"max"`
	Mean   uint64 `json:"mean"`
	Median uint64 `json:"median"`
}

// persistSample saves a sample to disk
func (rt *RegressionTracker) persistSample(sample PerformanceSample) error {
	filename := filepath.Join(rt.dataDir, fmt.Sprintf("%s.json", sample.Operation))

	// Read existing samples
	var samples []PerformanceSample
	if data, err := os.ReadFile(filename); err == nil {
		json.Unmarshal(data, &samples)
	}

	// Append new sample
	samples = append(samples, sample)

	// Keep only the last 1000 samples
	if len(samples) > 1000 {
		samples = samples[len(samples)-1000:]
	}

	// Write back to file
	data, err := json.MarshalIndent(samples, "", "  ")
	if err != nil {
		return err
	}

	return os.WriteFile(filename, data, 0644)
}

// loadBaselines loads existing performance data from disk
func (rt *RegressionTracker) loadBaselines() error {
	files, err := filepath.Glob(filepath.Join(rt.dataDir, "*.json"))
	if err != nil {
		return err
	}

	for _, file := range files {
		operation := filepath.Base(file)
		operation = operation[:len(operation)-5] // Remove .json extension

		data, err := os.ReadFile(file)
		if err != nil {
			continue
		}

		var samples []PerformanceSample
		if err := json.Unmarshal(data, &samples); err != nil {
			continue
		}

		rt.baselines[operation] = samples
	}

	return nil
}

// Helper functions for calculating statistics

func calculateAverageLatency(samples []PerformanceSample) time.Duration {
	if len(samples) == 0 {
		return 0
	}
	var sum time.Duration
	for _, sample := range samples {
		sum += sample.Duration
	}
	return sum / time.Duration(len(samples))
}

func calculateAverageThroughput(samples []PerformanceSample) float64 {
	if len(samples) == 0 {
		return 0
	}
	var sum float64
	for _, sample := range samples {
		sum += sample.Throughput
	}
	return sum / float64(len(samples))
}

func calculateAverageMemory(samples []PerformanceSample) uint64 {
	if len(samples) == 0 {
		return 0
	}
	var sum uint64
	for _, sample := range samples {
		sum += sample.MemoryUsage
	}
	return sum / uint64(len(samples))
}

func calculateLatencyStats(durations []time.Duration) LatencyStats {
	if len(durations) == 0 {
		return LatencyStats{}
	}

	sort.Slice(durations, func(i, j int) bool {
		return durations[i] < durations[j]
	})

	var sum time.Duration
	for _, d := range durations {
		sum += d
	}

	stats := LatencyStats{
		Min:    durations[0],
		Max:    durations[len(durations)-1],
		Mean:   sum / time.Duration(len(durations)),
		Median: durations[len(durations)/2],
	}

	// Calculate percentiles
	if len(durations) > 0 {
		p95Index := int(float64(len(durations)) * 0.95)
		p99Index := int(float64(len(durations)) * 0.99)
		if p95Index >= len(durations) {
			p95Index = len(durations) - 1
		}
		if p99Index >= len(durations) {
			p99Index = len(durations) - 1
		}
		stats.P95 = durations[p95Index]
		stats.P99 = durations[p99Index]
	}

	return stats
}

func calculateThroughputStats(throughputs []float64) ThroughputStats {
	if len(throughputs) == 0 {
		return ThroughputStats{}
	}

	sort.Float64s(throughputs)

	var sum float64
	for _, t := range throughputs {
		sum += t
	}

	return ThroughputStats{
		Min:    throughputs[0],
		Max:    throughputs[len(throughputs)-1],
		Mean:   sum / float64(len(throughputs)),
		Median: throughputs[len(throughputs)/2],
	}
}

func calculateMemoryStats(memoryUsages []uint64) MemoryStats {
	if len(memoryUsages) == 0 {
		return MemoryStats{}
	}

	sort.Slice(memoryUsages, func(i, j int) bool {
		return memoryUsages[i] < memoryUsages[j]
	})

	var sum uint64
	for _, m := range memoryUsages {
		sum += m
	}

	return MemoryStats{
		Min:    memoryUsages[0],
		Max:    memoryUsages[len(memoryUsages)-1],
		Mean:   sum / uint64(len(memoryUsages)),
		Median: memoryUsages[len(memoryUsages)/2],
	}
}
