/*
Package dmt implements comprehensive performance profiling and optimization tools
for the distributed radix tree system. This includes CPU profiling, memory profiling,
bottleneck identification, and performance regression tracking.
*/
package dmt

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"runtime/pprof"
	"sync"
	"time"
)

// Profiler defines the interface for performance profiling
type Profiler interface {
	StartProfiling(ctx context.Context) error
	StopProfiling() (*ProfileResult, error)
	RecordOperation(operation string, duration time.Duration)
	ProfileOperation(ctx context.Context, operation string, fn func() error) error
	GetOperationStats(operation string) (min, max, avg time.Duration, count int)
	IsActive() bool
	Reset()
}

// Profiling-specific errors
var (
	ErrProfilingActive   = errors.New("profiling already active")
	ErrProfilingInactive = errors.New("profiling not active")
	ErrInvalidConfig     = errors.New("invalid profiling configuration")
	ErrProfileGeneration = errors.New("profile generation failed")
)

// ProfileConfig configures profiling behavior
type ProfileConfig struct {
	CPUProfile   bool          // Enable CPU profiling
	MemProfile   bool          // Enable memory profiling
	BlockProfile bool          // Enable block profiling
	MutexProfile bool          // Enable mutex profiling
	TraceProfile bool          // Enable execution tracing
	ProfileDir   string        // Directory to store profile files
	Duration     time.Duration // Profiling duration
	SampleRate   int           // Sampling rate for profiles

	// Advanced configuration
	MaxSamples          int           // Maximum samples per operation (default: 1000)
	BottleneckThreshold time.Duration // Threshold for bottleneck detection (default: 50ms)
	RegressionThreshold float64       // Regression detection threshold (default: 20%)
}

// PerformanceProfiler manages profiling operations
type PerformanceProfiler struct {
	config    ProfileConfig
	cpuFile   *os.File
	traceFile *os.File
	active    bool
	startTime time.Time
	mu        sync.Mutex

	// Performance tracking
	operationTimes map[string][]time.Duration
	bottlenecks    map[string]BottleneckInfo
	regressions    []RegressionInfo

	// Resource management
	profileFiles map[string]*os.File // Track all open profile files
}

// BottleneckInfo tracks performance bottleneck information
type BottleneckInfo struct {
	Operation    string
	AverageTime  time.Duration
	MaxTime      time.Duration
	Count        int64
	LastOccurred time.Time
	Severity     string // "low", "medium", "high", "critical"
}

// RegressionInfo tracks performance regression information
type RegressionInfo struct {
	Operation    string
	BaselineTime time.Duration
	CurrentTime  time.Duration
	Regression   float64 // Percentage regression
	DetectedAt   time.Time
	Severity     string
}

// ProfileResult contains profiling results and analysis
type ProfileResult struct {
	Duration         time.Duration
	CPUProfilePath   string
	MemProfilePath   string
	BlockProfilePath string
	MutexProfilePath string
	TraceProfilePath string
	Bottlenecks      []BottleneckInfo
	Regressions      []RegressionInfo
	Recommendations  []string
}

// NewPerformanceProfiler creates a new performance profiler
func NewPerformanceProfiler(config ProfileConfig) (*PerformanceProfiler, error) {
	if config.ProfileDir == "" {
		config.ProfileDir = "profiles"
	}
	if config.Duration == 0 {
		config.Duration = 30 * time.Second
	}
	if config.SampleRate == 0 {
		config.SampleRate = 100
	}

	// Ensure profile directory exists
	if err := os.MkdirAll(config.ProfileDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create profile directory: %w", err)
	}

	return &PerformanceProfiler{
		config:         config,
		operationTimes: make(map[string][]time.Duration),
		bottlenecks:    make(map[string]BottleneckInfo),
		profileFiles:   make(map[string]*os.File),
	}, nil
}

