// Copyright 2020 lesismal. All rights reserved.
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package timer

import (
	"math"
	"runtime"
	"time"
	"unsafe"

	"github.com/lesismal/nbio/logging"
)

const (
	TimeForever = time.Duration(math.MaxInt64)
)

type Timer struct {
	name string
}

func New(name string) *Timer {
	return &Timer{name: name}
}

// IsTimerRunning .
func (t *Timer) IsTimerRunning() bool {
	return true
}

// Start .
func (t *Timer) Start() {}

// Stop .
func (t *Timer) Stop() {}

// After used as time.After.
func (t *Timer) After(d time.Duration) <-chan time.Time {
	return time.After(d)
}

// AfterFunc used as time.AfterFunc.
func (t *Timer) AfterFunc(timeout time.Duration, f func()) *time.Timer {
	return time.AfterFunc(timeout, func() {
		defer func() {
			err := recover()
			if err != nil {
				const size = 64 << 10
				buf := make([]byte, size)
				buf = buf[:runtime.Stack(buf, false)]
				logging.Error("Timer[%v] exec call failed: %v\n%v\n", t.name, err, *(*string)(unsafe.Pointer(&buf)))
			}
		}()
		f()
	})
}

// Async executes f in another goroutine.
func (t *Timer) Async(f func()) {
	go func() {
		defer func() {
			err := recover()
			if err != nil {
				const size = 64 << 10
				buf := make([]byte, size)
				buf = buf[:runtime.Stack(buf, false)]
				logging.Error("Timer[%v] exec call failed: %v\n%v\n", t.name, err, *(*string)(unsafe.Pointer(&buf)))
			}
		}()
		f()
	}()
}
