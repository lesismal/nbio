package timer

import (
	"fmt"
	"sync"
	"sync/atomic"
	"time"
)

type TimerGroup struct {
	size   uint32
	index  uint32
	timers []*Timer
}

// Start .
func (tg *TimerGroup) Start() {
	for _, t := range tg.timers {
		t.Start()
	}
}

// Stop .
func (tg *TimerGroup) Stop() {
	wg := sync.WaitGroup{}
	for _, v := range tg.timers {
		wg.Add(1)
		go func(t *Timer) {
			defer wg.Done()
			t.Stop()
		}(v)
	}
	wg.Wait()
}

// After used as time.After.
func (tg *TimerGroup) After(timeout time.Duration) <-chan time.Time {
	return tg.NextTimer().After(timeout)
}

// AfterFunc used as time.AfterFunc.
func (tg *TimerGroup) AfterFunc(timeout time.Duration, f func()) *Item {
	return tg.NextTimer().AfterFunc(timeout, f)
}

// Async executes f in another goroutine.
func (tg *TimerGroup) Async(f func()) {
	tg.NextTimer().Async(f)
}

// NextIndex returns next timer index.
func (tg *TimerGroup) NextIndex() uint32 {
	return atomic.AddUint32(&tg.index, 1) % tg.size
}

// NextTimer returns next timer.
func (tg *TimerGroup) NextTimer() *Timer {
	return tg.timers[tg.NextIndex()]
}

// NewGroup creates a TimerGroup.
func NewGroup(name string, size int, executor func(f func())) *TimerGroup {
	if size <= 0 {
		panic(fmt.Errorf("TimerGroup: invalid size: %v", size))
	}

	tg := &TimerGroup{size: uint32(size)}
	for i := 0; i < size; i++ {
		timer := New(name, executor)
		tg.timers = append(tg.timers, timer)
	}

	return tg
}
