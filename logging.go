/*
package dmt implements structured logging functionality for the distributed key-value store.
It provides context-aware logging with different levels, request tracking, and node information
to facilitate debugging and monitoring of distributed system operations.
*/
package dmt

import (
	"context"
	"fmt"
	"io"
	"os"
	"sync"
	"time"
)

/*
LogLevel represents the severity level of a log message.
Different levels allow filtering and appropriate handling of log messages.
*/
type LogLevel int

const (
	// LogLevelDebug for detailed debugging information
	LogLevelDebug LogLevel = iota
	// LogLevelInfo for general informational messages
	LogLevelInfo
	// LogLevelWarn for warning messages that don't stop execution
	LogLevelWarn
	// LogLevelError for error messages that indicate problems
	LogLevelError
	// LogLevelFatal for fatal errors that require system shutdown
	LogLevelFatal
)

/*
String returns the string representation of a log level.
*/
func (l LogLevel) String() string {
	switch l {
	case LogLevelDebug:
		return "DEBUG"
	case LogLevelInfo:
		return "INFO"
	case LogLevelWarn:
		return "WARN"
	case LogLevelError:
		return "ERROR"
	case LogLevelFatal:
		return "FATAL"
	default:
		return "UNKNOWN"
	}
}

/*
Logger interface defines the logging contract for the DMT system.
It provides structured logging with context and different severity levels.
*/
type Logger interface {
	Debug(msg string, keysAndValues ...interface{})
	Info(msg string, keysAndValues ...interface{})
	Warn(msg string, keysAndValues ...interface{})
	Error(msg string, keysAndValues ...interface{})
	Fatal(msg string, keysAndValues ...interface{})
	WithContext(ctx context.Context) Logger
	WithFields(fields map[string]interface{}) Logger
	SetLevel(level LogLevel)
}

/*
StructuredLogger implements the Logger interface with structured logging capabilities.
It provides context-aware logging with request IDs, node information, and structured fields.
*/
type StructuredLogger struct {
	level      LogLevel
	output     io.Writer
	nodeID     string
	fields     map[string]interface{}
	mutex      sync.RWMutex
	timeFormat string
}

/*
LoggerConfig holds configuration for creating a structured logger.
*/
type LoggerConfig struct {
	Level      LogLevel
	Output     io.Writer
	NodeID     string
	TimeFormat string
}

/*
NewStructuredLogger creates a new structured logger with the provided configuration.
*/
func NewStructuredLogger(config LoggerConfig) *StructuredLogger {
	if config.Output == nil {
		config.Output = os.Stdout
	}
	if config.TimeFormat == "" {
		config.TimeFormat = time.RFC3339
	}

	return &StructuredLogger{
		level:      config.Level,
		output:     config.Output,
		nodeID:     config.NodeID,
		fields:     make(map[string]interface{}),
		timeFormat: config.TimeFormat,
	}
}

/*
Debug logs a debug message with optional key-value pairs.
*/
func (l *StructuredLogger) Debug(msg string, keysAndValues ...interface{}) {
	l.log(LogLevelDebug, msg, keysAndValues...)
}

/*
Info logs an info message with optional key-value pairs.
*/
func (l *StructuredLogger) Info(msg string, keysAndValues ...interface{}) {
	l.log(LogLevelInfo, msg, keysAndValues...)
}

/*
Warn logs a warning message with optional key-value pairs.
*/
func (l *StructuredLogger) Warn(msg string, keysAndValues ...interface{}) {
	l.log(LogLevelWarn, msg, keysAndValues...)
}

/*
Error logs an error message with optional key-value pairs.
*/
func (l *StructuredLogger) Error(msg string, keysAndValues ...interface{}) {
	l.log(LogLevelError, msg, keysAndValues...)
}

/*
Fatal logs a fatal message with optional key-value pairs and exits the program.
*/
func (l *StructuredLogger) Fatal(msg string, keysAndValues ...interface{}) {
	l.log(LogLevelFatal, msg, keysAndValues...)
	os.Exit(1)
}

/*
WithContext returns a new logger with context information extracted from the context.
It looks for request IDs and other contextual information.
*/
func (l *StructuredLogger) WithContext(ctx context.Context) Logger {
	newLogger := l.clone()

	// Extract request ID from context if available
	if requestID := getRequestIDFromContext(ctx); requestID != "" {
		newLogger.fields["request_id"] = requestID
	}

	// Extract operation from context if available
	if operation := getOperationFromContext(ctx); operation != "" {
		newLogger.fields["operation"] = operation
	}

	return newLogger
}

/*
WithFields returns a new logger with additional fields.
*/
func (l *StructuredLogger) WithFields(fields map[string]interface{}) Logger {
	newLogger := l.clone()
	for k, v := range fields {
		newLogger.fields[k] = v
	}
	return newLogger
}

/*
SetLevel sets the minimum log level for this logger.
*/
func (l *StructuredLogger) SetLevel(level LogLevel) {
	l.mutex.Lock()
	defer l.mutex.Unlock()
	l.level = level
}

/*
log is the internal logging method that formats and writes log messages.
*/
func (l *StructuredLogger) log(level LogLevel, msg string, keysAndValues ...interface{}) {
	l.mutex.RLock()
	currentLevel := l.level
	l.mutex.RUnlock()

	// Skip if level is below threshold
	if level < currentLevel {
		return
	}

	// Build log entry
	entry := l.buildLogEntry(level, msg, keysAndValues...)

	// Write to output
	l.mutex.Lock()
	defer l.mutex.Unlock()
	fmt.Fprintln(l.output, entry)
}

