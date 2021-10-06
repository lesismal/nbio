package main

import (
	"os"
	"sync"
	"time"
)

type Pool struct {
	pool        sync.Pool
	buffers     chan struct{}
	messageSize int
	getTimeout  time.Duration
}

func NewFixedBufferPool(buffers, messageSize int, getTimeout time.Duration) *Pool {
	rtn := &Pool{
		buffers:     make(chan struct{}, buffers),
		messageSize: messageSize,
		getTimeout:  getTimeout,
		pool: sync.Pool{
			New: func() interface{} {
				buf := make([]byte, messageSize)
				return &buf
			},
		},
	}
	for i := 0; i < buffers; i++ {
		rtn.buffers <- struct{}{}
	}
	return rtn
}

func (p *Pool) Put(in []byte) {
	if len(in) == p.messageSize {
		select {
		case p.buffers <- struct{}{}:
			p.pool.Put(&in)
		default:
		}
	}
}

func (p *Pool) Get() ([]byte, error) {
	// don't wast cycles building a timer if there is a buffer available
	select {
	case <-p.buffers:
		return (*(p.pool.Get().(*[]byte)))[0:0], nil
	default:
	}
	t := time.NewTimer(p.getTimeout)
	select {
	case <-p.buffers:
		t.Stop()
		return (*(p.pool.Get().(*[]byte)))[0:0], nil
	case <-t.C:
		return nil, os.ErrDeadlineExceeded
	}
}
