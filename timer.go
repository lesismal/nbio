package nbio

import (
	"sync"
	"time"
)

type timerWheel struct {
	mux      sync.Mutex
	index    int
	expire   time.Time
	interval time.Duration
	closeErr error
	wheels   []map[*Conn]time.Time
}

func (tw *timerWheel) check(now time.Time) int {
	tw.mux.Lock()
	if now.After(tw.expire) {
		if len(tw.wheels[tw.index]) > 0 {
			wheelMap := tw.wheels[tw.index]
			for c, expr := range wheelMap {
				if expr.IsZero() || now.After(expr) {
					c.closeWithError(tw.closeErr)
					delete(wheelMap, c)
				}
			}
			tw.expire = now.Add(tw.interval)
			tw.index = (tw.index + 1) % len(tw.wheels)
		} else {
			tw.expire = now.Add(tw.interval)
			tw.index = (tw.index + 1) % len(tw.wheels)
		}

		tw.mux.Unlock()
		return int(tw.interval.Milliseconds())
	}
	tw.mux.Unlock()

	return int(tw.expire.Sub(now).Milliseconds())
}

func (tw *timerWheel) reset(c *Conn, pindex *int, t time.Time) {
	tw.mux.Lock()

	newIndex := (tw.index + int(t.Sub(time.Now())/tw.interval) + 1) % len(tw.wheels)
	if newIndex != *pindex {
		if uint32(*pindex) < uint32(len(tw.wheels)) {
			delete(tw.wheels[*pindex], c)
		}
		*pindex = newIndex
		tw.wheels[newIndex][c] = t
	}

	tw.mux.Unlock()
}

func (tw *timerWheel) delete(c *Conn, pindex *int) {
	tw.mux.Lock()
	if uint32(*pindex) < uint32(len(tw.wheels)) {
		delete(tw.wheels[*pindex], c)
	}
	tw.mux.Unlock()
}

func (tw *timerWheel) start() {
	tw.expire = time.Now().Add(tw.interval)
}

func newTimerWheel(wheelNum int, interval time.Duration, closeErr error) *timerWheel {
	tw := &timerWheel{
		index:    0,
		interval: interval,
		closeErr: closeErr,
		wheels:   make([]map[*Conn]time.Time, wheelNum),
	}
	for i := 0; i < wheelNum; i++ {
		tw.wheels[i] = make(map[*Conn]time.Time, 64)
	}
	return tw
}
