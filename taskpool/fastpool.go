package taskpool

import (
	"container/list"
	"runtime"
	"sync"
	"unsafe"

	"github.com/lesismal/nbio/loging"
)

type fastRunner struct {
	parent *FastPool
}

func (r *fastRunner) call(e interface{}) {
	defer func() {
		if err := recover(); err != nil {
			const size = 64 << 10
			buf := make([]byte, size)
			buf = buf[:runtime.Stack(buf, false)]
			loging.Error("taskpool fastRunner call failed: %v\n%v\n", err, *(*string)(unsafe.Pointer(&buf)))
		}
	}()
	e.(func())()
}

func (r *fastRunner) taskloop(mux *sync.Mutex, cond *sync.Cond, queue *list.List) {
	defer func() {
		r.parent.wg.Done()
		for r.parent.running {
			mux.Lock()
			e := queue.Front()
			if e != nil {
				queue.Remove(e)
				mux.Unlock()
				r.call(e.Value)
			} else {
				break
			}
		}
	}()
	for r.parent.running {
		mux.Lock()
		e := queue.Front()
		if e != nil {
			queue.Remove(e)
			mux.Unlock()
			r.call(e.Value)
		} else {
			cond.Wait()
			e := queue.Front()
			if e != nil {
				queue.Remove(e)
				mux.Unlock()
				r.call(e.Value)
			} else {
				mux.Unlock()
			}
		}
	}
}

// FastPool .
type FastPool struct {
	wg      *sync.WaitGroup
	cond    *sync.Cond
	queue   *list.List
	running bool
}

// Go .
func (fp *FastPool) Go(f func()) {
	fp.cond.L.Lock()
	fp.queue.PushBack(f)
	fp.cond.Signal()
	fp.cond.L.Unlock()
}

// Stop .
func (fp *FastPool) Stop() {
	fp.running = false
	fp.cond.Broadcast()
	fp.wg.Wait()
}

// NewFastPool .
func NewFastPool(size int) *FastPool {
	mux := &sync.Mutex{}
	cond := sync.NewCond(mux)
	queue := list.New()
	fp := &FastPool{
		wg:      &sync.WaitGroup{},
		cond:    cond,
		queue:   queue,
		running: true,
	}
	for i := 0; i < size; i++ {
		fp.wg.Add(1)
		r := &fastRunner{parent: fp}
		go r.taskloop(mux, cond, queue)
	}
	return fp
}
