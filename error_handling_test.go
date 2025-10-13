/*
package dmt implements tests for error handling, logging, and metrics functionality.
These tests validate error categorization, retry logic, structured logging, and metrics collection
to ensure the observability and reliability features work correctly.
*/
package dmt

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"

	. "github.com/smartystreets/goconvey/convey"
)

func TestErrorHandling(t *testing.T) {
	Convey("Error Handling", t, func() {

		Convey("Error categorization should work correctly", func() {
			var buf bytes.Buffer
			logger := TestLogger("test-node", &buf)
			metrics := NewMetrics()
			errorHandler := NewErrorHandler(logger, metrics, "test-node")

			Convey("Fatal errors should be categorized correctly", func() {
				err := errors.New("no space left on device")
				dmtErr, action := errorHandler.HandleError(err, "persistence", nil)

				So(dmtErr.Category, ShouldEqual, ErrorCategoryFatal)
				So(action, ShouldEqual, ErrorActionShutdown)
				So(dmtErr.Retryable, ShouldBeFalse)
			})

			Convey("Transient errors should be categorized correctly", func() {
				err := errors.New("connection refused")
				dmtErr, action := errorHandler.HandleError(err, "network", nil)

				So(dmtErr.Category, ShouldEqual, ErrorCategoryTransient)
				So(action, ShouldEqual, ErrorActionRetry)
				So(dmtErr.Retryable, ShouldBeTrue)
			})

			Convey("Logic errors should be categorized correctly", func() {
				err := errors.New("invalid input parameter")
				dmtErr, action := errorHandler.HandleError(err, "validation", nil)

				So(dmtErr.Category, ShouldEqual, ErrorCategoryLogic)
				So(action, ShouldEqual, ErrorActionReject)
				So(dmtErr.Retryable, ShouldBeFalse)
			})
		})

		Convey("Retry logic should work correctly", func() {
			var buf bytes.Buffer
			logger := TestLogger("test-node", &buf)
			metrics := NewMetrics()
			errorHandler := NewErrorHandler(logger, metrics, "test-node")

			Convey("Should retry transient errors", func() {
				attempts := 0
				ctx := context.Background()

				config := RetryConfig{
					MaxRetries:    3,
					BaseDelay:     1 * time.Millisecond,
					MaxDelay:      10 * time.Millisecond,
					BackoffFactor: 2.0,
					JitterEnabled: false,
				}

				err := errorHandler.RetryWithBackoff(ctx, "test_operation", config, func() error {
					attempts++
					if attempts < 3 {
						return errors.New("connection timeout")
					}
					return nil
				})

				So(err, ShouldBeNil)
				So(attempts, ShouldEqual, 3)
			})

			Convey("Should stop retrying after max attempts", func() {
				attempts := 0
				ctx := context.Background()

				config := RetryConfig{
					MaxRetries:    2,
					BaseDelay:     1 * time.Millisecond,
					MaxDelay:      10 * time.Millisecond,
					BackoffFactor: 2.0,
					JitterEnabled: false,
				}

				err := errorHandler.RetryWithBackoff(ctx, "test_operation", config, func() error {
					attempts++
					return errors.New("connection timeout")
				})

				So(err, ShouldNotBeNil)
				So(attempts, ShouldEqual, 3) // Initial attempt + 2 retries
			})

			Convey("Should respect context cancellation", func() {
				attempts := 0
				ctx, cancel := context.WithCancel(context.Background())

				config := RetryConfig{
					MaxRetries:    5,
					BaseDelay:     100 * time.Millisecond,
					MaxDelay:      1 * time.Second,
					BackoffFactor: 2.0,
					JitterEnabled: false,
				}

				// Cancel context after first attempt
				go func() {
					time.Sleep(10 * time.Millisecond)
					cancel()
				}()

				err := errorHandler.RetryWithBackoff(ctx, "test_operation", config, func() error {
					attempts++
					return errors.New("connection timeout")
				})

				So(err, ShouldNotBeNil)
				So(attempts, ShouldEqual, 1)
				So(err.Error(), ShouldContainSubstring, "context canceled")
			})
		})

		Convey("Error wrapping should work correctly", func() {
			var buf bytes.Buffer
			logger := TestLogger("test-node", &buf)
			metrics := NewMetrics()
			errorHandler := NewErrorHandler(logger, metrics, "test-node")

			originalErr := errors.New("original error")
			context := map[string]interface{}{
				"key": "value",
			}

			wrappedErr := errorHandler.WrapError(originalErr, "test_operation", context)

			So(wrappedErr, ShouldNotBeNil)
			So(wrappedErr.Underlying, ShouldEqual, originalErr)
			So(wrappedErr.Operation, ShouldEqual, "test_operation")
			So(wrappedErr.Context["key"], ShouldEqual, "value")
		})
	})
}

