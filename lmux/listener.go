package lmux

import (
	"errors"
	"net"
	"sync/atomic"
	"time"

	"github.com/lesismal/nbio/logging"
)

type event struct {
	err  error
	conn net.Conn
}

type listenerAB struct {
	a, b *ChanListener
}

func New(maxOnlineA int) *ListenerMux {
	return &ListenerMux{
		listeners:  map[net.Listener]listenerAB{},
		chClose:    make(chan struct{}),
		maxOnlineA: int32(maxOnlineA),
		chEventA:   make(chan event, 1024*32),
		chEventB:   make(chan event, 1024*32),
	}
}

type ListenerMux struct {
	listeners  map[net.Listener]listenerAB
	chClose    chan struct{}
	onlineA    int32
	maxOnlineA int32
	chEventA   chan event
	chEventB   chan event
}

func (lm *ListenerMux) Mux(l net.Listener) (*ChanListener, *ChanListener) {
	if l == nil || lm == nil {
		return nil, nil
	}
	if lm.listeners == nil {
		lm.listeners = map[net.Listener]listenerAB{}
	}
	ab := listenerAB{
		a: &ChanListener{
			addr:     l.Addr(),
			chClose:  lm.chClose,
			chEvent:  lm.chEventA,
			decrease: lm.DecreaseOnlineA,
		},
		b: &ChanListener{
			addr:    l.Addr(),
			chClose: lm.chClose,
			chEvent: lm.chEventB,
		},
	}
	lm.listeners[l] = ab
	return ab.a, ab.b
}

func (lm *ListenerMux) Stop() {
	if lm == nil {
		return
	}
	for l, ab := range lm.listeners {
		l.Close()
		ab.a.Close()
		ab.b.Close()
	}
	close(lm.chClose)
}

func (lm *ListenerMux) Start() {
	if lm == nil {
		return
	}

	for k, v := range lm.listeners {
		go func(l net.Listener, listenerA *ChanListener, listenerB *ChanListener) {
			for {
				c, err := l.Accept()
				if err != nil {
					var ne net.Error
					if ok := errors.As(err, &ne); ok && ne.Timeout() {
						logging.Error("Accept failed: temporary error, retrying...")
						time.Sleep(time.Second / 20)
						continue
					} else {
						logging.Error("Accept failed: %v, exit...", err)
						break
					}
				}
				if atomic.AddInt32(&lm.onlineA, 1) <= lm.maxOnlineA {
					listenerA.chEvent <- event{err: nil, conn: c}
				} else {
					atomic.AddInt32(&lm.onlineA, -1)
					listenerB.chEvent <- event{err: nil, conn: c}
				}
			}
		}(k, v.a, v.b)
	}
}

func (lm *ListenerMux) DecreaseOnlineA() {
	atomic.AddInt32(&lm.onlineA, -1)
}

// ChanListener .
type ChanListener struct {
	addr     net.Addr
	chEvent  chan event
	chClose  chan struct{}
	decrease func()
}

// Accept accepts a connection.
func (l *ChanListener) Accept() (net.Conn, error) {
	select {
	case e := <-l.chEvent:
		return e.conn, e.err
	case <-l.chClose:
		return nil, net.ErrClosed
	}
}

// Close does nothing but implementing net.Conn.Close.
// User should call ListenerMux.Close to close it automatically.
func (l *ChanListener) Close() error {
	return nil
}

// Addr returns the listener's network address.
func (l *ChanListener) Addr() net.Addr {
	return l.addr
}

func (l *ChanListener) Decrease() {
	if l.decrease != nil {
		l.decrease()
	}
}
