// Copyright 2020 lesismal. All rights reserved.
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package taskpool

import (
	"errors"
	"sync"
	"sync/atomic"
	"time"
)

var (
	// ErrStopped .
	ErrStopped = errors.New("stopped")
)

// runner .
type runner struct {
	parent *TaskPool
}

func (r *runner) taskLoop(maxIdleTime time.Duration, chTask <-chan func(), chClose <-chan struct{}, f func()) {
	defer func() {
		r.parent.wg.Done()
		<-r.parent.chRunner
	}()

	call(f)

	timer := time.NewTimer(maxIdleTime)
	defer timer.Stop()
	for r.parent.running {
		select {
		case f := <-chTask:
			call(f)
			timer.Reset(maxIdleTime)
		case <-timer.C:
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

	chTask   chan func()
	chRunner chan struct{}
	chClose  chan struct{}

	maxIdleTime time.Duration
}

func (tp *TaskPool) push(f func()) error {
	select {
	case tp.chTask <- f:
	case tp.chRunner <- struct{}{}:
		r := &runner{parent: tp}
		tp.wg.Add(1)
		go r.taskLoop(tp.maxIdleTime, tp.chTask, tp.chClose, f)
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

// New .
func New(size int, maxIdleTime time.Duration) *TaskPool {
	tp := &TaskPool{
		wg:          &sync.WaitGroup{},
		running:     true,
		chTask:      make(chan func(), 1024),
		chRunner:    make(chan struct{}, size),
		chClose:     make(chan struct{}),
		maxIdleTime: maxIdleTime,
	}
	tp.wg.Add(1)

	return tp
}