func TestStructuredLogging(t *testing.T) {
	Convey("Structured Logging", t, func() {

		Convey("Logger should format messages correctly", func() {
			var buf bytes.Buffer
			logger := TestLogger("test-node", &buf)

			logger.Info("Test message", "key1", "value1", "key2", 42)

			output := buf.String()
			So(output, ShouldContainSubstring, "level=INFO")
			So(output, ShouldContainSubstring, "msg=\"Test message\"")
			So(output, ShouldContainSubstring, "node_id=test-node")
			So(output, ShouldContainSubstring, "key1=\"value1\"")
			So(output, ShouldContainSubstring, "key2=42")
		})

		Convey("Logger should handle different log levels", func() {
			var buf bytes.Buffer
			logger := TestLogger("test-node", &buf)
			logger.SetLevel(LogLevelWarn)

			logger.Debug("Debug message")
			logger.Info("Info message")
			logger.Warn("Warn message")

			output := buf.String()
			So(output, ShouldNotContainSubstring, "Debug message")
			So(output, ShouldNotContainSubstring, "Info message")
			So(output, ShouldContainSubstring, "Warn message")
		})

		Convey("Logger should handle context correctly", func() {
			var buf bytes.Buffer
			logger := TestLogger("test-node", &buf)

			ctx := WithRequestID(context.Background(), "req-123")
			ctx = WithOperation(ctx, "test-op")

			contextLogger := logger.WithContext(ctx)
			contextLogger.Info("Test message")

			output := buf.String()
			So(output, ShouldContainSubstring, "request_id=\"req-123\"")
			So(output, ShouldContainSubstring, "operation=\"test-op\"")
		})

		Convey("Logger should handle fields correctly", func() {
			var buf bytes.Buffer
			logger := TestLogger("test-node", &buf)

			fields := map[string]interface{}{
				"field1": "value1",
				"field2": 123,
				"field3": true,
			}

			fieldLogger := logger.WithFields(fields)
			fieldLogger.Info("Test message")

			output := buf.String()
			So(output, ShouldContainSubstring, "field1=\"value1\"")
			So(output, ShouldContainSubstring, "field2=123")
			So(output, ShouldContainSubstring, "field3=true")
		})
	})
}

