package dmt

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	. "github.com/smartystreets/goconvey/convey"
)

func TestPerformanceProfiler(t *testing.T) {
	Convey("Given a performance profiler", t, func() {
		tempDir := filepath.Join(os.TempDir(), "dmt-profiling-test")
		defer os.RemoveAll(tempDir)

		config := ProfileConfig{
			CPUProfile:   true,
			MemProfile:   true,
			BlockProfile: true,
			MutexProfile: true,
			ProfileDir:   tempDir,
			Duration:     100 * time.Millisecond,
			SampleRate:   100,
		}

		profiler, err := NewPerformanceProfiler(config)
		So(err, ShouldBeNil)

		Convey("When starting profiling", func() {
			err := profiler.StartProfiling(context.Background())
			So(err, ShouldBeNil)
			So(profiler.IsActive(), ShouldBeTrue)

			Convey("And recording operations", func() {
				// Record some operations
				profiler.RecordOperation("test_insert", 50*time.Millisecond)
				profiler.RecordOperation("test_insert", 75*time.Millisecond)
				profiler.RecordOperation("test_lookup", 10*time.Millisecond)
				profiler.RecordOperation("test_sync", 200*time.Millisecond)

				// Wait a bit for profiling
				time.Sleep(150 * time.Millisecond)

				Convey("When stopping profiling", func() {
					result, err := profiler.StopProfiling()
					So(err, ShouldBeNil)
					So(profiler.IsActive(), ShouldBeFalse)

					Convey("Then profile files should be created", func() {
						So(result.CPUProfilePath, ShouldNotBeEmpty)
						So(result.MemProfilePath, ShouldNotBeEmpty)
						So(result.BlockProfilePath, ShouldNotBeEmpty)
						So(result.MutexProfilePath, ShouldNotBeEmpty)

						// Check files exist
						_, err := os.Stat(result.CPUProfilePath)
						So(err, ShouldBeNil)
						_, err = os.Stat(result.MemProfilePath)
						So(err, ShouldBeNil)
					})

					Convey("And bottlenecks should be identified", func() {
						So(len(result.Bottlenecks), ShouldBeGreaterThan, 0)

						// Should identify test_sync as a bottleneck (200ms)
						found := false
						for _, bottleneck := range result.Bottlenecks {
							if bottleneck.Operation == "test_sync" {
								found = true
								So(bottleneck.Severity, ShouldBeIn, "medium", "high", "critical")
								break
							}
						}
						So(found, ShouldBeTrue)
					})

					Convey("And recommendations should be provided", func() {
						So(len(result.Recommendations), ShouldBeGreaterThan, 0)
					})
				})
			})

			Convey("When trying to start profiling again", func() {
				err := profiler.StartProfiling(context.Background())
				So(err, ShouldNotBeNil)
				So(errors.Is(err, ErrProfilingActive), ShouldBeTrue)
			})
		})

		Convey("When profiling an operation", func() {
			operationCalled := false
			err := profiler.ProfileOperation(context.Background(), "test_operation", func() error {
				operationCalled = true
				time.Sleep(10 * time.Millisecond)
				return nil
			})

			So(err, ShouldBeNil)
			So(operationCalled, ShouldBeTrue)

			Convey("Then operation stats should be recorded", func() {
				min, max, avg, count := profiler.GetOperationStats("test_operation")
				So(count, ShouldEqual, 1)
				So(min, ShouldBeGreaterThan, 5*time.Millisecond)
				So(max, ShouldBeGreaterThan, 5*time.Millisecond)
				So(avg, ShouldBeGreaterThan, 5*time.Millisecond)
			})
		})

		Convey("When recording multiple operations for regression detection", func() {
			// Record baseline operations (fast)
			for i := 0; i < 10; i++ {
				profiler.RecordOperation("regression_test", 10*time.Millisecond)
			}

			// Record recent operations (slow - regression)
			for i := 0; i < 10; i++ {
				profiler.RecordOperation("regression_test", 50*time.Millisecond)
			}

			Convey("Then regressions should be detected", func() {
				regressions := profiler.detectRegressions()
				found := false
				for _, regression := range regressions {
					if regression.Operation == "regression_test" {
						found = true
						So(regression.Regression, ShouldBeGreaterThan, 20)
						break
					}
				}
				So(found, ShouldBeTrue)
			})
		})

		Convey("When resetting profiler", func() {
			profiler.RecordOperation("test_op", 10*time.Millisecond)
			profiler.Reset()

			Convey("Then all data should be cleared", func() {
				_, _, _, count := profiler.GetOperationStats("test_op")
				So(count, ShouldEqual, 0)
			})
		})
	})
}

