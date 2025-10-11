/*
Package dmt implements comprehensive performance analysis tools for the distributed radix tree system.
This integrates profiling, optimization, and regression tracking to provide complete performance insights.
*/
package dmt

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"time"
)

// PerformanceAnalyzer provides comprehensive performance analysis
type PerformanceAnalyzer struct {
	profiler          *PerformanceProfiler
	optimizer         *PerformanceOptimizer
	regressionTracker *RegressionTracker
	metrics           *Metrics
	config            AnalysisConfig
}

// AnalysisConfig configures performance analysis behavior
type AnalysisConfig struct {
	EnableProfiling        bool
	EnableOptimization     bool
	EnableRegression       bool
	AnalysisInterval       time.Duration
	ReportDirectory        string
	AutoApplyOptimizations bool
}

// PerformanceReport contains comprehensive performance analysis results
type PerformanceReport struct {
	GeneratedAt        time.Time                 `json:"generated_at"`
	SystemInfo         SystemInfo                `json:"system_info"`
	ProfileResults     *ProfileResult            `json:"profile_results,omitempty"`
	OptimizationReport OptimizationReport        `json:"optimization_report"`
	RegressionAlerts   []RegressionAlert         `json:"regression_alerts"`
	OperationStats     map[string]OperationStats `json:"operation_stats"`
	Recommendations    []string                  `json:"recommendations"`
	PerformanceTrends  PerformanceTrends         `json:"performance_trends"`
}

// SystemInfo contains system-level information
type SystemInfo struct {
	GoVersion      string    `json:"go_version"`
	OS             string    `json:"os"`
	Architecture   string    `json:"architecture"`
	CPUCount       int       `json:"cpu_count"`
	MemoryTotal    uint64    `json:"memory_total"`
	MemoryUsed     uint64    `json:"memory_used"`
	GoroutineCount int       `json:"goroutine_count"`
	Timestamp      time.Time `json:"timestamp"`
}

// PerformanceTrends contains performance trend analysis
type PerformanceTrends struct {
	LatencyTrend    TrendAnalysis `json:"latency_trend"`
	ThroughputTrend TrendAnalysis `json:"throughput_trend"`
	MemoryTrend     TrendAnalysis `json:"memory_trend"`
	ErrorRateTrend  TrendAnalysis `json:"error_rate_trend"`
}

// TrendAnalysis contains trend analysis for a specific metric
type TrendAnalysis struct {
	Direction  string  `json:"direction"`  // "improving", "degrading", "stable"
	Magnitude  float64 `json:"magnitude"`  // Percentage change
	Confidence string  `json:"confidence"` // "high", "medium", "low"
	DataPoints int     `json:"data_points"`
	TimeSpan   string  `json:"time_span"`
}

// NewPerformanceAnalyzer creates a new comprehensive performance analyzer
func NewPerformanceAnalyzer(config AnalysisConfig) (*PerformanceAnalyzer, error) {
	if config.ReportDirectory == "" {
		config.ReportDirectory = "performance_reports"
	}
	if config.AnalysisInterval == 0 {
		config.AnalysisInterval = 10 * time.Minute
	}

	// Ensure report directory exists
	os.MkdirAll(config.ReportDirectory, 0755)

	analyzer := &PerformanceAnalyzer{
		config:  config,
		metrics: NewMetrics(),
	}

	// Initialize components based on configuration
	if config.EnableProfiling {
		profilerConfig := ProfileConfig{
			CPUProfile:   true,
			MemProfile:   true,
			BlockProfile: true,
			MutexProfile: true,
			ProfileDir:   filepath.Join(config.ReportDirectory, "profiles"),
			Duration:     30 * time.Second,
		}
		profiler, err := NewPerformanceProfiler(profilerConfig)
		if err != nil {
			return nil, fmt.Errorf("failed to create profiler: %w", err)
		}
		analyzer.profiler = profiler
	}

	if config.EnableOptimization {
		optimizerConfig := OptimizationConfig{
			EnableAutoOptimization: config.AutoApplyOptimizations,
			OptimizationInterval:   config.AnalysisInterval,
			MemoryThreshold:        1024 * 1024 * 1024, // 1GB
			LatencyThreshold:       100 * time.Millisecond,
			CPUThreshold:           0.8,
		}
		analyzer.optimizer = NewPerformanceOptimizer(optimizerConfig, analyzer.profiler, analyzer.metrics)
	}

	if config.EnableRegression {
		regressionDir := filepath.Join(config.ReportDirectory, "regression_data")
		analyzer.regressionTracker = NewRegressionTracker(regressionDir)
	}

	return analyzer, nil
}

