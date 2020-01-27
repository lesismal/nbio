package nbio

import (
	"log"
	"sync/atomic"
)

type worker struct {
	g *Gopher

	idx uint32

	queueSize uint32

	conns map[int]*Conn

	chStop  chan empty
	chEvent chan event

	online int32
}

func newWorker(g *Gopher, idx uint32, queueSize uint32) *worker {
	w := &worker{
		g:         g,
		idx:       idx,
		queueSize: queueSize,
		conns:     map[int]*Conn{},
		chStop:    make(chan empty),
		chEvent:   make(chan event, queueSize),
	}

	return w
}

func (w *worker) stop() {
	log.Printf("worker[%v] stop...", w.idx)
	close(w.chStop)
}

func (w *worker) pushEvent(e event) {
	w.chEvent <- e
}

func (w *worker) start() {
	if w.g != nil {
		defer w.g.Done()
	}

	log.Printf("worker[%v] start", w.idx)
	defer log.Printf("worker[%v] stopped", w.idx)

	for {
		select {
		case e := <-w.chEvent:
			switch e.t {
			case _EVENT_OPEN:
				atomic.AddInt32(&w.online, 1)
				if w.g.onOpen != nil {
					w.g.onOpen(e.c)
				}
			case _EVENT_DATA:
				if w.g.onData != nil {
					w.g.onData(e.c, e.b)
				}
				w.g.payback(e.c, e.b)
			case _EVENT_CLOSE:
				w.onCloseEvent(e.c)
			default:
			}
		case <-w.chStop:
			return
		}
	}
}

func (w *worker) onCloseEvent(c *Conn) {
	atomic.AddInt32(&w.online, -1)
	if w.g.onClose != nil {
		w.g.onClose(c, c.closeErr)
	}
}