// StartProfiling begins profiling with the configured options
func (pp *PerformanceProfiler) StartProfiling(ctx context.Context) error {
	pp.mu.Lock()
	defer pp.mu.Unlock()

	if pp.active {
		return ErrProfilingActive
	}

	pp.startTime = time.Now()
	timestamp := pp.startTime.Format("20060102-150405")

	var err error

	// Start CPU profiling
	if pp.config.CPUProfile {
		cpuPath := filepath.Join(pp.config.ProfileDir, fmt.Sprintf("cpu-%s.prof", timestamp))
		pp.cpuFile, err = os.Create(cpuPath)
		if err != nil {
			return fmt.Errorf("failed to create CPU profile: %v", err)
		}
		// Stop any existing CPU profiling before starting new one
		pprof.StopCPUProfile()

		if err := pprof.StartCPUProfile(pp.cpuFile); err != nil {
			pp.cpuFile.Close()
			return fmt.Errorf("failed to start CPU profile: %v", err)
		}
	}

	// Configure block profiling
	if pp.config.BlockProfile {
		runtime.SetBlockProfileRate(pp.config.SampleRate)
	}

	// Configure mutex profiling
	if pp.config.MutexProfile {
		runtime.SetMutexProfileFraction(pp.config.SampleRate)
	}

	// Start execution tracing
	if pp.config.TraceProfile {
		tracePath := filepath.Join(pp.config.ProfileDir, fmt.Sprintf("trace-%s.out", timestamp))
		pp.traceFile, err = os.Create(tracePath)
		if err != nil {
			return fmt.Errorf("failed to create trace file: %v", err)
		}
		// Note: execution tracing would require additional setup
	}

	pp.active = true
	return nil
}

// StopProfiling stops profiling and generates profile files
func (pp *PerformanceProfiler) StopProfiling() (*ProfileResult, error) {
	pp.mu.Lock()
	defer pp.mu.Unlock()

	if !pp.active {
		return nil, ErrProfilingInactive
	}

	duration := time.Since(pp.startTime)
	timestamp := pp.startTime.Format("20060102-150405")

	result := &ProfileResult{Duration: duration}

	// Stop active profiling and generate profile files
	if err := pp.stopActiveProfiles(result, timestamp); err != nil {
		return nil, fmt.Errorf("failed to stop profiles: %w", err)
	}

	// Perform analysis
	pp.performAnalysis(result)

	// Ensure profiling instrumentation is disabled after analysis
	ensureProfilingDisabled()

	pp.active = false
	return result, nil
}

// stopActiveProfiles stops all active profiling and generates files
func (pp *PerformanceProfiler) stopActiveProfiles(result *ProfileResult, timestamp string) error {
	var errors []error

	// Stop CPU profiling
	if pp.config.CPUProfile && pp.cpuFile != nil {
		pprof.StopCPUProfile()
		pp.cpuFile.Close()
		result.CPUProfilePath = pp.cpuFile.Name()
	}

	// Generate memory profile
	if pp.config.MemProfile {
		if path, err := pp.generateMemoryProfile(timestamp); err != nil {
			errors = append(errors, err)
		} else {
			result.MemProfilePath = path
		}
	}

	// Generate block profile
	if pp.config.BlockProfile {
		if path, err := pp.generateBlockProfile(timestamp); err != nil {
			errors = append(errors, err)
		} else {
			result.BlockProfilePath = path
		}
	}

	// Generate mutex profile
	if pp.config.MutexProfile {
		if path, err := pp.generateMutexProfile(timestamp); err != nil {
			errors = append(errors, err)
		} else {
			result.MutexProfilePath = path
		}
	}

	// Stop tracing
	if pp.config.TraceProfile && pp.traceFile != nil {
		pp.traceFile.Close()
		result.TraceProfilePath = pp.traceFile.Name()
	}

	if len(errors) > 0 {
		return fmt.Errorf("profile generation errors: %v", errors)
	}
	return nil
}

// performAnalysis performs bottleneck and regression analysis
func (pp *PerformanceProfiler) performAnalysis(result *ProfileResult) {
	result.Bottlenecks = pp.analyzeBottlenecks()
	result.Regressions = pp.detectRegressions()
	result.Recommendations = pp.generateRecommendations(result)
}

// ensureProfilingDisabled resets global profiling rates to defaults
func ensureProfilingDisabled() {
	// Reset block/mutex profiling to avoid overhead after profiling stops
	runtime.SetBlockProfileRate(0)
	runtime.SetMutexProfileFraction(0)
}

const (
	maxOperationSamples = 1000
	ringBufferSize      = 100
)

// RecordOperation records timing for a specific operation
func (pp *PerformanceProfiler) RecordOperation(operation string, duration time.Duration) {
	pp.mu.Lock()
	defer pp.mu.Unlock()

	if pp.operationTimes[operation] == nil {
		pp.operationTimes[operation] = make([]time.Duration, 0, maxOperationSamples)
	}

	times := pp.operationTimes[operation]

	// Use ring buffer approach for better performance
	if len(times) >= maxOperationSamples {
		// Shift elements efficiently by copying in chunks
		copy(times, times[ringBufferSize:])
		times = times[:len(times)-ringBufferSize]
	}

	pp.operationTimes[operation] = append(times, duration)

	// Update bottleneck information
	pp.updateBottleneckInfo(operation, duration)
}

