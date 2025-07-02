// Copyright 2020 lesismal. All rights reserved.
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package logging

import (
	"fmt"
	"io"
	"os"
	"time"
)

var (
	// TimeFormat is used to format time parameters.
	TimeFormat = "2006/01/02 15:04:05.000"

	// Output is used to receive log output.
	Output io.Writer = os.Stdout

	// DefaultLogger is the default logger and is used by arpc.
	DefaultLogger Logger = &logger{level: LevelInfo}
)

const (
	// LevelAll enables all logs.
	LevelAll = iota
	// LevelDebug logs are usually disabled in production.
	LevelDebug
	// LevelInfo is the default logging priority.
	LevelInfo
	// LevelWarn .
	LevelWarn
	// LevelError .
	LevelError
	// LevelNone disables all logs.
	LevelNone
)

// Logger defines log interface.
type Logger interface {
	Debug(format string, v ...interface{})
	Info(format string, v ...interface{})
	Warn(format string, v ...interface{})
	Error(format string, v ...interface{})
}

// SetLogger sets default logger.
//
//go:norace
func SetLogger(l Logger) {
	DefaultLogger = l
}

// SetLevel sets default logger's priority.
//
//go:norace
func SetLevel(lvl int) {
	if l, ok := DefaultLogger.(interface {
		SetLevel(lvl int)
	}); ok {
		l.SetLevel(lvl)
	}
}

// logger implements Logger and is used in arpc by default.
type logger struct {
	level int
}

// SetLevel sets logs priority.
//
//go:norace
func (l *logger) SetLevel(lvl int) {
	switch lvl {
	case LevelAll, LevelDebug, LevelInfo, LevelWarn, LevelError, LevelNone:
		l.level = lvl
	default:
		_, _ = fmt.Fprintf(Output, "invalid log level: %v", lvl)
	}
}

// Debug uses fmt.Printf to log a message at LevelDebug.
//
//go:norace
func (l *logger) Debug(format string, v ...interface{}) {
	if LevelDebug >= l.level {
		_, _ = fmt.Fprintf(Output, time.Now().Format(TimeFormat)+" [DBG] "+format+"\n", v...)
	}
}

// Info uses fmt.Printf to log a message at LevelInfo.
//
//go:norace
func (l *logger) Info(format string, v ...interface{}) {
	if LevelInfo >= l.level {
		_, _ = fmt.Fprintf(Output, time.Now().Format(TimeFormat)+" [INF] "+format+"\n", v...)
	}
}

// Warn uses fmt.Printf to log a message at LevelWarn.
//
//go:norace
func (l *logger) Warn(format string, v ...interface{}) {
	if LevelWarn >= l.level {
		_, _ = fmt.Fprintf(Output, time.Now().Format(TimeFormat)+" [WRN] "+format+"\n", v...)
	}
}

// Error uses fmt.Printf to log a message at LevelError.
//
//go:norace
func (l *logger) Error(format string, v ...interface{}) {
	if LevelError >= l.level {
		_, _ = fmt.Fprintf(Output, time.Now().Format(TimeFormat)+" [ERR] "+format+"\n", v...)
	}
}

// Debug uses DefaultLogger to log a message at LevelDebug.
//
//go:norace
func Debug(format string, v ...interface{}) {
	if DefaultLogger != nil {
		DefaultLogger.Debug(format, v...)
	}
}

// Info uses DefaultLogger to log a message at LevelInfo.
//
//go:norace
func Info(format string, v ...interface{}) {
	if DefaultLogger != nil {
		DefaultLogger.Info(format, v...)
	}
}

// Warn uses DefaultLogger to log a message at LevelWarn.
//
//go:norace
func Warn(format string, v ...interface{}) {
	if DefaultLogger != nil {
		DefaultLogger.Warn(format, v...)
	}
}

// Error uses DefaultLogger to log a message at LevelError.
//
//go:norace
func Error(format string, v ...interface{}) {
	if DefaultLogger != nil {
		DefaultLogger.Error(format, v...)
	}
}
