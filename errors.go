/*
package dmt implements comprehensive error handling and categorization for the distributed key-value store.
It provides structured error types, retry logic, and proper error handling strategies for different
categories of errors that can occur in a distributed system.
*/
package dmt

import (
	"context"
	"errors"
	"fmt"
	"math"
	"math/rand"
	"strings"
	"sync"
	"time"
)

/*
ErrorCategory represents the type of error for appropriate handling strategy.
Different categories require different response strategies in a distributed system.
*/
type ErrorCategory int

const (
	// ErrorCategoryFatal represents unrecoverable errors that require system shutdown
	ErrorCategoryFatal ErrorCategory = iota
	// ErrorCategoryTransient represents temporary errors that can be retried
	ErrorCategoryTransient
	// ErrorCategoryLogic represents application logic errors that should be rejected
	ErrorCategoryLogic
)

/*
DMTError represents a categorized error with context information.
It provides structured error handling with category, context, and retry information.
*/
type DMTError struct {
	Category   ErrorCategory
	Operation  string
	Context    map[string]any
	Underlying error
	Retryable  bool
	RetryAfter time.Duration
	NodeID     string
	RequestID  string
}

/*
Error implements the error interface for DMTError.
*/
func (e *DMTError) Error() string {
	if e.Underlying != nil {
		return fmt.Sprintf("[%s] %s: %v", e.Operation, e.categoryString(), e.Underlying)
	}
	return fmt.Sprintf("[%s] %s error", e.Operation, e.categoryString())
}

/*
Unwrap returns the underlying error for error unwrapping.
*/
func (e *DMTError) Unwrap() error {
	return e.Underlying
}

/*
categoryString returns a string representation of the error category.
*/
func (e *DMTError) categoryString() string {
	switch e.Category {
	case ErrorCategoryFatal:
		return "FATAL"
	case ErrorCategoryTransient:
		return "TRANSIENT"
	case ErrorCategoryLogic:
		return "LOGIC"
	default:
		return "UNKNOWN"
	}
}

/*
ErrorHandler provides centralized error handling with categorization and retry logic.
It implements different strategies for different types of errors and includes
circuit breaker functionality to prevent cascading failures.
*/
type ErrorHandler struct {
	logger         Logger
	metrics        *Metrics
	nodeID         string
	circuitBreaker *CircuitBreaker
}

/*
CircuitBreakerState represents the state of a circuit breaker.
*/
type CircuitBreakerState int

const (
	// CircuitBreakerClosed - normal operation, requests pass through
	CircuitBreakerClosed CircuitBreakerState = iota
	// CircuitBreakerOpen - circuit is open, requests fail fast
	CircuitBreakerOpen
	// CircuitBreakerHalfOpen - testing if service has recovered
	CircuitBreakerHalfOpen
)

/*
CircuitBreaker implements the circuit breaker pattern to prevent cascading failures.
*/
type CircuitBreaker struct {
	state            CircuitBreakerState
	failureCount     int
	successCount     int
	lastFailureTime  time.Time
	failureThreshold int
	recoveryTimeout  time.Duration
	mutex            sync.RWMutex
}

/*
NewCircuitBreaker creates a new circuit breaker with default settings.
*/
func NewCircuitBreaker() *CircuitBreaker {
	return &CircuitBreaker{
		state:            CircuitBreakerClosed,
		failureThreshold: 5,
		recoveryTimeout:  30 * time.Second,
	}
}

/*
NewErrorHandler creates a new error handler with the provided logger and metrics.
*/
func NewErrorHandler(logger Logger, metrics *Metrics, nodeID string) *ErrorHandler {
	return &ErrorHandler{
		logger:         logger,
		metrics:        metrics,
		nodeID:         nodeID,
		circuitBreaker: NewCircuitBreaker(),
	}
}

/*
ErrorAction represents the action to take in response to an error.
*/
type ErrorAction int

const (
	// ErrorActionRetry indicates the operation should be retried
	ErrorActionRetry ErrorAction = iota
	// ErrorActionReject indicates the operation should be rejected
	ErrorActionReject
	// ErrorActionShutdown indicates the system should shut down
	ErrorActionShutdown
	// ErrorActionIgnore indicates the error can be safely ignored
	ErrorActionIgnore
)

