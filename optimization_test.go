package dmt

import (
	"fmt"
	"testing"
	"time"

	. "github.com/smartystreets/goconvey/convey"
)

func TestPerformanceOptimizer(t *testing.T) {
	Convey("Given a performance optimizer", t, func() {
		profiler, err := NewPerformanceProfiler(ProfileConfig{})
		So(err, ShouldBeNil)
		metrics := NewMetrics()

		config := OptimizationConfig{
			EnableAutoOptimization: true,
			OptimizationInterval:   1 * time.Second,
			MemoryThreshold:        100 * 1024 * 1024, // 100MB
			LatencyThreshold:       50 * time.Millisecond,
			CPUThreshold:           0.8,
		}

		optimizer := NewPerformanceOptimizer(config, profiler, metrics)

		Convey("When analyzing performance with no issues", func() {
			actions := optimizer.AnalyzePerformance()

			Convey("Then no optimization actions should be suggested", func() {
				So(len(actions), ShouldEqual, 0)
			})
		})

		Convey("When there are performance issues", func() {
			// Record slow operations
			profiler.RecordOperation("forest_insert", 100*time.Millisecond)
			profiler.RecordOperation("merkle_rebuild", 200*time.Millisecond)
			profiler.RecordOperation("network_sync", 300*time.Millisecond)

			actions := optimizer.AnalyzePerformance()

			Convey("Then optimization actions should be suggested", func() {
				So(len(actions), ShouldBeGreaterThan, 0)

				actionsByTarget := make(map[string]OptimizationAction)
				for _, action := range actions {
					actionsByTarget[action.Target] = action
				}

				// Should suggest optimizations for slow operations
				So(actionsByTarget["forest_insert"].Type, ShouldEqual, "latency")
				So(actionsByTarget["merkle_rebuild"].Type, ShouldEqual, "algorithm")
				So(actionsByTarget["network_sync"].Type, ShouldEqual, "network")
			})

			Convey("When applying an optimization", func() {
				action := actions[0]
				err := optimizer.ApplyOptimization(&action)

				Convey("Then the optimization should be applied successfully", func() {
					So(err, ShouldBeNil)
					So(action.Applied, ShouldBeTrue)
					So(action.AppliedAt, ShouldNotBeZeroValue)
					So(action.Result, ShouldNotBeEmpty)
				})

				Convey("And trying to apply it again should fail", func() {
					err := optimizer.ApplyOptimization(&action)
					So(err, ShouldNotBeNil)
					So(err.Error(), ShouldContainSubstring, "already applied")
				})
			})
		})

		Convey("When getting optimization history", func() {
			// Apply some optimizations
			profiler.RecordOperation("test_op", 100*time.Millisecond)
			actions := optimizer.AnalyzePerformance()

			if len(actions) > 0 {
				optimizer.ApplyOptimization(&actions[0])
			}

			history := optimizer.GetOptimizationHistory()

			Convey("Then history should contain applied optimizations", func() {
				So(len(history), ShouldBeGreaterThanOrEqualTo, len(actions))
			})
		})

		Convey("When generating an optimization report", func() {
			// Record some performance data
			profiler.RecordOperation("report_test", 75*time.Millisecond)

			report := optimizer.GenerateOptimizationReport()

			Convey("Then report should contain comprehensive information", func() {
				So(report.GeneratedAt, ShouldNotBeZeroValue)
				So(report.SystemMetrics.CPUCount, ShouldBeGreaterThan, 0)
				So(report.SystemMetrics.GoroutineCount, ShouldBeGreaterThan, 0)
			})
		})
	})
}

func TestOptimizationTypes(t *testing.T) {
	Convey("Given an optimizer with different optimization types", t, func() {
		profiler, err := NewPerformanceProfiler(ProfileConfig{})
		So(err, ShouldBeNil)
		metrics := NewMetrics()
		config := OptimizationConfig{
			LatencyThreshold: 10 * time.Millisecond,
		}
		optimizer := NewPerformanceOptimizer(config, profiler, metrics)

		testCases := []struct {
			operation    string
			latency      time.Duration
			expectedType string
		}{
			{"merkle_rebuild", 100 * time.Millisecond, "algorithm"},
			{"forest_insert", 50 * time.Millisecond, "algorithm"},
			{"persistence_write", 30 * time.Millisecond, "io"},
			{"network_sync", 200 * time.Millisecond, "network"},
		}

		for _, tc := range testCases {
			Convey(fmt.Sprintf("When %s is slow", tc.operation), func() {
				profiler.RecordOperation(tc.operation, tc.latency)
				actions := optimizer.AnalyzePerformance()

				Convey(fmt.Sprintf("Then %s optimization should be suggested", tc.expectedType), func() {
					found := false
					for _, action := range actions {
						if action.Target == tc.operation && action.Type == tc.expectedType {
							found = true
							break
						}
					}
					So(found, ShouldBeTrue)
				})
			})
		}
	})
}

