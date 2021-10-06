package main

import (
	"os"
	"time"
)

type Pool struct {
	buffers    chan []byte
	getTimeout time.Duration
}

func NewFixedBufferPool(buffers, messageSize int, getTimeout time.Duration) *Pool {
	rtn := &Pool{
		buffers:    make(chan []byte, buffers),
		getTimeout: getTimeout,
	}
	for i := 0; i < buffers; i++ {
		rtn.buffers <- make([]byte, 0, messageSize)
	}
	return rtn
}

func (p *Pool) Put(in []byte) {
	p.buffers <- in
}

func (p *Pool) Get() ([]byte, error) {
	// don't wast cycles building a timer if there is a buffer available
	select {
	case rtn := <-p.buffers:
		return rtn[:0], nil
	default:
	}
	t := time.NewTimer(p.getTimeout)
	select {
	case rtn := <-p.buffers:
		t.Stop()
		return rtn[:0], nil
	case <-t.C:
		return nil, os.ErrDeadlineExceeded
	}
}