/*
HandleError processes an error and returns the appropriate action to take.
It categorizes the error, logs it appropriately, updates metrics, and determines
the correct response strategy.
*/
func (eh *ErrorHandler) HandleError(err error, operation string, context map[string]any) (*DMTError, ErrorAction) {
	if err == nil {
		return nil, ErrorActionIgnore
	}

	// Check if it's already a DMTError
	if dmtErr, ok := err.(*DMTError); ok {
		eh.logError(dmtErr)
		eh.updateMetrics(dmtErr)
		return dmtErr, eh.getActionForCategory(dmtErr.Category)
	}

	// Categorize the error
	category := eh.categorizeError(err, operation)

	dmtErr := &DMTError{
		Category:   category,
		Operation:  operation,
		Context:    context,
		Underlying: err,
		NodeID:     eh.nodeID,
		Retryable:  category == ErrorCategoryTransient,
	}

	// Set retry delay for transient errors
	if category == ErrorCategoryTransient {
		dmtErr.RetryAfter = eh.calculateRetryDelay(operation, 1)
	}

	eh.logError(dmtErr)
	eh.updateMetrics(dmtErr)

	return dmtErr, eh.getActionForCategory(category)
}

/*
categorizeError determines the category of an error based on its type and context.
It uses both error type inspection and string pattern matching for robust categorization.
*/
func (eh *ErrorHandler) categorizeError(err error, operation string) ErrorCategory {
	// First check for specific error types
	if category := eh.categorizeByType(err); category != ErrorCategoryLogic {
		return category
	}

	// Fall back to string-based categorization
	errStr := strings.ToLower(err.Error())

	// Fatal errors - system corruption, disk full, etc.
	if eh.isFatalError(errStr) {
		return ErrorCategoryFatal
	}

	// Transient errors - network issues, temporary unavailability
	if eh.isTransientError(errStr, operation) {
		return ErrorCategoryTransient
	}

	// Default to logic error
	return ErrorCategoryLogic
}

/*
categorizeByType attempts to categorize errors based on their concrete types.
This provides more reliable categorization than string matching alone.
*/
func (eh *ErrorHandler) categorizeByType(err error) ErrorCategory {
	// Check for context errors (always transient)
	if err == context.Canceled || err == context.DeadlineExceeded {
		return ErrorCategoryTransient
	}

	// Check for our custom error types
	switch err.(type) {
	case *NetworkError:
		return ErrorCategoryTransient // Network errors are usually transient
	case *ConsensusError:
		return ErrorCategoryTransient // Consensus errors are usually transient
	case *PersistenceError:
		// Persistence errors could be fatal (disk full) or transient (lock contention)
		// Fall back to string-based categorization
		return ErrorCategoryLogic
	}

	// Check for wrapped errors
	if unwrapped := errors.Unwrap(err); unwrapped != nil {
		return eh.categorizeByType(unwrapped)
	}

	return ErrorCategoryLogic // Default when type-based categorization fails
}

/*
isFatalError checks if an error is fatal and requires system shutdown.
*/
func (eh *ErrorHandler) isFatalError(errStr string) bool {
	fatalPatterns := []string{
		"no space left on device",
		"disk full",
		"corruption detected",
		"checksum mismatch",
		"invalid magic number",
		"database corrupted",
		"out of memory",
		"segmentation fault",
	}

	for _, pattern := range fatalPatterns {
		if strings.Contains(strings.ToLower(errStr), pattern) {
			return true
		}
	}
	return false
}

/*
isTransientError checks if an error is transient and can be retried.
*/
func (eh *ErrorHandler) isTransientError(errStr string, operation string) bool {
	transientPatterns := []string{
		"connection refused",
		"connection reset",
		"timeout",
		"temporary failure",
		"network unreachable",
		"host unreachable",
		"no route to host",
		"broken pipe",
		"connection timed out",
		"dial tcp",
		"context deadline exceeded",
		"context canceled",
		"peer unavailable",
		"leader election in progress",
		"not leader",
	}

	for _, pattern := range transientPatterns {
		if strings.Contains(strings.ToLower(errStr), pattern) {
			return true
		}
	}

	// Operation-specific transient errors
	switch operation {
	case "replication", "sync", "heartbeat":
		// Network operations are often transient
		lowerErr := strings.ToLower(errStr)
		return strings.Contains(lowerErr, "failed") || strings.Contains(lowerErr, "error")
	case "persistence":
		// Some persistence errors are transient
		lowerErr := strings.ToLower(errStr)
		return strings.Contains(lowerErr, "lock") || strings.Contains(lowerErr, "busy")
	}

	return false
}