// Severity thresholds for better maintainability
var severityThresholds = struct {
	critical time.Duration
	high     time.Duration
	medium   time.Duration
}{
	critical: 1 * time.Second,
	high:     500 * time.Millisecond,
	medium:   100 * time.Millisecond,
}

// updateBottleneckInfo updates bottleneck tracking for an operation
func (pp *PerformanceProfiler) updateBottleneckInfo(operation string, duration time.Duration) {
	info := pp.bottlenecks[operation]
	info.Operation = operation
	info.Count++
	info.LastOccurred = time.Now()

	if duration > info.MaxTime {
		info.MaxTime = duration
	}

	// Calculate running average using exponential moving average
	const alpha = 0.1
	if info.Count == 1 {
		info.AverageTime = duration
	} else {
		info.AverageTime = time.Duration(float64(info.AverageTime)*(1-alpha) + float64(duration)*alpha)
	}

	// Determine severity based on duration using lookup table
	info.Severity = pp.calculateSeverity(duration)
	pp.bottlenecks[operation] = info
}

// calculateSeverity determines severity level based on duration
func (pp *PerformanceProfiler) calculateSeverity(duration time.Duration) string {
	switch {
	case duration > severityThresholds.critical:
		return "critical"
	case duration > severityThresholds.high:
		return "high"
	case duration > severityThresholds.medium:
		return "medium"
	default:
		return "low"
	}
}

// analyzeBottlenecks identifies performance bottlenecks
func (pp *PerformanceProfiler) analyzeBottlenecks() []BottleneckInfo {
	var bottlenecks []BottleneckInfo

	for _, info := range pp.bottlenecks {
		// Consider it a bottleneck if average time > 50ms or max time > 200ms
		if info.AverageTime > 50*time.Millisecond || info.MaxTime > 200*time.Millisecond {
			bottlenecks = append(bottlenecks, info)
		}
	}

	return bottlenecks
}

// detectRegressions detects performance regressions
func (pp *PerformanceProfiler) detectRegressions() []RegressionInfo {
	var regressions []RegressionInfo

	// This is a simplified regression detection
	// In a real system, you'd compare against historical baselines
	for operation, times := range pp.operationTimes {
		if len(times) < 10 {
			continue
		}

		// Compare recent performance (last 25%) with earlier performance (first 25%)
		recentStart := len(times) * 3 / 4
		earlyEnd := len(times) / 4

		if recentStart >= len(times) || earlyEnd <= 0 {
			continue
		}

		// Calculate averages
		var earlySum, recentSum time.Duration
		earlyCount := earlyEnd
		recentCount := len(times) - recentStart

		for i := 0; i < earlyEnd; i++ {
			earlySum += times[i]
		}
		for i := recentStart; i < len(times); i++ {
			recentSum += times[i]
		}

		earlyAvg := earlySum / time.Duration(earlyCount)
		recentAvg := recentSum / time.Duration(recentCount)

		// Check for regression (>20% slower)
		if recentAvg > earlyAvg {
			regression := float64(recentAvg-earlyAvg) / float64(earlyAvg) * 100
			if regression > 20 {
				severity := "medium"
				if regression > 50 {
					severity = "high"
				}
				if regression > 100 {
					severity = "critical"
				}

				regressions = append(regressions, RegressionInfo{
					Operation:    operation,
					BaselineTime: earlyAvg,
					CurrentTime:  recentAvg,
					Regression:   regression,
					DetectedAt:   time.Now(),
					Severity:     severity,
				})
			}
		}
	}

	return regressions
}