// StartAnalysis begins continuous performance analysis
func (pa *PerformanceAnalyzer) StartAnalysis(ctx context.Context) error {
	if pa.config.EnableProfiling && pa.profiler != nil {
		if err := pa.profiler.StartProfiling(ctx); err != nil {
			return fmt.Errorf("failed to start profiling: %v", err)
		}
	}

	// Start background analysis loop
	go pa.analysisLoop(ctx)

	return nil
}

// StopAnalysis stops performance analysis and generates final report
func (pa *PerformanceAnalyzer) StopAnalysis() (*PerformanceReport, error) {
	var profileResult *ProfileResult
	var err error

	if pa.config.EnableProfiling && pa.profiler != nil && pa.profiler.IsActive() {
		profileResult, err = pa.profiler.StopProfiling()
		if err != nil {
			return nil, fmt.Errorf("failed to stop profiling: %v", err)
		}
	}

	return pa.generateReport(profileResult)
}

// RecordOperation records performance data for an operation
func (pa *PerformanceAnalyzer) RecordOperation(operation string, duration time.Duration, metadata map[string]interface{}) {
	// Record in profiler
	if pa.profiler != nil {
		pa.profiler.RecordOperation(operation, duration)
	}

	// Record in metrics
	if pa.metrics != nil {
		switch operation {
		case "forest_insert":
			pa.metrics.RecordInsert(duration, 0)
		case "forest_get", "forest_seek":
			pa.metrics.RecordLookup(duration)
		case "network_sync":
			pa.metrics.RecordSync(duration, 0)
		}
	}

	// Record in regression tracker
	if pa.regressionTracker != nil {
		var memStats runtime.MemStats
		runtime.ReadMemStats(&memStats)

		sample := PerformanceSample{
			Timestamp:   time.Now(),
			Operation:   operation,
			Duration:    duration,
			Throughput:  1.0 / duration.Seconds(), // Simple throughput calculation
			MemoryUsage: memStats.Alloc,
			Metadata:    metadata,
		}
		pa.regressionTracker.RecordSample(sample)
	}
}

// ProfileOperation profiles a specific operation with comprehensive tracking
func (pa *PerformanceAnalyzer) ProfileOperation(ctx context.Context, operation string, fn func() error, metadata map[string]interface{}) error {
	start := time.Now()

	if pa.profiler != nil {
		// Let the profiler record the operation itself to avoid double counting
		err := pa.profiler.ProfileOperation(ctx, operation, fn)

		duration := time.Since(start)
		// Record metrics and regression tracking without touching profiler again
		if pa.metrics != nil {
			switch operation {
			case "forest_insert":
				pa.metrics.RecordInsert(duration, 0)
			case "forest_get", "forest_seek":
				pa.metrics.RecordLookup(duration)
			case "network_sync":
				pa.metrics.RecordSync(duration, 0)
			}
		}

		if pa.regressionTracker != nil {
			var memStats runtime.MemStats
			runtime.ReadMemStats(&memStats)
			sample := PerformanceSample{
				Timestamp:   time.Now(),
				Operation:   operation,
				Duration:    duration,
				Throughput:  1.0 / duration.Seconds(),
				MemoryUsage: memStats.Alloc,
				Metadata:    metadata,
			}
			pa.regressionTracker.RecordSample(sample)
		}

		return err
	}

	// If profiler is disabled, execute and record via standard path
	err := fn()
	duration := time.Since(start)
	pa.RecordOperation(operation, duration, metadata)
	return err
}

// GenerateReport generates a comprehensive performance report
func (pa *PerformanceAnalyzer) GenerateReport() (*PerformanceReport, error) {
	return pa.generateReport(nil)
}