/*
getActionForCategory returns the appropriate action for an error category.
*/
func (eh *ErrorHandler) getActionForCategory(category ErrorCategory) ErrorAction {
	switch category {
	case ErrorCategoryFatal:
		return ErrorActionShutdown
	case ErrorCategoryTransient:
		return ErrorActionRetry
	case ErrorCategoryLogic:
		return ErrorActionReject
	default:
		return ErrorActionReject
	}
}

/*
logError logs an error with appropriate level and context.
*/
func (eh *ErrorHandler) logError(err *DMTError) {
	fields := map[string]any{
		"category":  err.categoryString(),
		"operation": err.Operation,
		"node_id":   err.NodeID,
		"retryable": err.Retryable,
	}

	// Add context fields
	for k, v := range err.Context {
		fields[k] = v
	}

	// Add request ID if available
	if err.RequestID != "" {
		fields["request_id"] = err.RequestID
	}

	switch err.Category {
	case ErrorCategoryFatal:
		eh.logger.Error("Fatal error occurred", "error", err.Underlying, "fields", fields)
	case ErrorCategoryTransient:
		eh.logger.Warn("Transient error occurred", "error", err.Underlying, "fields", fields)
	case ErrorCategoryLogic:
		eh.logger.Error("Logic error occurred", "error", err.Underlying, "fields", fields)
	}
}

/*
updateMetrics updates error metrics based on the error category and operation.
*/
func (eh *ErrorHandler) updateMetrics(err *DMTError) {
	if eh.metrics != nil {
		eh.metrics.RecordError(err.Operation, err.categoryString())

		// Record circuit breaker state
		if eh.circuitBreaker != nil {
			state := eh.circuitBreaker.GetState()
			eh.metrics.RecordCircuitBreakerState(err.Operation, int(state))
		}

		// Record additional context metrics if available
		if err.Context != nil {
			if retryAttempt, ok := err.Context["attempt"].(int); ok {
				eh.metrics.RecordRetryAttempt(err.Operation, retryAttempt)
			}
		}
	}
}

/*
RetryConfig holds configuration for retry logic.
*/
type RetryConfig struct {
	MaxRetries    int
	BaseDelay     time.Duration
	MaxDelay      time.Duration
	BackoffFactor float64
	JitterEnabled bool
}

/*
DefaultRetryConfig returns a default retry configuration.
*/
func DefaultRetryConfig() RetryConfig {
	return RetryConfig{
		MaxRetries:    5,
		BaseDelay:     100 * time.Millisecond,
		MaxDelay:      30 * time.Second,
		BackoffFactor: 2.0,
		JitterEnabled: true,
	}
}

/*
RetryWithBackoff executes a function with exponential backoff retry logic.
It handles transient errors automatically and respects context cancellation.
*/
func (eh *ErrorHandler) RetryWithBackoff(ctx context.Context, operation string, config RetryConfig, fn func() error) error {
	var lastErr error

	for attempt := 0; attempt <= config.MaxRetries; attempt++ {
		// Execute the function
		err := fn()
		if err == nil {
			eh.logSuccessAfterRetry(operation, attempt)
			return nil
		}

		lastErr = err

		// Handle the error and determine if we should retry
		if shouldStop, dmtErr := eh.shouldStopRetrying(err, operation, attempt, config.MaxRetries); shouldStop {
			return dmtErr
		}

		// Wait for delay or context cancellation
		if ctxErr := eh.waitForRetryDelay(ctx, operation, config, attempt, err, lastErr); ctxErr != nil {
			return ctxErr
		}
	}

	return lastErr
}

/*
logSuccessAfterRetry logs successful operation completion after retries.
*/
func (eh *ErrorHandler) logSuccessAfterRetry(operation string, attempt int) {
	if attempt > 0 {
		eh.logger.Info("Operation succeeded after retry",
			"operation", operation,
			"attempts", attempt+1,
			"node_id", eh.nodeID)
	}
}