func TestBottleneckDetection(t *testing.T) {
	Convey("Given a profiler with various operation timings", t, func() {
		profiler, err := NewPerformanceProfiler(ProfileConfig{})
		So(err, ShouldBeNil)

		// Record operations with different performance characteristics
		profiler.RecordOperation("fast_op", 5*time.Millisecond)
		profiler.RecordOperation("medium_op", 75*time.Millisecond)
		profiler.RecordOperation("slow_op", 150*time.Millisecond)
		profiler.RecordOperation("very_slow_op", 1200*time.Millisecond)

		Convey("When analyzing bottlenecks", func() {
			bottlenecks := profiler.analyzeBottlenecks()

			Convey("Then appropriate operations should be identified as bottlenecks", func() {
				bottleneckOps := make(map[string]string)
				for _, b := range bottlenecks {
					bottleneckOps[b.Operation] = b.Severity
				}

				// fast_op should not be a bottleneck
				_, exists := bottleneckOps["fast_op"]
				So(exists, ShouldBeFalse)

				// medium_op should be a bottleneck
				severity, exists := bottleneckOps["medium_op"]
				So(exists, ShouldBeTrue)
				So(severity, ShouldBeIn, "low", "medium")

				// slow_op should be a bottleneck
				severity, exists = bottleneckOps["slow_op"]
				So(exists, ShouldBeTrue)
				So(severity, ShouldBeIn, "medium", "high")

				// very_slow_op should be a critical bottleneck
				severity, exists = bottleneckOps["very_slow_op"]
				So(exists, ShouldBeTrue)
				So(severity, ShouldEqual, "critical")
			})
		})
	})
}

func TestRegressionDetection(t *testing.T) {
	Convey("Given a profiler with performance data", t, func() {
		profiler, err := NewPerformanceProfiler(ProfileConfig{})
		So(err, ShouldBeNil)

		Convey("When there's no regression", func() {
			// Record consistent performance
			for i := 0; i < 20; i++ {
				profiler.RecordOperation("stable_op", 10*time.Millisecond)
			}

			regressions := profiler.detectRegressions()

			Convey("Then no regressions should be detected", func() {
				found := false
				for _, r := range regressions {
					if r.Operation == "stable_op" {
						found = true
						break
					}
				}
				So(found, ShouldBeFalse)
			})
		})

		Convey("When there's a significant regression", func() {
			// Record baseline (fast)
			for i := 0; i < 5; i++ {
				profiler.RecordOperation("regressed_op", 10*time.Millisecond)
			}
			// Record middle values
			for i := 0; i < 10; i++ {
				profiler.RecordOperation("regressed_op", 15*time.Millisecond)
			}
			// Record recent (slow - regression)
			for i := 0; i < 5; i++ {
				profiler.RecordOperation("regressed_op", 30*time.Millisecond)
			}

			regressions := profiler.detectRegressions()

			Convey("Then regression should be detected", func() {
				found := false
				for _, r := range regressions {
					if r.Operation == "regressed_op" {
						found = true
						So(r.Regression, ShouldBeGreaterThan, 20)
						So(r.Severity, ShouldBeIn, "medium", "high", "critical")
						break
					}
				}
				So(found, ShouldBeTrue)
			})
		})
	})
}

func TestRecommendationGeneration(t *testing.T) {
	Convey("Given profiling results with bottlenecks", t, func() {
		profiler, err := NewPerformanceProfiler(ProfileConfig{})
		So(err, ShouldBeNil)

		result := &ProfileResult{
			Bottlenecks: []BottleneckInfo{
				{
					Operation:   "merkle_rebuild",
					AverageTime: 150 * time.Millisecond,
					Severity:    "high",
				},
				{
					Operation:   "forest_insert",
					AverageTime: 75 * time.Millisecond,
					Severity:    "medium",
				},
			},
			Regressions: []RegressionInfo{
				{
					Operation:  "network_sync",
					Regression: 150,
					Severity:   "critical",
				},
			},
		}

		Convey("When generating recommendations", func() {
			recommendations := profiler.generateRecommendations(result)

			Convey("Then appropriate recommendations should be provided", func() {
				So(len(recommendations), ShouldBeGreaterThan, 0)

				recommendationText := ""
				for _, rec := range recommendations {
					recommendationText += rec + " "
				}

				// Should recommend Merkle tree optimization
				So(recommendationText, ShouldContainSubstring, "incremental Merkle")

				// Should recommend Forest optimization
				So(recommendationText, ShouldContainSubstring, "tree selection")

				// Should highlight critical regression
				So(recommendationText, ShouldContainSubstring, "CRITICAL")
				So(recommendationText, ShouldContainSubstring, "network_sync")
			})
		})
	})
}

// Benchmark the profiler itself to ensure minimal overhead
func BenchmarkProfilerOverhead(b *testing.B) {
	profiler, err := NewPerformanceProfiler(ProfileConfig{})
	if err != nil {
		b.Fatal(err)
	}

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		profiler.RecordOperation("benchmark_op", 1*time.Millisecond)
	}
}

func BenchmarkProfileOperation(b *testing.B) {
	profiler, err := NewPerformanceProfiler(ProfileConfig{})
	if err != nil {
		b.Fatal(err)
	}

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		profiler.ProfileOperation(context.Background(), "benchmark_op", func() error {
			// Simulate some work
			time.Sleep(100 * time.Microsecond)
			return nil
		})
	}
}