// generateReport creates a comprehensive performance report
func (pa *PerformanceAnalyzer) generateReport(profileResult *ProfileResult) (*PerformanceReport, error) {
	report := &PerformanceReport{
		GeneratedAt:     time.Now(),
		SystemInfo:      pa.getSystemInfo(),
		ProfileResults:  profileResult,
		OperationStats:  make(map[string]OperationStats),
		Recommendations: make([]string, 0),
	}

	// Get optimization report
	if pa.optimizer != nil {
		report.OptimizationReport = pa.optimizer.GenerateOptimizationReport()
	}

	// Get regression alerts
	if pa.regressionTracker != nil {
		alerts, err := pa.regressionTracker.DetectRegressions()
		if err == nil {
			report.RegressionAlerts = alerts
		}

		// Get operation statistics
		operations := []string{"forest_insert", "forest_get", "forest_seek", "merkle_rebuild", "network_sync", "persistence_write"}
		for _, op := range operations {
			stats := pa.regressionTracker.GetOperationStats(op)
			if stats.SampleCount > 0 {
				report.OperationStats[op] = stats
			}
		}
	}

	// Analyze performance trends
	report.PerformanceTrends = pa.analyzePerformanceTrends()

	// Generate comprehensive recommendations
	report.Recommendations = pa.generateComprehensiveRecommendations(report)

	return report, nil
}

// analysisLoop runs continuous performance analysis
func (pa *PerformanceAnalyzer) analysisLoop(ctx context.Context) {
	ticker := time.NewTicker(pa.config.AnalysisInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			pa.performPeriodicAnalysis()
		}
	}
}

// performPeriodicAnalysis performs periodic performance analysis
func (pa *PerformanceAnalyzer) performPeriodicAnalysis() {
	// Run auto-optimization if enabled
	if pa.optimizer != nil {
		pa.optimizer.AutoOptimize()
	}

	// Check for regressions
	if pa.regressionTracker != nil {
		alerts, err := pa.regressionTracker.DetectRegressions()
		if err == nil && len(alerts) > 0 {
			// Log critical regressions
			for _, alert := range alerts {
				if alert.Severity == "critical" {
					// In a real system, this would trigger alerts
					fmt.Printf("CRITICAL REGRESSION DETECTED: %s\n", alert.Description)
				}
			}
		}
	}
}

// getSystemInfo collects current system information
func (pa *PerformanceAnalyzer) getSystemInfo() SystemInfo {
	var memStats runtime.MemStats
	runtime.ReadMemStats(&memStats)

	return SystemInfo{
		GoVersion:      runtime.Version(),
		OS:             runtime.GOOS,
		Architecture:   runtime.GOARCH,
		CPUCount:       runtime.NumCPU(),
		MemoryTotal:    memStats.Sys,
		MemoryUsed:     memStats.Alloc,
		GoroutineCount: runtime.NumGoroutine(),
		Timestamp:      time.Now(),
	}
}

// analyzePerformanceTrends analyzes performance trends over time
func (pa *PerformanceAnalyzer) analyzePerformanceTrends() PerformanceTrends {
	trends := PerformanceTrends{
		LatencyTrend:    pa.analyzeTrend("latency"),
		ThroughputTrend: pa.analyzeTrend("throughput"),
		MemoryTrend:     pa.analyzeTrend("memory"),
		ErrorRateTrend:  pa.analyzeTrend("error_rate"),
	}

	return trends
}

// analyzeTrend analyzes trend for a specific metric
func (pa *PerformanceAnalyzer) analyzeTrend(_ string) TrendAnalysis {
	// This is a simplified trend analysis
	// In a real system, you'd use more sophisticated statistical methods

	trend := TrendAnalysis{
		Direction:  "stable",
		Magnitude:  0.0,
		Confidence: "low",
		DataPoints: 0,
		TimeSpan:   "24h",
	}

	if pa.regressionTracker == nil {
		return trend
	}

	// Get recent performance data for analysis
	since := time.Now().Add(-24 * time.Hour)
	operations := []string{"forest_insert", "forest_get", "network_sync"}

	totalSamples := 0
	for _, op := range operations {
		samples := pa.regressionTracker.GetPerformanceHistory(op, since)
		totalSamples += len(samples)
	}

	trend.DataPoints = totalSamples

	if totalSamples < 10 {
		trend.Confidence = "low"
		return trend
	}

	// Simple trend analysis based on sample count and recent performance
	if totalSamples > 100 {
		trend.Confidence = "high"
	} else if totalSamples > 50 {
		trend.Confidence = "medium"
	}

	// This would be replaced with actual trend calculation
	// For now, we'll use a placeholder
	trend.Direction = "stable"
	trend.Magnitude = 0.0

	return trend
}

