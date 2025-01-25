// Copyright 2020 lesismal. All rights reserved.
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package taskpool

import (
	"runtime"
	"sync/atomic"
	"unsafe"

	"github.com/lesismal/nbio/logging"
)

// TaskPool .
type TaskPool struct {
	concurrent    int64
	maxConcurrent int64
	chQqueue      chan func()
	chClose       chan struct{}
	caller        func(f func())
}

// fork .
//
//go:norace
func (tp *TaskPool) fork(f func()) bool {
	if atomic.AddInt64(&tp.concurrent, 1) < tp.maxConcurrent {
		go func() {
			defer atomic.AddInt64(&tp.concurrent, -1)
			tp.caller(f)
			for {
				select {
				case f = <-tp.chQqueue:
					if f != nil {
						tp.caller(f)
					}
				default:
					return
				}
			}
		}()
		return true
	}
	return false
}

// Call .
//
//go:norace
func (tp *TaskPool) Call(f func()) {
	tp.caller(f)
}

// Go .
//
//go:norace
func (tp *TaskPool) Go(f func()) {
	// If current goroutine num is less than maxConcurrent,
	// creat a new goroutine to exec new task.
	if tp.fork(f) {
		return
	}

	// Else push the new task into chan/queue.
	atomic.AddInt64(&tp.concurrent, -1)
	select {
	case tp.chQqueue <- f:
	case <-tp.chClose:
	}
}

// Stop .
//
//go:norace
func (tp *TaskPool) Stop() {
	atomic.AddInt64(&tp.concurrent, tp.maxConcurrent)
	close(tp.chClose)
}

// New creates and returns a TaskPool.
//
//go:norace
func New(maxConcurrent int, chQqueueSize int, v ...interface{}) *TaskPool {
	tp := &TaskPool{
		maxConcurrent: int64(maxConcurrent - 1),
		chQqueue:      make(chan func(), chQqueueSize),
		chClose:       make(chan struct{}),
	}
	tp.caller = func(f func()) {
		defer func() {
			if err := recover(); err != nil {
				const size = 64 << 10
				buf := make([]byte, size)
				buf = buf[:runtime.Stack(buf, false)]
				logging.Error("taskpool call failed: %v\n%v\n", err, *(*string)(unsafe.Pointer(&buf)))
			}
		}()
		f()
	}
	if len(v) > 0 {
		if caller, ok := v[0].(func(f func())); ok {
			tp.caller = func(f func()) {
				defer atomic.AddInt64(&tp.concurrent, -1)
				caller(f)
			}
		}
	}
	go func() {
		for {
			select {
			case f := <-tp.chQqueue:
				if tp.fork(f) {
					continue
				}

				if f != nil {
					tp.caller(f)
				}
			case <-tp.chClose:
				return
			}
		}
	}()
	return tp
}
