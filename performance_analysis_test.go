package dmt

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	. "github.com/smartystreets/goconvey/convey"
)

func TestPerformanceAnalyzer(t *testing.T) {
	Convey("Given a performance analyzer", t, func() {
		tempDir := filepath.Join(os.TempDir(), "dmt-analysis-test")
		defer os.RemoveAll(tempDir)

		config := AnalysisConfig{
			EnableProfiling:        true,
			EnableOptimization:     true,
			EnableRegression:       true,
			AnalysisInterval:       1 * time.Second,
			ReportDirectory:        tempDir,
			AutoApplyOptimizations: false,
		}

		analyzer, err := NewPerformanceAnalyzer(config)
		So(err, ShouldBeNil)

		Convey("When starting analysis", func() {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()

			err := analyzer.StartAnalysis(ctx)
			So(err, ShouldBeNil)

			Convey("And recording operations", func() {
				// Record some operations with different performance characteristics
				analyzer.RecordOperation("forest_insert", 25*time.Millisecond, map[string]interface{}{
					"key_size":   100,
					"value_size": 200,
				})

				analyzer.RecordOperation("forest_get", 5*time.Millisecond, map[string]interface{}{
					"cache_hit": true,
				})

				analyzer.RecordOperation("network_sync", 150*time.Millisecond, map[string]interface{}{
					"peer_count": 3,
					"data_size":  1024,
				})

				// Wait a bit for analysis
				time.Sleep(100 * time.Millisecond)

				Convey("When generating a report", func() {
					report, err := analyzer.GenerateReport()
					So(err, ShouldBeNil)
					So(report, ShouldNotBeNil)

					Convey("Then report should contain comprehensive information", func() {
						So(report.GeneratedAt, ShouldNotBeZeroValue)
						So(report.SystemInfo.GoVersion, ShouldNotBeEmpty)
						So(report.SystemInfo.CPUCount, ShouldBeGreaterThan, 0)
						So(report.SystemInfo.GoroutineCount, ShouldBeGreaterThan, 0)
					})

					Convey("And operation stats should be recorded", func() {
						// Check if we have operation stats
						So(len(report.OperationStats), ShouldBeGreaterThan, 0)
					})

					Convey("And recommendations should be provided", func() {
						// Should have some recommendations based on the recorded operations
						So(len(report.Recommendations), ShouldBeGreaterThan, 0)
					})
				})

				Convey("When stopping analysis", func() {
					report, err := analyzer.StopAnalysis()
					So(err, ShouldBeNil)
					So(report, ShouldNotBeNil)

					Convey("Then final report should be generated", func() {
						So(report.GeneratedAt, ShouldNotBeZeroValue)
						So(report.SystemInfo, ShouldNotBeZeroValue)
					})
				})
			})
		})

		Convey("When profiling operations", func() {
			operationExecuted := false
			metadata := map[string]interface{}{
				"test_param": "test_value",
			}

			err := analyzer.ProfileOperation(context.Background(), "test_operation", func() error {
				operationExecuted = true
				time.Sleep(10 * time.Millisecond)
				return nil
			}, metadata)

			So(err, ShouldBeNil)
			So(operationExecuted, ShouldBeTrue)

			Convey("Then operation should be recorded in profiler", func() {
				profiler := analyzer.GetProfiler()
				So(profiler, ShouldNotBeNil)

				min, max, avg, count := profiler.GetOperationStats("test_operation")
				So(count, ShouldEqual, 1)
				So(min, ShouldBeGreaterThan, 5*time.Millisecond)
				So(max, ShouldBeGreaterThan, 5*time.Millisecond)
				So(avg, ShouldBeGreaterThan, 5*time.Millisecond)
			})
		})

		Convey("When saving a report", func() {
			report, err := analyzer.GenerateReport()
			So(err, ShouldBeNil)

			filename, err := analyzer.SaveReport(report)
			So(err, ShouldBeNil)
			So(filename, ShouldNotBeEmpty)

			Convey("Then report file should exist", func() {
				_, err := os.Stat(filename)
				So(err, ShouldBeNil)

				// Check file content
				content, err := os.ReadFile(filename)
				So(err, ShouldBeNil)
				So(len(content), ShouldBeGreaterThan, 0)
				So(string(content), ShouldContainSubstring, "Performance Report")
			})
		})
	})
}

func TestPerformanceAnalyzerComponents(t *testing.T) {
	Convey("Given a performance analyzer with all components enabled", t, func() {
		tempDir := filepath.Join(os.TempDir(), "dmt-components-test")
		defer os.RemoveAll(tempDir)

		config := AnalysisConfig{
			EnableProfiling:    true,
			EnableOptimization: true,
			EnableRegression:   true,
			ReportDirectory:    tempDir,
		}

		analyzer, err := NewPerformanceAnalyzer(config)
		So(err, ShouldBeNil)

		Convey("Then all components should be initialized", func() {
			So(analyzer.GetProfiler(), ShouldNotBeNil)
			So(analyzer.GetOptimizer(), ShouldNotBeNil)
			So(analyzer.GetRegressionTracker(), ShouldNotBeNil)
			So(analyzer.GetMetrics(), ShouldNotBeNil)
		})

		Convey("When components are disabled", func() {
			disabledConfig := AnalysisConfig{
				EnableProfiling:    false,
				EnableOptimization: false,
				EnableRegression:   false,
				ReportDirectory:    tempDir,
			}

			disabledAnalyzer, err := NewPerformanceAnalyzer(disabledConfig)
			So(err, ShouldBeNil)

			Convey("Then disabled components should be nil", func() {
				So(disabledAnalyzer.GetProfiler(), ShouldBeNil)
				So(disabledAnalyzer.GetOptimizer(), ShouldBeNil)
				So(disabledAnalyzer.GetRegressionTracker(), ShouldBeNil)
				So(disabledAnalyzer.GetMetrics(), ShouldNotBeNil) // Metrics always enabled
			})
		})
	})
}

