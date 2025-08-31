package logger

import (
	"fmt"
	"os"
	"regexp"
	"time"

	"github.com/rs/zerolog"
)

type Logger struct {
	*zerolog.Logger
	component string
}

// LogLevel represents our custom log levels
type LogLevel string

const (
	LevelDebug   LogLevel = "DEBUG"
	LevelInfo    LogLevel = "INFO"
	LevelSuccess LogLevel = "SUCCESS"
	LevelWarn    LogLevel = "WARN"
	LevelError   LogLevel = "ERROR"
	LevelFatal   LogLevel = "FATAL"
)

var (
	// Global log levels for different environments
	logLevel = map[string]zerolog.Level{
		"development": zerolog.DebugLevel,
		"staging":     zerolog.InfoLevel,
		"production":  zerolog.InfoLevel,
	}
)

// Config represents logger configuration
type Config struct {
	IsProduction bool
	AppEnv       string
}

// New creates a new logger instance for a specific component
func New(component string) *Logger {
	return NewWithConfig(component, Config{
		IsProduction: os.Getenv("APP_ENV") == "production",
		AppEnv:       os.Getenv("APP_ENV"),
	})
}

// NewWithConfig creates a new logger instance with custom configuration
func NewWithConfig(component string, config Config) *Logger {
	// Configure zerolog
	zerolog.TimeFieldFormat = time.RFC3339

	// Create console writer with color and custom format
	output := zerolog.ConsoleWriter{
		Out: os.Stdout,
		FormatMessage: func(i interface{}) string {
			return fmt.Sprintf("[%s] %s", component, i)
		},
		FormatLevel: func(i interface{}) string {
			if level, ok := i.(string); ok {
				// Always use colors for console output
				switch level {
				case "debug":
					return "\033[36m[DEBUG]\033[0m" // Cyan
				case "info":
					return "\033[34m[INFO]\033[0m" // Blue
				case "success":
					return "\033[32m[SUCCESS]\033[0m" // Green
				case "warn":
					return "\033[33m[WARN]\033[0m" // Yellow
				case "error":
					return "\033[31m[ERROR]\033[0m" // Red
				case "fatal":
					return "\033[35m[FATAL]\033[0m" // Purple
				default:
					return fmt.Sprintf("[%s]", level)
				}
			}
			return "???"
		},
	}

	// Remove timestamp in production
	if config.IsProduction {
		output.TimeFormat = ""
	} else {
		output.TimeFormat = "2006-01-02 15:04:05"
	}

	// Create logger
	var logger zerolog.Logger
	if config.IsProduction {
		// No timestamp in production
		logger = zerolog.New(output).Level(getLogLevel(config.AppEnv))
	} else {
		// Include timestamp in non-production
		logger = zerolog.New(output).
			Level(getLogLevel(config.AppEnv)).
			With().
			Timestamp().
			Logger()
	}

	return &Logger{
		Logger:    &logger,
		component: component,
	}
}

// getLogLevel returns the appropriate log level based on environment
func getLogLevel(env string) zerolog.Level {
	if level, exists := logLevel[env]; exists {
		return level
	}
	return zerolog.DebugLevel
}

// Legacy methods for backward compatibility
func (l *Logger) Debug() *zerolog.Event   { return l.Logger.Debug() }
func (l *Logger) Info() *zerolog.Event    { return l.Logger.Info() }
func (l *Logger) Success() *zerolog.Event { return l.Logger.Info().Str("level", "success") }
func (l *Logger) Warn() *zerolog.Event    { return l.Logger.Warn() }
func (l *Logger) Error() *zerolog.Event   { return l.Logger.Error() }

// Simple logging methods
func (l *Logger) LogDebug(msg string) {
	l.Debug().Msg(msg)
}

func (l *Logger) LogInfo(msg string) {
	l.Info().Msg(msg)
}

func (l *Logger) LogSuccess(msg string) {
	l.Success().Msg(msg)
}

func (l *Logger) LogWarn(msg string) {
	l.Warn().Msg(msg)
}

func (l *Logger) LogError(msg string, err error) {
	if err != nil {
		l.Error().Err(err).Msg(msg)
		return
	}
	l.Error().Msg(msg)
}

func (l *Logger) LogFatal(msg string, err error) {
	if err != nil {
		l.Fatal().Err(err).Msg(msg)
		return
	}
	l.Fatal().Msg(msg)
}

// Formatted logging methods with variable arguments
func (l *Logger) LogDebugf(format string, v ...interface{}) {
	l.Debug().Msgf(format, v...)
}

func (l *Logger) LogInfof(format string, v ...interface{}) {
	l.Info().Msgf(format, v...)
}

func (l *Logger) LogSuccessf(format string, v ...interface{}) {
	l.Success().Msgf(format, v...)
}

func (l *Logger) LogWarnf(format string, v ...interface{}) {
	l.Warn().Msgf(format, v...)
}

// Fixed error formatting - handles both patterns correctly
func (l *Logger) LogErrorf(format string, v ...interface{}) {
	// Handle the common pattern where error is passed as part of v
	l.Error().Msgf(format, v...)
}

func (l *Logger) LogFatalf(format string, v ...interface{}) {
	// Handle the common pattern where error is passed as part of v
	l.Fatal().Msgf(format, v...)
}

// WithFields adds fields to the log event
func (l *Logger) WithFields(fields map[string]interface{}) *zerolog.Event {
	event := l.Info()
	for k, v := range fields {
		event = event.Interface(k, v)
	}
	return event
}

// DebugWithFields adds fields to the log event
func (l *Logger) DebugWithFields(fields map[string]interface{}) *zerolog.Event {
	event := l.Debug()
	for k, v := range fields {
		event = event.Interface(k, v)
	}
	return event
}

// ErrorWithFields adds fields to the log event
func (l *Logger) ErrorWithFields(fields map[string]interface{}) *zerolog.Event {
	event := l.Error()
	for k, v := range fields {
		event = event.Interface(k, v)
	}
	return event
}

// StripANSI removes ANSI color codes from a string
// This is useful when you need to ensure a string doesn't contain color codes
// before sending it in an HTTP response
func StripANSI(str string) string {
	// ANSI color code pattern: ESC[ ... m
	// ESC is the escape character \033 (octal) or \x1B (hex)
	// This regex matches all ANSI color/style sequences
	ansiPattern := regexp.MustCompile("\x1B\\[[0-9;]*[a-zA-Z]")
	return ansiPattern.ReplaceAllString(str, "")
}