func TestMetricsCollection(t *testing.T) {
	Convey("Metrics Collection", t, func() {

		Convey("Basic metrics should be recorded correctly", func() {
			metrics := NewMetrics()

			// Record some operations
			metrics.RecordInsert(10*time.Millisecond, 100)
			metrics.RecordLookup(5 * time.Millisecond)
			metrics.RecordSync(20*time.Millisecond, 200)

			metricsData := metrics.GetMetrics()

			So(metricsData["operations"].(map[string]uint64)["insert"], ShouldEqual, 1)
			So(metricsData["operations"].(map[string]uint64)["lookup"], ShouldEqual, 1)
			So(metricsData["operations"].(map[string]uint64)["sync"], ShouldEqual, 1)
		})

		Convey("Error metrics should be recorded correctly", func() {
			metrics := NewMetrics()

			metrics.RecordError("network", "timeout")
			metrics.RecordError("network", "timeout")
			metrics.RecordError("persistence", "disk_full")

			errorMetrics := metrics.GetErrorMetrics()

			So(errorMetrics["network"]["timeout"], ShouldEqual, 2)
			So(errorMetrics["persistence"]["disk_full"], ShouldEqual, 1)
		})

		Convey("Consensus metrics should be recorded correctly", func() {
			metrics := NewMetrics()

			metrics.RecordHeartbeatSent()
			metrics.RecordHeartbeatReceived()
			metrics.RecordAppendEntriesSent()
			metrics.RecordAppendEntriesReceived()
			metrics.UpdateCommitIndex(10)
			metrics.UpdateAppliedIndex(8)

			consensusMetrics := metrics.GetConsensusMetrics()

			So(consensusMetrics["heartbeats_sent"], ShouldEqual, 1)
			So(consensusMetrics["heartbeats_received"], ShouldEqual, 1)
			So(consensusMetrics["append_entries_sent"], ShouldEqual, 1)
			So(consensusMetrics["append_entries_recv"], ShouldEqual, 1)
			So(consensusMetrics["commit_index"], ShouldEqual, 10)
			So(consensusMetrics["applied_index"], ShouldEqual, 8)
		})

		Convey("Health status should be calculated correctly", func() {
			metrics := NewMetrics()

			// Initially healthy
			status, _ := metrics.GetHealthStatus()
			So(status, ShouldEqual, "healthy")

			// Record some errors to make it unhealthy
			for i := 0; i < 20; i++ {
				metrics.RecordError("test", "error")
				metrics.RecordInsert(time.Millisecond, 10)
			}

			status, _ = metrics.GetHealthStatus()
			So(status, ShouldEqual, "unhealthy")
		})

		Convey("Metrics reset should work correctly", func() {
			metrics := NewMetrics()

			// Record some data
			metrics.RecordInsert(10*time.Millisecond, 100)
			metrics.RecordError("test", "error")
			metrics.UpdateCommitIndex(5)

			// Reset
			metrics.Reset()

			// Check that everything is reset
			metricsData := metrics.GetMetrics()
			So(metricsData["operations"].(map[string]uint64)["insert"], ShouldEqual, 0)

			errorMetrics := metrics.GetErrorMetrics()
			So(len(errorMetrics), ShouldEqual, 0)

			consensusMetrics := metrics.GetConsensusMetrics()
			So(consensusMetrics["commit_index"], ShouldEqual, 0)
		})
	})
}

