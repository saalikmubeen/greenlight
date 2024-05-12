package jsonlog

import (
	"encoding/json"
	"io"
	"os"
	"runtime/debug"
	"sync"
	"time"
)

// Or simply use a third party logger package like zerolog:
// https://github.com/rs/zerolog

// Level represents the severity level of a log entry.
// In this project we will use the following three severity
// levels, ordered from least to most severe:
type Level int8

// Initialize constants which represent a specific severity level using the "iota" keyword
// as a shortcut to assign successive integer values to the constants.
// Could be extended to support additional severity levels such as DEBUG and WARNING.
const (
	LevelInfo  Level = iota // Has the value of 0.
	LevelError              // Has the value of 1.
	LevelFatal              // Has the value of 2.
	LevelOff                // Has the value of 3.
)

// String returns a human-friendly string for the severity level.
func (l Level) String() string {
	switch l {
	case LevelInfo:
		return "INFO"
	case LevelError:
		return "ERROR"
	case LevelFatal:
		return "FATAL"
	default:
		return ""
	}
}

// Logger is the custom logger. It holds the output destination that the log entries will be
// written to, the minimum severity level that log entries will be written for, and a mutex
// for coordination the writes.
type Logger struct {
	out      io.Writer // The output destination for the log entries.
	minLevel Level
	mu       sync.Mutex
}

// NewLogger returns a new Logger instance which writes log entries at or above a minimum severity
// level to a specific output destination.
func NewLogger(out io.Writer, minLevel Level) *Logger {
	return &Logger{
		out:      out,
		minLevel: minLevel,
	}
}

// PrintInfo is a helper that writes Info level log entries.
func (l *Logger) PrintInfo(message string, properties map[string]string) {
	l.print(LevelInfo, message, properties)
}

// PrintError is a helper that writes Error level log entries.
func (l *Logger) PrintError(err error, properties map[string]string) {
	l.print(LevelError, err.Error(), properties)
}

// PrintFatal is a helper that writes Info level log entries.
// It also terminates the application.
func (l *Logger) PrintFatal(err error, properties map[string]string) {
	l.print(LevelFatal, err.Error(), properties)
	os.Exit(1)
}

type LogEntry struct {
	// indicating the severity of the log entry
	Level string `json:"level"`
	// The UTC time that the log entry was made with second precision.
	Time string `json:"time"`
	// A string containing the free-text information or error message.
	Message string `json:"message"`
	// Any additional information relevant to the log entry in string key/value pairs (optional).
	Properties map[string]string `json:"properties,omitempty"`
	// A stack trace for debugging purposes (optional).
	Trace string `json:"trace,omitempty"`
}

// print is an internal method for writing a log entry.
func (l *Logger) print(level Level, message string, properties map[string]string) (int, error) {
	// If the log is not of severe enough level to be logged, then return with no further action.
	// If the severity level of the log entry is below the minimum severity for the logger
	// then return with no further action
	if level < l.minLevel {
		return 0, nil
	}

	// Declare an anonymous struct holding the data for the log entry.
	aux := LogEntry{
		Level:      level.String(),
		Time:       time.Now().UTC().Format(time.RFC3339),
		Message:    message,
		Properties: properties,
	}

	// Include a stack trace for entries at the ERROR and FATAL levels.
	if level >= LevelError {
		aux.Trace = string(debug.Stack())
	}

	// Declare a line variable for holding the actual log entry text.
	var line []byte

	// Marshal the LogEntry struct to JSON and store it in the line variable. If there was a
	// problem creating the JSON then set the contents of the log entry to be that
	// plan-text error message instead.
	line, err := json.Marshal(aux)
	if err != nil {
		line = []byte(LevelError.String() + ": unable to marshal log entry" + err.Error())
	}

	// Lock the mutex so that no two writes to the output destination cannot happen concurrently.
	// If we don't do this, it's possible that the text for two or more log entries will
	// be intermingled in the output
	l.mu.Lock()
	defer l.mu.Unlock()

	// Write the log entry followed by a newline.
	return l.out.Write(append(line, '\n'))
}

// Make our logger satisfy the io.Writer interface by implementing the Write() method
// so that it can be used as the output destination for the Go's http server error log
// in the server.go file.
// It writes a log entry at the ERROR level with no additional properties
func (l *Logger) Write(message []byte) (n int, err error) {
	return l.print(LevelError, string(message), nil)
}