// generateComprehensiveRecommendations generates comprehensive optimization recommendations
func (pa *PerformanceAnalyzer) generateComprehensiveRecommendations(report *PerformanceReport) []string {
	var recommendations []string

	// Add optimization recommendations
	if len(report.OptimizationReport.Actions) > 0 {
		recommendations = append(recommendations, report.OptimizationReport.Actions[0].Description)
	}

	// Add regression-based recommendations
	for _, alert := range report.RegressionAlerts {
		if alert.Severity == "critical" {
			recommendations = append(recommendations,
				fmt.Sprintf("URGENT: Address %s regression in %s (%.1f%% degradation)",
					alert.Type, alert.Operation, alert.Change))
		}
	}

	// Add system-level recommendations
	if report.SystemInfo.MemoryUsed > 1024*1024*1024 { // > 1GB
		recommendations = append(recommendations,
			"High memory usage detected - consider implementing memory optimization strategies")
	}

	if report.SystemInfo.GoroutineCount > 1000 {
		recommendations = append(recommendations,
			"High goroutine count detected - review goroutine lifecycle management")
	}

	// Add operation-specific recommendations
	for operation, stats := range report.OperationStats {
		if stats.LatencyStats.P95 > 100*time.Millisecond {
			recommendations = append(recommendations,
				fmt.Sprintf("High P95 latency in %s (%.2fms) - investigate bottlenecks",
					operation, float64(stats.LatencyStats.P95)/float64(time.Millisecond)))
		}
	}

	// Sort recommendations by priority (critical first)
	sort.Slice(recommendations, func(i, j int) bool {
		return len(recommendations[i]) > len(recommendations[j]) // Simple priority by length
	})

	return recommendations
}

// SaveReport saves a performance report to disk
func (pa *PerformanceAnalyzer) SaveReport(report *PerformanceReport) (string, error) {
	timestamp := report.GeneratedAt.Format("20060102-150405")
	filename := filepath.Join(pa.config.ReportDirectory, fmt.Sprintf("performance-report-%s.json", timestamp))

	// In a real implementation, you'd serialize the report to JSON
	// For now, we'll create a simple text report
	content := fmt.Sprintf(`Performance Report - %s

System Information:
- Go Version: %s
- OS: %s
- Architecture: %s
- CPU Count: %d
- Memory Used: %d MB
- Goroutines: %d

Recommendations:
`, report.GeneratedAt.Format(time.RFC3339),
		report.SystemInfo.GoVersion,
		report.SystemInfo.OS,
		report.SystemInfo.Architecture,
		report.SystemInfo.CPUCount,
		report.SystemInfo.MemoryUsed/1024/1024,
		report.SystemInfo.GoroutineCount)

	for i, rec := range report.Recommendations {
		content += fmt.Sprintf("%d. %s\n", i+1, rec)
	}

	if len(report.RegressionAlerts) > 0 {
		content += "\nRegression Alerts:\n"
		for _, alert := range report.RegressionAlerts {
			content += fmt.Sprintf("- %s: %s\n", alert.Severity, alert.Description)
		}
	}

	err := os.WriteFile(filename, []byte(content), 0644)
	if err != nil {
		return "", err
	}

	return filename, nil
}

// GetMetrics returns the current metrics instance
func (pa *PerformanceAnalyzer) GetMetrics() *Metrics {
	return pa.metrics
}

// GetProfiler returns the profiler instance
func (pa *PerformanceAnalyzer) GetProfiler() *PerformanceProfiler {
	return pa.profiler
}

// GetOptimizer returns the optimizer instance
func (pa *PerformanceAnalyzer) GetOptimizer() *PerformanceOptimizer {
	return pa.optimizer
}

// GetRegressionTracker returns the regression tracker instance
func (pa *PerformanceAnalyzer) GetRegressionTracker() *RegressionTracker {
	return pa.regressionTracker
}