func TestHealthMonitoring(t *testing.T) {
	Convey("Health Monitoring", t, func() {

		Convey("Health monitor should register and run checks", func() {
			var buf bytes.Buffer
			logger := TestLogger("test-node", &buf)
			metrics := NewMetrics()

			monitor := NewHealthMonitor(HealthMonitorConfig{
				CheckInterval: 100 * time.Millisecond,
				Logger:        logger,
				Metrics:       metrics,
			})
			defer monitor.Close()

			// Register a test check
			checkCalled := false
			monitor.RegisterCheck("test_check", func() *HealthCheck {
				checkCalled = true
				return &HealthCheck{
					Name:    "test_check",
					Status:  HealthStatusHealthy,
					Message: "Test check passed",
				}
			})

			// Wait for check to run
			time.Sleep(150 * time.Millisecond)

			So(checkCalled, ShouldBeTrue)

			report := monitor.GetHealthReport()
			So(report["overall_status"], ShouldEqual, HealthStatusHealthy)

			checks := report["checks"].(map[string]*HealthCheck)
			So(checks["test_check"].Status, ShouldEqual, HealthStatusHealthy)
			So(checks["test_check"].Message, ShouldEqual, "Test check passed")
		})

		Convey("Health monitor should calculate overall status correctly", func() {
			var buf bytes.Buffer
			logger := TestLogger("test-node", &buf)
			metrics := NewMetrics()

			monitor := NewHealthMonitor(HealthMonitorConfig{
				CheckInterval: 1 * time.Second, // Long interval to control timing
				Logger:        logger,
				Metrics:       metrics,
			})
			defer monitor.Close()

			// Register healthy check
			monitor.RegisterCheck("healthy_check", func() *HealthCheck {
				return &HealthCheck{
					Name:    "healthy_check",
					Status:  HealthStatusHealthy,
					Message: "All good",
				}
			})

			// Register degraded check
			monitor.RegisterCheck("degraded_check", func() *HealthCheck {
				return &HealthCheck{
					Name:    "degraded_check",
					Status:  HealthStatusDegraded,
					Message: "Some issues",
				}
			})

			// Run checks manually
			monitor.runAllChecks()

			// Should be degraded due to one degraded check
			So(monitor.GetOverallStatus(), ShouldEqual, HealthStatusDegraded)

			// Add unhealthy check
			monitor.RegisterCheck("unhealthy_check", func() *HealthCheck {
				return &HealthCheck{
					Name:    "unhealthy_check",
					Status:  HealthStatusUnhealthy,
					Message: "Critical issues",
				}
			})

			// Run checks again
			monitor.runAllChecks()

			// Should be unhealthy due to one unhealthy check
			So(monitor.GetOverallStatus(), ShouldEqual, HealthStatusUnhealthy)
		})
	})
}

func TestIntegratedErrorScenarios(t *testing.T) {
	Convey("Integrated Error Scenarios", t, func() {

		Convey("Network connection failures should be handled correctly", func() {
			var buf bytes.Buffer
			logger := TestLogger("test-node", &buf)
			metrics := NewMetrics()
			errorHandler := NewErrorHandler(logger, metrics, "test-node")

			// Simulate network connection failure
			err := errors.New("dial tcp 127.0.0.1:8080: connection refused")
			dmtErr, action := errorHandler.HandleError(err, "peer_connect", map[string]interface{}{
				"peer_addr": "127.0.0.1:8080",
			})

			So(dmtErr.Category, ShouldEqual, ErrorCategoryTransient)
			So(action, ShouldEqual, ErrorActionRetry)

			// Check that error was logged
			output := buf.String()
			So(output, ShouldContainSubstring, "TRANSIENT")
			So(output, ShouldContainSubstring, "peer_connect")
			So(output, ShouldContainSubstring, "connection refused")

			// Check that metrics were updated
			errorMetrics := metrics.GetErrorMetrics()
			So(errorMetrics["peer_connect"]["TRANSIENT"], ShouldEqual, 1)
		})

		Convey("Persistence failures should be handled correctly", func() {
			var buf bytes.Buffer
			logger := TestLogger("test-node", &buf)
			metrics := NewMetrics()
			errorHandler := NewErrorHandler(logger, metrics, "test-node")

			// Simulate disk full error
			err := errors.New("write /tmp/wal.log: no space left on device")
			dmtErr, action := errorHandler.HandleError(err, "persistence", map[string]interface{}{
				"file_path": "/tmp/wal.log",
			})

			So(dmtErr.Category, ShouldEqual, ErrorCategoryFatal)
			So(action, ShouldEqual, ErrorActionShutdown)

			// Check that error was logged as fatal
			output := buf.String()
			So(output, ShouldContainSubstring, "FATAL")
			So(output, ShouldContainSubstring, "persistence")
			So(output, ShouldContainSubstring, "no space left on device")
		})

		Convey("Election failures should be handled correctly", func() {
			var buf bytes.Buffer
			logger := TestLogger("test-node", &buf)
			metrics := NewMetrics()
			errorHandler := NewErrorHandler(logger, metrics, "test-node")

			// Simulate election timeout
			err := errors.New("election timeout: no leader elected")
			dmtErr, action := errorHandler.HandleError(err, "election", map[string]interface{}{
				"term": 5,
			})

			So(dmtErr.Category, ShouldEqual, ErrorCategoryTransient)
			So(action, ShouldEqual, ErrorActionRetry)

			// Check logging and metrics
			output := buf.String()
			So(output, ShouldContainSubstring, "election")

			errorMetrics := metrics.GetErrorMetrics()
			So(errorMetrics["election"]["TRANSIENT"], ShouldEqual, 1)
		})
	})
}