// generateRecommendations generates optimization recommendations
func (pp *PerformanceProfiler) generateRecommendations(result *ProfileResult) []string {
	var recommendations []string

	// Analyze bottlenecks
	for _, bottleneck := range result.Bottlenecks {
		switch bottleneck.Operation {
		case "merkle_rebuild":
			if bottleneck.AverageTime > 100*time.Millisecond {
				recommendations = append(recommendations,
					"Consider implementing incremental Merkle tree updates instead of full rebuilds")
			}
		case "forest_insert":
			if bottleneck.AverageTime > 50*time.Millisecond {
				recommendations = append(recommendations,
					"Forest insert operations are slow - consider optimizing tree selection algorithm")
			}
		case "persistence_write":
			if bottleneck.AverageTime > 20*time.Millisecond {
				recommendations = append(recommendations,
					"Persistence writes are slow - consider batching writes or using faster storage")
			}
		case "network_sync":
			if bottleneck.AverageTime > 200*time.Millisecond {
				recommendations = append(recommendations,
					"Network sync is slow - consider optimizing diff calculation or compression")
			}
		}
	}

	// Analyze regressions
	for _, regression := range result.Regressions {
		if regression.Severity == "critical" {
			recommendations = append(recommendations,
				fmt.Sprintf("CRITICAL: %s performance has regressed by %.1f%% - immediate investigation required",
					regression.Operation, regression.Regression))
		}
	}

	// General recommendations based on profile types
	if result.CPUProfilePath != "" {
		recommendations = append(recommendations,
			fmt.Sprintf("Analyze CPU profile with: go tool pprof %s", result.CPUProfilePath))
	}
	if result.MemProfilePath != "" {
		recommendations = append(recommendations,
			fmt.Sprintf("Analyze memory profile with: go tool pprof %s", result.MemProfilePath))
	}

	return recommendations
}

// ProfileOperation profiles a specific operation
func (pp *PerformanceProfiler) ProfileOperation(ctx context.Context, operation string, fn func() error) error {
	start := time.Now()
	defer func() {
		duration := time.Since(start)
		pp.RecordOperation(operation, duration)
	}()

	return fn()
}

// GetOperationStats returns statistics for a specific operation
func (pp *PerformanceProfiler) GetOperationStats(operation string) (min, max, avg time.Duration, count int) {
	pp.mu.Lock()
	defer pp.mu.Unlock()

	times := pp.operationTimes[operation]
	if len(times) == 0 {
		return 0, 0, 0, 0
	}

	min = times[0]
	max = times[0]
	var sum time.Duration

	for _, t := range times {
		if t < min {
			min = t
		}
		if t > max {
			max = t
		}
		sum += t
	}

	avg = sum / time.Duration(len(times))
	count = len(times)
	return min, max, avg, count
}

// Reset clears all profiling data
func (pp *PerformanceProfiler) Reset() {
	pp.mu.Lock()
	defer pp.mu.Unlock()

	pp.operationTimes = make(map[string][]time.Duration)
	pp.bottlenecks = make(map[string]BottleneckInfo)
	pp.regressions = nil
}

// IsActive returns whether profiling is currently active
func (pp *PerformanceProfiler) IsActive() bool {
	pp.mu.Lock()
	defer pp.mu.Unlock()
	return pp.active
}

// generateMemoryProfile creates a memory profile file
func (pp *PerformanceProfiler) generateMemoryProfile(timestamp string) (string, error) {
	memPath := filepath.Join(pp.config.ProfileDir, fmt.Sprintf("mem-%s.prof", timestamp))
	memFile, err := os.Create(memPath)
	if err != nil {
		return "", fmt.Errorf("failed to create memory profile: %w", err)
	}
	defer memFile.Close()

	runtime.GC() // Force GC before memory profile
	if err := pprof.WriteHeapProfile(memFile); err != nil {
		return "", fmt.Errorf("failed to write memory profile: %w", err)
	}
	return memPath, nil
}

// generateBlockProfile creates a block profile file
func (pp *PerformanceProfiler) generateBlockProfile(timestamp string) (string, error) {
	blockPath := filepath.Join(pp.config.ProfileDir, fmt.Sprintf("block-%s.prof", timestamp))
	blockFile, err := os.Create(blockPath)
	if err != nil {
		return "", fmt.Errorf("failed to create block profile: %w", err)
	}
	defer blockFile.Close()

	if err := pprof.Lookup("block").WriteTo(blockFile, 0); err != nil {
		return "", fmt.Errorf("failed to write block profile: %w", err)
	}
	return blockPath, nil
}

// generateMutexProfile creates a mutex profile file
func (pp *PerformanceProfiler) generateMutexProfile(timestamp string) (string, error) {
	mutexPath := filepath.Join(pp.config.ProfileDir, fmt.Sprintf("mutex-%s.prof", timestamp))
	mutexFile, err := os.Create(mutexPath)
	if err != nil {
		return "", fmt.Errorf("failed to create mutex profile: %w", err)
	}
	defer mutexFile.Close()

	if err := pprof.Lookup("mutex").WriteTo(mutexFile, 0); err != nil {
		return "", fmt.Errorf("failed to write mutex profile: %w", err)
	}
	return mutexPath, nil
}