/*
shouldStopRetrying determines if retry attempts should stop based on error type and attempt count.
*/
func (eh *ErrorHandler) shouldStopRetrying(err error, operation string, attempt, maxRetries int) (bool, *DMTError) {
	dmtErr, action := eh.HandleError(err, operation, map[string]any{
		"attempt":      attempt + 1,
		"max_attempts": maxRetries + 1,
	})

	// Don't retry if not retryable or if we've exhausted attempts
	if action != ErrorActionRetry || attempt >= maxRetries {
		return true, dmtErr
	}

	return false, nil
}

/*
waitForRetryDelay waits for the calculated delay before the next retry attempt.
*/
func (eh *ErrorHandler) waitForRetryDelay(ctx context.Context, operation string, config RetryConfig, attempt int, err, lastErr error) *DMTError {
	delay := eh.calculateRetryDelayWithConfig(config, attempt+1)

	eh.logger.Debug("Retrying operation after delay",
		"operation", operation,
		"attempt", attempt+1,
		"delay", delay,
		"error", err.Error(),
		"node_id", eh.nodeID)

	// Wait for delay or context cancellation
	select {
	case <-ctx.Done():
		return &DMTError{
			Category:   ErrorCategoryLogic,
			Operation:  operation,
			Underlying: ctx.Err(),
			NodeID:     eh.nodeID,
			Context: map[string]any{
				"last_error": lastErr.Error(),
				"attempts":   attempt + 1,
			},
		}
	case <-time.After(delay):
		return nil // Continue to next attempt
	}
}

/*
calculateRetryDelay calculates the delay before the next retry attempt.
*/
func (eh *ErrorHandler) calculateRetryDelay(_ string, attempt int) time.Duration {
	config := DefaultRetryConfig()
	return eh.calculateRetryDelayWithConfig(config, attempt)
}

/*
calculateRetryDelayWithConfig calculates retry delay using the provided configuration.
*/
func (eh *ErrorHandler) calculateRetryDelayWithConfig(config RetryConfig, attempt int) time.Duration {
	// Calculate exponential backoff
	delay := float64(config.BaseDelay) * math.Pow(config.BackoffFactor, float64(attempt-1))

	// Apply maximum delay limit
	if delay > float64(config.MaxDelay) {
		delay = float64(config.MaxDelay)
	}

	// Add jitter if enabled
	if config.JitterEnabled {
		jitter := delay * 0.1 * (2.0*rand.Float64() - 1.0) // ±10% jitter
		delay += jitter
	}

	return time.Duration(delay)
}

/*
WrapError wraps an existing error with additional context and categorization.
*/
func (eh *ErrorHandler) WrapError(err error, operation string, context map[string]any) *DMTError {
	if err == nil {
		return nil
	}

	if dmtErr, ok := err.(*DMTError); ok {
		// Add additional context to existing DMTError
		if dmtErr.Context == nil {
			dmtErr.Context = make(map[string]any)
		}
		for k, v := range context {
			dmtErr.Context[k] = v
		}
		return dmtErr
	}

	category := eh.categorizeError(err, operation)
	return &DMTError{
		Category:   category,
		Operation:  operation,
		Context:    context,
		Underlying: err,
		NodeID:     eh.nodeID,
		Retryable:  category == ErrorCategoryTransient,
	}
}

/*
NewFatalError creates a new fatal error.
*/
func NewFatalError(operation string, err error, context map[string]any) *DMTError {
	return &DMTError{
		Category:   ErrorCategoryFatal,
		Operation:  operation,
		Context:    context,
		Underlying: err,
		Retryable:  false,
	}
}

/*
NewTransientError creates a new transient error.
*/
func NewTransientError(operation string, err error, context map[string]any) *DMTError {
	return &DMTError{
		Category:   ErrorCategoryTransient,
		Operation:  operation,
		Context:    context,
		Underlying: err,
		Retryable:  true,
	}
}

/*
NewLogicError creates a new logic error.
*/
func NewLogicError(operation string, err error, context map[string]any) *DMTError {
	return &DMTError{
		Category:   ErrorCategoryLogic,
		Operation:  operation,
		Context:    context,
		Underlying: err,
		Retryable:  false,
	}
}

