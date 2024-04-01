package taskpool

import (
	"runtime"
	"unsafe"

	"github.com/lesismal/nbio/logging"
)

// IOTaskPool .
type IOTaskPool struct {
	bufSize    int
	queueSize  int
	concurrent int
	chTask     chan func([]byte)
	chClose    chan struct{}
	caller     func(f func())
}

func (tp *IOTaskPool) run() {
	buf := make([]byte, tp.bufSize)
	for {
		select {
		case f := <-tp.chTask:
			tp.caller(func() {
				f(buf)
			})
		case <-tp.chClose:
			return
		}
	}
}

// Go .
func (tp *IOTaskPool) Go(f func([]byte)) {
	select {
	case tp.chTask <- f:
	case <-tp.chClose:
	}
}

// Stop .
func (tp *IOTaskPool) Stop() {
	close(tp.chClose)
}

// NewIO .
func NewIO(concurrent, queueSize, bufSize int, v ...interface{}) *IOTaskPool {
	if concurrent <= 0 {
		concurrent = runtime.NumCPU() * 2
	}
	if queueSize <= 0 {
		queueSize = 1024
	}
	if bufSize <= 0 {
		bufSize = 1024 * 64
	}

	tp := &IOTaskPool{
		bufSize:    bufSize,
		queueSize:  queueSize,
		concurrent: concurrent,
		chTask:     make(chan func([]byte), queueSize),
		chClose:    make(chan struct{}),
	}
	tp.caller = func(f func()) {
		defer func() {
			if err := recover(); err != nil {
				const size = 64 << 10
				buf := make([]byte, size)
				buf = buf[:runtime.Stack(buf, false)]
				logging.Error("iotaskpool call failed: %v\n%v\n", err, *(*string)(unsafe.Pointer(&buf)))
			}
		}()
		f()
	}
	if len(v) > 0 {
		if caller, ok := v[0].(func(f func())); ok {
			tp.caller = caller
		}
	}
	for i := 0; i < concurrent; i++ {
		go tp.run()
	}

	return tp
}
