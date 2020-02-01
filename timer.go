package nbio

import (
	"sync"
	"time"
)

type wheel struct {
	mux    sync.Mutex
	values map[*Conn]time.Time
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
		if len(w.values) > 0 {
			for c, expr := range w.values {
				if expr.IsZero() || now.After(expr) {
					c.closeWithError(tw.closeErr)
					delete(w.values, c)
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
	newIndex := int(uint32((tw.index + int(t.Sub(time.Now())/tw.interval) + 1)) % uint32(len(tw.wheels)))
	if newIndex != *pindex {
		if uint32(*pindex) < uint32(len(tw.wheels)) {
			w := &tw.wheels[*pindex]
			w.mux.Lock()
			delete(w.values, c)
			w.mux.Unlock()
		}
		*pindex = newIndex
		w := &tw.wheels[newIndex]
		w.mux.Lock()
		w.values[c] = t
		w.mux.Unlock()
	}
}

func (tw *timerWheel) delete(c *Conn, pindex *int) {
	if uint32(*pindex) < uint32(len(tw.wheels)) {
		w := &tw.wheels[*pindex]
		w.mux.Lock()
		delete(w.values, c)
		w.mux.Unlock()
	}
}

func (tw *timerWheel) start() {
	tw.expire = time.Now().Add(tw.interval)
}

func newTimerWheel(wheelNum int, interval time.Duration, closeErr error) *timerWheel {
	tw := &timerWheel{
		index:    0,
		interval: interval,
		closeErr: closeErr,
		wheels:   make([]wheel, wheelNum),
	}
	for i := 0; i < wheelNum; i++ {
		tw.wheels[i].values = make(map[*Conn]time.Time, 128)
	}
	return tw
}