/*
CanExecute checks if the circuit breaker allows execution.
*/
func (cb *CircuitBreaker) CanExecute() bool {
	cb.mutex.RLock()
	defer cb.mutex.RUnlock()

	switch cb.state {
	case CircuitBreakerClosed:
		return true
	case CircuitBreakerOpen:
		// Check if recovery timeout has passed
		if time.Since(cb.lastFailureTime) > cb.recoveryTimeout {
			cb.mutex.RUnlock()
			cb.mutex.Lock()
			cb.state = CircuitBreakerHalfOpen
			cb.mutex.Unlock()
			cb.mutex.RLock()
			return true
		}
		return false
	case CircuitBreakerHalfOpen:
		return true
	default:
		return false
	}
}

/*
RecordSuccess records a successful operation.
*/
func (cb *CircuitBreaker) RecordSuccess() {
	cb.mutex.Lock()
	defer cb.mutex.Unlock()

	cb.successCount++
	cb.failureCount = 0

	if cb.state == CircuitBreakerHalfOpen {
		cb.state = CircuitBreakerClosed
	}
}

/*
RecordFailure records a failed operation.
*/
func (cb *CircuitBreaker) RecordFailure() {
	cb.mutex.Lock()
	defer cb.mutex.Unlock()

	cb.failureCount++
	cb.lastFailureTime = time.Now()

	if cb.failureCount >= cb.failureThreshold {
		cb.state = CircuitBreakerOpen
	}
}

/*
GetState returns the current circuit breaker state.
*/
func (cb *CircuitBreaker) GetState() CircuitBreakerState {
	cb.mutex.RLock()
	defer cb.mutex.RUnlock()
	return cb.state
}

/*
ExecuteWithCircuitBreaker executes a function with circuit breaker protection.
*/
func (eh *ErrorHandler) ExecuteWithCircuitBreaker(operation string, fn func() error) error {
	if !eh.circuitBreaker.CanExecute() {
		return NewTransientError(operation,
			fmt.Errorf("circuit breaker is open for operation: %s", operation),
			map[string]any{"circuit_breaker_state": "open"})
	}

	err := fn()
	if err != nil {
		eh.circuitBreaker.RecordFailure()
		return err
	}

	eh.circuitBreaker.RecordSuccess()
	return nil
}

/*
Predefined error types for common distributed system scenarios.
*/

/*
NetworkError represents network-related errors.
*/
type NetworkError struct {
	Operation  string
	Peer       string
	Underlying error
}

func (e *NetworkError) Error() string {
	return fmt.Sprintf("network error in %s with peer %s: %v", e.Operation, e.Peer, e.Underlying)
}

func (e *NetworkError) Unwrap() error {
	return e.Underlying
}

/*
ConsensusError represents consensus-related errors.
*/
type ConsensusError struct {
	Operation  string
	Term       uint64
	Underlying error
}

func (e *ConsensusError) Error() string {
	return fmt.Sprintf("consensus error in %s at term %d: %v", e.Operation, e.Term, e.Underlying)
}

func (e *ConsensusError) Unwrap() error {
	return e.Underlying
}

/*
PersistenceError represents persistence-related errors.
*/
type PersistenceError struct {
	Operation  string
	Path       string
	Underlying error
}

func (e *PersistenceError) Error() string {
	return fmt.Sprintf("persistence error in %s at path %s: %v", e.Operation, e.Path, e.Underlying)
}

func (e *PersistenceError) Unwrap() error {
	return e.Underlying
}

/*
Helper functions to create specific error types.
*/

/*
NewNetworkError creates a new network error.
*/
func NewNetworkError(operation, peer string, err error) *NetworkError {
	return &NetworkError{
		Operation:  operation,
		Peer:       peer,
		Underlying: err,
	}
}

/*
NewConsensusError creates a new consensus error.
*/
func NewConsensusError(operation string, term uint64, err error) *ConsensusError {
	return &ConsensusError{
		Operation:  operation,
		Term:       term,
		Underlying: err,
	}
}

/*
NewPersistenceError creates a new persistence error.
*/
func NewPersistenceError(operation, path string, err error) *PersistenceError {
	return &PersistenceError{
		Operation:  operation,
		Path:       path,
		Underlying: err,
	}
}