func TestErrorRecoveryBehavior(t *testing.T) {
	Convey("Error Recovery Behavior", t, func() {

		Convey("Should recover from transient network errors", func() {
			var buf bytes.Buffer
			logger := TestLogger("test-node", &buf)
			metrics := NewMetrics()
			errorHandler := NewErrorHandler(logger, metrics, "test-node")

			attempts := 0
			ctx := context.Background()

			config := DefaultRetryConfig()
			config.MaxRetries = 3
			config.BaseDelay = 1 * time.Millisecond

			err := errorHandler.RetryWithBackoff(ctx, "network_operation", config, func() error {
				attempts++
				if attempts <= 2 {
					return errors.New("connection timeout")
				}
				return nil // Success on third attempt
			})

			So(err, ShouldBeNil)
			So(attempts, ShouldEqual, 3)

			// Check that retry was logged
			output := buf.String()
			So(output, ShouldContainSubstring, "Operation succeeded after retry")
		})

		Convey("Should not retry fatal errors", func() {
			var buf bytes.Buffer
			logger := TestLogger("test-node", &buf)
			metrics := NewMetrics()
			errorHandler := NewErrorHandler(logger, metrics, "test-node")

			attempts := 0
			ctx := context.Background()

			config := DefaultRetryConfig()
			config.MaxRetries = 3
			config.BaseDelay = 1 * time.Millisecond

			err := errorHandler.RetryWithBackoff(ctx, "persistence", config, func() error {
				attempts++
				return errors.New("disk corruption detected")
			})

			So(err, ShouldNotBeNil)
			So(attempts, ShouldEqual, 1) // Should not retry fatal errors

			// Check that it was categorized as fatal
			dmtErr, ok := err.(*DMTError)
			So(ok, ShouldBeTrue)
			So(dmtErr.Category, ShouldEqual, ErrorCategoryFatal)
		})
	})
}

func TestCircuitBreaker(t *testing.T) {
	Convey("Circuit Breaker", t, func() {

		Convey("Should allow execution when closed", func() {
			cb := NewCircuitBreaker()
			So(cb.CanExecute(), ShouldBeTrue)
			So(cb.GetState(), ShouldEqual, CircuitBreakerClosed)
		})

		Convey("Should open after failure threshold", func() {
			cb := NewCircuitBreaker()

			// Record failures up to threshold
			for i := 0; i < 5; i++ {
				cb.RecordFailure()
			}

			So(cb.GetState(), ShouldEqual, CircuitBreakerOpen)
			So(cb.CanExecute(), ShouldBeFalse)
		})

		Convey("Should transition to half-open after recovery timeout", func() {
			cb := NewCircuitBreaker()
			cb.recoveryTimeout = 1 * time.Millisecond // Short timeout for testing

			// Open the circuit
			for i := 0; i < 5; i++ {
				cb.RecordFailure()
			}
			So(cb.GetState(), ShouldEqual, CircuitBreakerOpen)

			// Wait for recovery timeout
			time.Sleep(2 * time.Millisecond)

			// Should allow execution and transition to half-open
			So(cb.CanExecute(), ShouldBeTrue)
			So(cb.GetState(), ShouldEqual, CircuitBreakerHalfOpen)
		})

		Convey("Should close after successful execution in half-open state", func() {
			cb := NewCircuitBreaker()
			cb.state = CircuitBreakerHalfOpen

			cb.RecordSuccess()
			So(cb.GetState(), ShouldEqual, CircuitBreakerClosed)
		})
	})
}