func TestAutoOptimization(t *testing.T) {
	Convey("Given an optimizer with auto-optimization enabled", t, func() {
		profiler, err := NewPerformanceProfiler(ProfileConfig{})
		So(err, ShouldBeNil)
		metrics := NewMetrics()

		config := OptimizationConfig{
			EnableAutoOptimization: true,
			OptimizationInterval:   100 * time.Millisecond,
			LatencyThreshold:       10 * time.Millisecond,
		}

		optimizer := NewPerformanceOptimizer(config, profiler, metrics)

		Convey("When there are high-impact performance issues", func() {
			// Record operations that will trigger high-impact optimizations
			profiler.RecordOperation("merkle_rebuild", 200*time.Millisecond)
			profiler.RecordOperation("network_sync", 500*time.Millisecond)

			err := optimizer.AutoOptimize()

			Convey("Then auto-optimization should run successfully", func() {
				So(err, ShouldBeNil)

				// Check that high-impact optimizations were applied
				history := optimizer.GetOptimizationHistory()
				appliedCount := 0
				for _, action := range history {
					if action.Applied && action.Impact == "high" {
						appliedCount++
					}
				}
				So(appliedCount, ShouldBeGreaterThan, 0)
			})
		})

		Convey("When auto-optimization is disabled", func() {
			config.EnableAutoOptimization = false
			optimizer := NewPerformanceOptimizer(config, profiler, metrics)

			profiler.RecordOperation("test_op", 200*time.Millisecond)
			err := optimizer.AutoOptimize()

			Convey("Then no optimizations should be applied", func() {
				So(err, ShouldBeNil)
				history := optimizer.GetOptimizationHistory()
				So(len(history), ShouldEqual, 0)
			})
		})
	})
}

func TestBaselineRecording(t *testing.T) {
	Convey("Given an optimizer that records baselines", t, func() {
		profiler, err := NewPerformanceProfiler(ProfileConfig{})
		So(err, ShouldBeNil)
		metrics := NewMetrics()
		config := OptimizationConfig{}
		optimizer := NewPerformanceOptimizer(config, profiler, metrics)

		Convey("When recording performance data and applying optimization", func() {
			// Record some performance data
			for i := 0; i < 10; i++ {
				profiler.RecordOperation("baseline_test", 50*time.Millisecond)
			}

			// Trigger optimization which should record baseline
			actions := optimizer.AnalyzePerformance()
			if len(actions) > 0 {
				optimizer.ApplyOptimization(&actions[0])
			}

			baselines := optimizer.GetBaselines()

			Convey("Then baselines should be recorded", func() {
				if len(actions) > 0 {
					baseline, exists := baselines[actions[0].Target]
					So(exists, ShouldBeTrue)
					So(baseline.Operation, ShouldEqual, actions[0].Target)
					So(baseline.AverageTime, ShouldBeGreaterThan, 0)
					So(baseline.RecordedAt, ShouldNotBeZeroValue)
				}
			})
		})
	})
}

// Benchmark the optimizer itself to ensure minimal overhead
func BenchmarkOptimizationAnalysis(b *testing.B) {
	profiler, err := NewPerformanceProfiler(ProfileConfig{})
	if err != nil {
		b.Fatal(err)
	}
	metrics := NewMetrics()
	config := OptimizationConfig{
		LatencyThreshold: 50 * time.Millisecond,
	}
	optimizer := NewPerformanceOptimizer(config, profiler, metrics)

	// Pre-populate with some performance data
	operations := []string{"op1", "op2", "op3", "op4", "op5"}
	for _, op := range operations {
		for i := 0; i < 100; i++ {
			profiler.RecordOperation(op, time.Duration(i)*time.Millisecond)
		}
	}

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		optimizer.AnalyzePerformance()
	}
}

func BenchmarkOptimizationApplication(b *testing.B) {
	profiler, err := NewPerformanceProfiler(ProfileConfig{})
	if err != nil {
		b.Fatal(err)
	}
	metrics := NewMetrics()
	config := OptimizationConfig{}
	optimizer := NewPerformanceOptimizer(config, profiler, metrics)

	// Create optimization actions
	actions := []OptimizationAction{
		{Type: "memory", Target: "system", Description: "Test memory optimization"},
		{Type: "latency", Target: "test_op", Description: "Test latency optimization"},
		{Type: "algorithm", Target: "merkle_rebuild", Description: "Test algorithm optimization"},
	}

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		action := actions[i%len(actions)]
		action.Applied = false // Reset for benchmarking
		optimizer.ApplyOptimization(&action)
	}
}
