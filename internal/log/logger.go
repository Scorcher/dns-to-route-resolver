package log

import (
	"io"
	"os"
	"strings"
	"time"

	"github.com/rs/zerolog"
)

// Logger is a wrapper around zerolog.Logger
type Logger struct {
	logger zerolog.Logger
}

// NewLogger creates a new logger instance
func NewLogger() *Logger {
	// Default output to stderr
	output := zerolog.ConsoleWriter{
		Out:        os.Stderr,
		TimeFormat: time.RFC3339,
	}

	// Create logger with caller information
	logger := zerolog.New(output).
		With().
		Timestamp().
		Caller().
		Logger()

	return &Logger{logger: logger}
}

// SetLevel sets the log level
func (l *Logger) SetLevel(level zerolog.Level) {
	l.logger = l.logger.Level(level)
}

// SetOutput sets the output destination for the logger
func (l *Logger) SetOutput(w io.Writer) {
	l.logger = l.logger.Output(w)
}

// ParseLevel parses a log level string into a zerolog.Level
func ParseLevel(level string) (zerolog.Level, error) {
	switch strings.ToLower(level) {
	case "debug":
		return zerolog.DebugLevel, nil
	case "info":
		return zerolog.InfoLevel, nil
	case "warn", "warning":
		return zerolog.WarnLevel, nil
	case "error":
		return zerolog.ErrorLevel, nil
	case "fatal":
		return zerolog.FatalLevel, nil
	case "panic":
		return zerolog.PanicLevel, nil
	case "disabled":
		return zerolog.Disabled, nil
	default:
		return zerolog.InfoLevel, nil
	}
}

// Debug logs a debug message
func (l *Logger) Debug(msg string) {
	l.logger.Debug().Msg(msg)
}

// Debugf logs a formatted debug message
func (l *Logger) Debugf(format string, v ...interface{}) {
	l.logger.Debug().Msgf(format, v...)
}

// Info logs an info message
func (l *Logger) Info(msg string) {
	l.logger.Info().Msg(msg)
}

// Infof logs a formatted info message
func (l *Logger) Infof(format string, v ...interface{}) {
	l.logger.Info().Msgf(format, v...)
}

// Warn logs a warning message
func (l *Logger) Warn(msg string) {
	l.logger.Warn().Msg(msg)
}

// Warnf logs a formatted warning message
func (l *Logger) Warnf(format string, v ...interface{}) {
	l.logger.Warn().Msgf(format, v...)
}

// Error logs an error message
func (l *Logger) Error(msg string) {
	l.logger.Error().Msg(msg)
}

// Errorf logs a formatted error message
func (l *Logger) Errorf(format string, v ...interface{}) {
	l.logger.Error().Msgf(format, v...)
}

// Fatal logs a fatal message and exits the program
func (l *Logger) Fatal(msg string) {
	l.logger.Fatal().Msg(msg)
}

// Fatalf logs a formatted fatal message and exits the program
func (l *Logger) Fatalf(format string, v ...interface{}) {
	l.logger.Fatal().Msgf(format, v...)
}

// WithField adds a field to the logger
func (l *Logger) WithField(key string, value interface{}) *Logger {
	return &Logger{
		logger: l.logger.With().Interface(key, value).Logger(),
	}
}

// WithError adds an error field to the logger
func (l *Logger) WithError(err error) *Logger {
	return &Logger{
		logger: l.logger.With().Err(err).Logger(),
	}
}

// Global logger instance
var (
	globalLogger = NewLogger()
)

// SetGlobalLogger sets the global logger instance
func SetGlobalLogger(logger *Logger) {
	globalLogger = logger
}

// GetLogger returns the global logger instance
func GetLogger() *Logger {
	return globalLogger
}

// SetLevel sets the log level for the global logger
func SetLevel(level zerolog.Level) {
	globalLogger.SetLevel(level)
}

// Debug logs a debug message using the global logger
func Debug(msg string) {
	globalLogger.Debug(msg)
}

// Debugf logs a formatted debug message using the global logger
func Debugf(format string, v ...interface{}) {
	globalLogger.Debugf(format, v...)
}

// Info logs an info message using the global logger
func Info(msg string) {
	globalLogger.Info(msg)
}

// Infof logs a formatted info message using the global logger
func Infof(format string, v ...interface{}) {
	globalLogger.Infof(format, v...)
}

// Warn logs a warning message using the global logger
func Warn(msg string) {
	globalLogger.Warn(msg)
}

// Warnf logs a formatted warning message using the global logger
func Warnf(format string, v ...interface{}) {
	globalLogger.Warnf(format, v...)
}

// Error logs an error message using the global logger
func Error(msg string) {
	globalLogger.Error(msg)
}

// Errorf logs a formatted error message using the global logger
func Errorf(format string, v ...interface{}) {
	globalLogger.Errorf(format, v...)
}

// Fatal logs a fatal message and exits the program using the global logger
func Fatal(msg string) {
	globalLogger.Fatal(msg)
}

// Fatalf logs a formatted fatal message and exits the program using the global logger
func Fatalf(format string, v ...interface{}) {
	globalLogger.Fatalf(format, v...)
}

// WithField adds a field to the global logger and returns a new logger
func WithField(key string, value interface{}) *Logger {
	return globalLogger.WithField(key, value)
}

// WithError adds an error field to the global logger and returns a new logger
func WithError(err error) *Logger {
	return globalLogger.WithError(err)
}
