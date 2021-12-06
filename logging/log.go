// Copyright 2020 lesismal. All rights reserved.
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package logging

import (
	"log"
)

var (
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
	SetLevel(lvl int)
	Debug(format string, v ...interface{})
	Info(format string, v ...interface{})
	Warn(format string, v ...interface{})
	Error(format string, v ...interface{})
}

// SetLogger sets default logger.
func SetLogger(l Logger) {
	DefaultLogger = l
}

// SetLevel sets default logger's priority.
func SetLevel(lvl int) {
	switch lvl {
	case LevelAll, LevelDebug, LevelInfo, LevelWarn, LevelError, LevelNone:
		DefaultLogger.SetLevel(lvl)
	default:
		log.Printf("invalid log level: %v", lvl)
	}
}

// logger implements Logger and is used in arpc by default.
type logger struct {
	level int
}

// SetLevel sets logs priority.
func (l *logger) SetLevel(lvl int) {
	switch lvl {
	case LevelAll, LevelDebug, LevelInfo, LevelWarn, LevelError, LevelNone:
		l.level = lvl
	default:
		log.Printf("invalid log level: %v", lvl)
	}
}

// Debug uses fmt.Printf to log a message at LevelDebug.
func (l *logger) Debug(format string, v ...interface{}) {
	if LevelDebug >= l.level {
		log.Printf("[DBG] "+format+"\n", v...)
	}
}

// Info uses fmt.Printf to log a message at LevelInfo.
func (l *logger) Info(format string, v ...interface{}) {
	if LevelInfo >= l.level {
		log.Printf("[INF] "+format+"\n", v...)
	}
}

// Warn uses fmt.Printf to log a message at LevelWarn.
func (l *logger) Warn(format string, v ...interface{}) {
	if LevelWarn >= l.level {
		log.Printf("[WRN] "+format+"\n", v...)
	}
}

// Error uses fmt.Printf to log a message at LevelError.
func (l *logger) Error(format string, v ...interface{}) {
	if LevelError >= l.level {
		log.Printf("[ERR] "+format+"\n", v...)
	}
}

// Debug uses DefaultLogger to log a message at LevelDebug.
func Debug(format string, v ...interface{}) {
	if DefaultLogger != nil {
		DefaultLogger.Debug(format, v...)
	}
}

// Info uses DefaultLogger to log a message at LevelInfo.
func Info(format string, v ...interface{}) {
	if DefaultLogger != nil {
		DefaultLogger.Info(format, v...)
	}
}

// Warn uses DefaultLogger to log a message at LevelWarn.
func Warn(format string, v ...interface{}) {
	if DefaultLogger != nil {
		DefaultLogger.Warn(format, v...)
	}
}

// Error uses DefaultLogger to log a message at LevelError.
func Error(format string, v ...interface{}) {
	if DefaultLogger != nil {
		DefaultLogger.Error(format, v...)
	}
}
