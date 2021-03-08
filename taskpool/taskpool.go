// Copyright 2020 lesismal. All rights reserved.
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package taskpool

import (
	"errors"
	"runtime/debug"
	"sync"
	"sync/atomic"
	"time"

	"github.com/lesismal/nbio/loging"
)

var (
	// ErrStopped .
	ErrStopped = errors.New("stopped")
)

// runner .
type runner struct {
	parent *TaskPool
}

func (r *runner) call(f func()) {
	defer func() {
		if err := recover(); err != nil {
			loging.Error("taskpool runner call failed: %v", err)
			debug.PrintStack()
		}
	}()
	f()
}

func (r *runner) taskLoop(checkIdleInterval time.Duration, chTask <-chan func(), chClose <-chan struct{}, f func()) {
	defer func() {
		r.parent.wg.Done()
		<-r.parent.chRunner
	}()

	r.call(f)

	for r.parent.running {
		select {
		case f := <-chTask:
			r.call(f)
		case <-r.parent.chIdleExit:
			return
		case <-chClose:
			return
		}
	}
}

// TaskPool .
type TaskPool struct {
	wg      *sync.WaitGroup
	mux     sync.Mutex
	running bool
	stopped int32

	chTask     chan func()
	chRunner   chan struct{}
	chIdleExit chan struct{}
	chClose    chan struct{}

	checkIdleInterval time.Duration
}

func (tp *TaskPool) push(f func()) error {
	select {
	case tp.chTask <- f:
	case tp.chRunner <- struct{}{}:
		r := &runner{parent: tp}
		tp.wg.Add(1)
		go r.taskLoop(tp.checkIdleInterval, tp.chTask, tp.chClose, f)
	case <-tp.chClose:
		return ErrStopped
	}
	return nil
}

// Go .
func (tp *TaskPool) Go(f func()) {
	// if atomic.LoadInt32(&tp.stopped) == 1 {
	// 	return
	// }
	tp.push(f)
}

// Stop .
func (tp *TaskPool) Stop() {
	if atomic.CompareAndSwapInt32(&tp.stopped, 0, 1) {
		tp.running = false
		close(tp.chClose)
		tp.wg.Done()
		tp.wg.Wait()
	}
}

func (tp *TaskPool) checkIdle() {
	ticker := time.NewTicker(tp.checkIdleInterval)
	defer ticker.Stop()
	for tp.running {
		select {
		case <-ticker.C:
			pre := tp.chIdleExit
			tp.chIdleExit = make(chan struct{})
			close(pre)
		case <-tp.chClose:
			return
		}
	}
}

// New .
func New(size int, checkIdleInterval time.Duration) *TaskPool {
	tp := &TaskPool{
		wg:                &sync.WaitGroup{},
		running:           true,
		chTask:            make(chan func()),
		chRunner:          make(chan struct{}, size),
		chIdleExit:        make(chan struct{}),
		chClose:           make(chan struct{}),
		checkIdleInterval: checkIdleInterval,
	}

	tp.wg.Add(1)
	go tp.checkIdle()

	return tp
}
