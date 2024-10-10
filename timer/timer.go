// Copyright 2020 lesismal. All rights reserved.
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package timer

import (
	"math"
	"runtime"
	"sync"
	"time"
	"unsafe"

	"github.com/lesismal/nbio/logging"
)

const (
	TimeForever = time.Duration(math.MaxInt64)
)

type Timer struct {
	name      string
	asyncMux  sync.Mutex
	asyncList []func()
}

//go:norace
func New(name string) *Timer {
	return &Timer{name: name, asyncList: make([]func(), 8)[0:0]}
}

// IsTimerRunning .
//
//go:norace
func (t *Timer) IsTimerRunning() bool {
	return true
}

// Start .
//
//go:norace
func (t *Timer) Start() {}

// Stop .
//
//go:norace
func (t *Timer) Stop() {}

// After used as time.After.
//
//go:norace
func (t *Timer) After(d time.Duration) <-chan time.Time {
	return time.After(d)
}

// AfterFunc used as time.AfterFunc.
//
//go:norace
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
//
//go:norace
func (t *Timer) Async(f func()) {
	t.asyncMux.Lock()
	isHead := (len(t.asyncList) == 0)
	t.asyncList = append(t.asyncList, f)
	t.asyncMux.Unlock()
	if isHead {
		go func() {
			i := 0
			for {
				t.asyncMux.Lock()
				if i == len(t.asyncList) {
					if cap(t.asyncList) > 1024 {
						t.asyncList = make([]func(), 0, 8)
					} else {
						t.asyncList = t.asyncList[0:0]
					}
					t.asyncMux.Unlock()
					return
				}
				f := t.asyncList[i]
				i++
				t.asyncMux.Unlock()
				func() {
					defer func() {
						err := recover()
						if err != nil {
							const size = 64 << 10
							buf := make([]byte, size)
							buf = buf[:runtime.Stack(buf, false)]
							logging.Error("Timer[%v] async call failed: %v\n%v\n", t.name, err, *(*string)(unsafe.Pointer(&buf)))
						}
					}()
					f()
				}()
			}
		}()
	}
}

// func (t *Timer) Async(f func()) {

// 	go func() {
// 		defer func() {
// 			err := recover()
// 			if err != nil {
// 				const size = 64 << 10
// 				buf := make([]byte, size)
// 				buf = buf[:runtime.Stack(buf, false)]
// 				logging.Error("Timer[%v] exec call failed: %v\n%v\n", t.name, err, *(*string)(unsafe.Pointer(&buf)))
// 			}
// 		}()
// 		f()
// 	}()
// }