func TestStructuredErrorTypes(t *testing.T) {
	Convey("Structured Error Types", t, func() {

		Convey("NetworkError should format correctly", func() {
			underlying := errors.New("connection refused")
			netErr := NewNetworkError("sync", "peer1", underlying)

			So(netErr.Error(), ShouldContainSubstring, "network error in sync with peer peer1")
			So(netErr.Unwrap(), ShouldEqual, underlying)
		})

		Convey("ConsensusError should format correctly", func() {
			underlying := errors.New("term mismatch")
			consensusErr := NewConsensusError("election", 5, underlying)

			So(consensusErr.Error(), ShouldContainSubstring, "consensus error in election at term 5")
			So(consensusErr.Unwrap(), ShouldEqual, underlying)
		})

		Convey("PersistenceError should format correctly", func() {
			underlying := errors.New("disk full")
			persistErr := NewPersistenceError("write", "/tmp/wal.log", underlying)

			So(persistErr.Error(), ShouldContainSubstring, "persistence error in write at path /tmp/wal.log")
			So(persistErr.Unwrap(), ShouldEqual, underlying)
		})
	})
}

func TestEnhancedErrorCategorization(t *testing.T) {
	Convey("Enhanced Error Categorization", t, func() {
		var buf bytes.Buffer
		logger := TestLogger("test-node", &buf)
		metrics := NewMetrics()
		errorHandler := NewErrorHandler(logger, metrics, "test-node")

		Convey("Should categorize NetworkError as transient", func() {
			netErr := NewNetworkError("sync", "peer1", errors.New("connection refused"))
			dmtErr, action := errorHandler.HandleError(netErr, "network_sync", nil)

			So(dmtErr.Category, ShouldEqual, ErrorCategoryTransient)
			So(action, ShouldEqual, ErrorActionRetry)
		})

		Convey("Should categorize ConsensusError as transient", func() {
			consensusErr := NewConsensusError("election", 5, errors.New("term mismatch"))
			dmtErr, action := errorHandler.HandleError(consensusErr, "leader_election", nil)

			So(dmtErr.Category, ShouldEqual, ErrorCategoryTransient)
			So(action, ShouldEqual, ErrorActionRetry)
		})

		Convey("Should categorize context errors as transient", func() {
			dmtErr, action := errorHandler.HandleError(context.Canceled, "operation", nil)

			So(dmtErr.Category, ShouldEqual, ErrorCategoryTransient)
			So(action, ShouldEqual, ErrorActionRetry)
		})
	})
}

func TestExecuteWithCircuitBreaker(t *testing.T) {
	Convey("Execute With Circuit Breaker", t, func() {
		var buf bytes.Buffer
		logger := TestLogger("test-node", &buf)
		metrics := NewMetrics()
		errorHandler := NewErrorHandler(logger, metrics, "test-node")

		Convey("Should execute successfully when circuit is closed", func() {
			executed := false
			err := errorHandler.ExecuteWithCircuitBreaker("test_op", func() error {
				executed = true
				return nil
			})

			So(err, ShouldBeNil)
			So(executed, ShouldBeTrue)
		})

		Convey("Should fail fast when circuit is open", func() {
			// Open the circuit by recording failures
			for i := 0; i < 5; i++ {
				errorHandler.circuitBreaker.RecordFailure()
			}

			executed := false
			err := errorHandler.ExecuteWithCircuitBreaker("test_op", func() error {
				executed = true
				return nil
			})

			So(err, ShouldNotBeNil)
			So(executed, ShouldBeFalse)
			So(err.Error(), ShouldContainSubstring, "circuit breaker is open")
		})
	})
}
