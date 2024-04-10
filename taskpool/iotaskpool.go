package taskpool

import (
	"sync"
)

// IOTaskPool  .
type IOTaskPool struct {
	task *TaskPool
	pool sync.Pool
}

// Go .
func (tp *IOTaskPool) Go(f func([]byte)) {
	tp.task.Go(func() {
		pbuf := tp.pool.Get().(*[]byte)
		f(*pbuf)
		tp.pool.Put(pbuf)
	})
}

// Stop .
func (tp *IOTaskPool) Stop() {
	tp.task.Stop()
}

// NewIO creates and returns a IOTaskPool.
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
