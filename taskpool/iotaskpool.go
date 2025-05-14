package taskpool

import (
	"sync"
)

// IOTaskPool  .
type IOTaskPool struct {
	task *TaskPool
	pool sync.Pool
}

// Call .
//
//go:norace
func (tp *IOTaskPool) Call(f func(*[]byte)) {
	tp.task.Call(func() {
		pbuf := tp.pool.Get().(*[]byte)
		f(pbuf)
		tp.pool.Put(pbuf)
	})
}

// Go .
//
//go:norace
func (tp *IOTaskPool) Go(f func(*[]byte)) {
	tp.task.Go(func() {
		pbuf := tp.pool.Get().(*[]byte)
		f(pbuf)
		tp.pool.Put(pbuf)
	})
}

// Stop .
//
//go:norace
func (tp *IOTaskPool) Stop() {
	tp.task.Stop()
}

// NewIO creates and returns a IOTaskPool.
//
//go:norace
func NewIO(concurrent, queueSize, bufSize int, v ...interface{}) *IOTaskPool {
	task := New(concurrent, queueSize, v...)

	tp := &IOTaskPool{
		task: task,
		pool: sync.Pool{
			New: func() interface{} {
				buf := make([]byte, bufSize)
				return &buf
			},
		},
	}

	return tp
}
