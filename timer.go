// +build linux

package nbio

import (
	"sync"
	"time"
)

const (
	interval   = time.Second / 20  // 50 ms
	maxTimeout = time.Second * 120 // 120 s
)

type wheel struct {
	mux   sync.Mutex
	items map[*Conn]time.Time
}

type timerWheel struct {
	mux      sync.Mutex
	index    int
	expire   time.Time
	interval time.Duration
	closeErr error
	wheels   []wheel
}

func (tw *timerWheel) check(now time.Time) int {
	if now.After(tw.expire) {
		w := &tw.wheels[tw.index]
		w.mux.Lock()
		if len(w.items) > 0 {
			for c, expr := range w.items {
				if expr.IsZero() || now.After(expr) {
					c.closeWithError(tw.closeErr)
					delete(w.items, c)
				}
			}
			w.mux.Unlock()
			tw.expire = now.Add(tw.interval)
			tw.index = (tw.index + 1) % len(tw.wheels)
		} else {
			w.mux.Unlock()
			tw.expire = now.Add(tw.interval)
			tw.index = (tw.index + 1) % len(tw.wheels)
		}
		return int(tw.interval.Milliseconds())
	}
	return int(tw.expire.Sub(now).Milliseconds())
}

func (tw *timerWheel) reset(c *Conn, pindex *int, t time.Time) {
	newIndex := int(uint32((tw.index + int(t.Sub(time.Now())/tw.interval))) % uint32(len(tw.wheels)))
	if newIndex != *pindex {
		if uint32(*pindex) < uint32(len(tw.wheels)) {
			w := &tw.wheels[*pindex]
			w.mux.Lock()
			delete(w.items, c)
			w.mux.Unlock()
		}
		*pindex = newIndex
		w := &tw.wheels[newIndex]
		w.mux.Lock()
		w.items[c] = t
		w.mux.Unlock()
	}
}

func (tw *timerWheel) delete(c *Conn, pindex *int) {
	if uint32(*pindex) < uint32(len(tw.wheels)) {
		w := &tw.wheels[*pindex]
		w.mux.Lock()
		delete(w.items, c)
		w.mux.Unlock()
	}
}

func (tw *timerWheel) start() *timerWheel {
	tw.expire = time.Now().Add(tw.interval)
	return tw
}

func newTimerWheel(wheelNum int, interval time.Duration, closeErr error) *timerWheel {
	tw := &timerWheel{
		index:    0,
		interval: interval,
		closeErr: closeErr,
		wheels:   make([]wheel, wheelNum),
	}
	for i := 0; i < wheelNum; i++ {
		tw.wheels[i].items = make(map[*Conn]time.Time, 128)
	}
	return tw
}