/*
buildLogEntry constructs a structured log entry with timestamp, level, message, and fields.
*/
func (l *StructuredLogger) buildLogEntry(level LogLevel, msg string, keysAndValues ...interface{}) string {
	timestamp := time.Now().Format(l.timeFormat)

	// Start with basic fields
	entry := fmt.Sprintf("time=%s level=%s msg=\"%s\"", timestamp, level.String(), msg)

	// Add node ID if available
	if l.nodeID != "" {
		entry += fmt.Sprintf(" node_id=%s", l.nodeID)
	}

	// Add persistent fields
	for k, v := range l.fields {
		entry += fmt.Sprintf(" %s=%v", k, formatValue(v))
	}

	// Add key-value pairs from the log call
	fields := parseKeysAndValues(keysAndValues...)
	for k, v := range fields {
		entry += fmt.Sprintf(" %s=%v", k, formatValue(v))
	}

	return entry
}

/*
clone creates a copy of the logger with the same configuration but separate fields.
*/
func (l *StructuredLogger) clone() *StructuredLogger {
	l.mutex.RLock()
	defer l.mutex.RUnlock()

	newFields := make(map[string]interface{})
	for k, v := range l.fields {
		newFields[k] = v
	}

	return &StructuredLogger{
		level:      l.level,
		output:     l.output,
		nodeID:     l.nodeID,
		fields:     newFields,
		timeFormat: l.timeFormat,
	}
}

/*
parseKeysAndValues converts a slice of alternating keys and values into a map.
*/
func parseKeysAndValues(keysAndValues ...interface{}) map[string]interface{} {
	fields := make(map[string]interface{})

	for i := 0; i < len(keysAndValues); i += 2 {
		if i+1 < len(keysAndValues) {
			key, ok := keysAndValues[i].(string)
			if ok {
				fields[key] = keysAndValues[i+1]
			}
		}
	}

	return fields
}

/*
formatValue formats a value for logging output.
*/
func formatValue(v interface{}) string {
	switch val := v.(type) {
	case string:
		return fmt.Sprintf("\"%s\"", val)
	case error:
		return fmt.Sprintf("\"%s\"", val.Error())
	case time.Duration:
		return val.String()
	case time.Time:
		return val.Format(time.RFC3339)
	default:
		return fmt.Sprintf("%v", val)
	}
}

/*
Context keys for extracting information from context.
*/
type contextKey string

const (
	requestIDKey contextKey = "request_id"
	operationKey contextKey = "operation"
)

/*
WithRequestID adds a request ID to the context.
*/
func WithRequestID(ctx context.Context, requestID string) context.Context {
	return context.WithValue(ctx, requestIDKey, requestID)
}

/*
WithOperation adds an operation name to the context.
*/
func WithOperation(ctx context.Context, operation string) context.Context {
	return context.WithValue(ctx, operationKey, operation)
}

/*
getRequestIDFromContext extracts the request ID from context.
*/
func getRequestIDFromContext(ctx context.Context) string {
	if requestID, ok := ctx.Value(requestIDKey).(string); ok {
		return requestID
	}
	return ""
}

/*
getOperationFromContext extracts the operation name from context.
*/
func getOperationFromContext(ctx context.Context) string {
	if operation, ok := ctx.Value(operationKey).(string); ok {
		return operation
	}
	return ""
}

/*
NoOpLogger is a logger implementation that discards all log messages.
Useful for testing or when logging is disabled.
*/
type NoOpLogger struct{}

/*
NewNoOpLogger creates a new no-op logger.
*/
func NewNoOpLogger() *NoOpLogger {
	return &NoOpLogger{}
}

/*
Debug does nothing in the no-op logger.
*/
func (l *NoOpLogger) Debug(msg string, keysAndValues ...interface{}) {}

/*
Info does nothing in the no-op logger.
*/
func (l *NoOpLogger) Info(msg string, keysAndValues ...interface{}) {}

/*
Warn does nothing in the no-op logger.
*/
func (l *NoOpLogger) Warn(msg string, keysAndValues ...interface{}) {}

/*
Error does nothing in the no-op logger.
*/
func (l *NoOpLogger) Error(msg string, keysAndValues ...interface{}) {}

/*
Fatal does nothing in the no-op logger.
*/
func (l *NoOpLogger) Fatal(msg string, keysAndValues ...interface{}) {}

/*
WithContext returns the same no-op logger.
*/
func (l *NoOpLogger) WithContext(ctx context.Context) Logger {
	return l
}

/*
WithFields returns the same no-op logger.
*/
func (l *NoOpLogger) WithFields(fields map[string]interface{}) Logger {
	return l
}

/*
SetLevel does nothing in the no-op logger.
*/
func (l *NoOpLogger) SetLevel(level LogLevel) {}

/*
DefaultLogger creates a default logger for the DMT system.
*/
func DefaultLogger(nodeID string) Logger {
	return NewStructuredLogger(LoggerConfig{
		Level:      LogLevelInfo,
		Output:     os.Stdout,
		NodeID:     nodeID,
		TimeFormat: time.RFC3339,
	})
}

/*
TestLogger creates a logger suitable for testing that writes to a buffer.
*/
func TestLogger(nodeID string, output io.Writer) Logger {
	return NewStructuredLogger(LoggerConfig{
		Level:      LogLevelDebug,
		Output:     output,
		NodeID:     nodeID,
		TimeFormat: time.RFC3339,
	})
}