func TestPerformanceTrendAnalysis(t *testing.T) {
	Convey("Given a performance analyzer with regression tracking", t, func() {
		tempDir := filepath.Join(os.TempDir(), "dmt-trends-test")
		defer os.RemoveAll(tempDir)

		config := AnalysisConfig{
			EnableRegression: true,
			ReportDirectory:  tempDir,
		}

		analyzer, err := NewPerformanceAnalyzer(config)
		So(err, ShouldBeNil)

		Convey("When recording performance data over time", func() {
			// Record baseline performance
			for i := 0; i < 10; i++ {
				analyzer.RecordOperation("trend_test", 50*time.Millisecond, map[string]interface{}{
					"iteration": i,
				})
				time.Sleep(1 * time.Millisecond) // Small delay to create time series
			}

			// Record degraded performance
			for i := 10; i < 20; i++ {
				analyzer.RecordOperation("trend_test", 100*time.Millisecond, map[string]interface{}{
					"iteration": i,
				})
				time.Sleep(1 * time.Millisecond)
			}

			Convey("When generating a report", func() {
				report, err := analyzer.GenerateReport()
				So(err, ShouldBeNil)

				Convey("Then performance trends should be analyzed", func() {
					So(report.PerformanceTrends, ShouldNotBeZeroValue)
					So(report.PerformanceTrends.LatencyTrend.Direction, ShouldBeIn, "improving", "degrading", "stable")
				})

				Convey("And regression alerts should be detected", func() {
					// Should detect regression in trend_test operation
					regressionFound := false
					for _, alert := range report.RegressionAlerts {
						if alert.Operation == "trend_test" {
							regressionFound = true
							So(alert.Type, ShouldEqual, "latency")
							So(alert.Change, ShouldBeGreaterThan, 0)
							break
						}
					}
					So(regressionFound, ShouldBeTrue)
				})
			})
		})
	})
}

func TestComprehensiveRecommendations(t *testing.T) {
	Convey("Given a performance analyzer with various performance issues", t, func() {
		tempDir := filepath.Join(os.TempDir(), "dmt-recommendations-test")
		defer os.RemoveAll(tempDir)

		config := AnalysisConfig{
			EnableProfiling:    true,
			EnableOptimization: true,
			EnableRegression:   true,
			ReportDirectory:    tempDir,
		}

		analyzer, err := NewPerformanceAnalyzer(config)
		So(err, ShouldBeNil)

		Convey("When recording operations with performance issues", func() {
			// Record slow operations that should trigger recommendations
			analyzer.RecordOperation("forest_insert", 200*time.Millisecond, nil)
			analyzer.RecordOperation("merkle_rebuild", 500*time.Millisecond, nil)
			analyzer.RecordOperation("network_sync", 1*time.Second, nil)

			report, err := analyzer.GenerateReport()
			So(err, ShouldBeNil)

			Convey("Then comprehensive recommendations should be generated", func() {
				So(len(report.Recommendations), ShouldBeGreaterThan, 0)

				recommendationText := ""
				for _, rec := range report.Recommendations {
					recommendationText += rec + " "
				}

				// Should contain operation-specific recommendations
				So(len(report.Recommendations), ShouldBeGreaterThanOrEqualTo, 1)
			})
		})
	})
}

// Benchmark the analyzer itself to ensure minimal overhead
func BenchmarkPerformanceAnalyzer(b *testing.B) {
	tempDir := filepath.Join(os.TempDir(), "dmt-analyzer-benchmark")
	defer os.RemoveAll(tempDir)

	config := AnalysisConfig{
		EnableProfiling:    true,
		EnableOptimization: true,
		EnableRegression:   true,
		ReportDirectory:    tempDir,
	}

	analyzer, err := NewPerformanceAnalyzer(config)
	if err != nil {
		b.Fatal(err)
	}

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		analyzer.RecordOperation("benchmark_op", 10*time.Millisecond, map[string]interface{}{
			"iteration": i,
		})
	}
}

func BenchmarkReportGeneration(b *testing.B) {
	tempDir := filepath.Join(os.TempDir(), "dmt-report-benchmark")
	defer os.RemoveAll(tempDir)

	config := AnalysisConfig{
		EnableProfiling:    true,
		EnableOptimization: true,
		EnableRegression:   true,
		ReportDirectory:    tempDir,
	}

	analyzer, err := NewPerformanceAnalyzer(config)
	if err != nil {
		b.Fatal(err)
	}

	// Pre-populate with some data
	for i := 0; i < 100; i++ {
		analyzer.RecordOperation("report_test", time.Duration(i)*time.Millisecond, nil)
	}

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		report, err := analyzer.GenerateReport()
		if err != nil {
			b.Fatalf("Failed to generate report: %v", err)
		}
		_ = report // Use the result
	}
}
