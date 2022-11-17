// Copyright 2020 lesismal. All rights reserved.
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package timer

import (
	"container/heap"
	"math"
	"runtime"
	"sync"
	"time"
	"unsafe"

	"github.com/lesismal/nbio/logging"
)

const (
	TimeForever = time.Duration(math.MaxInt64)
)

type Timer struct {
	name string

	wg  sync.WaitGroup
	mux sync.Mutex

	executor func(f func())

	chCalling chan struct{}
	callings  []func()

	trigger *time.Timer
	items   timerHeap

	chClose chan struct{}
}

func New(name string, executor func(f func())) *Timer {
	t := &Timer{}

	t.mux.Lock()
	t.name = name
	t.executor = executor
	t.callings = []func(){}
	t.chCalling = make(chan struct{}, 1)
	t.trigger = time.NewTimer(TimeForever)
	t.chClose = make(chan struct{})
	t.mux.Unlock()

	return t
}

// Start .
func (t *Timer) Start() {
	t.wg.Add(1)
	go t.loop()
}

// Stop .
func (t *Timer) Stop() {
	close(t.chClose)
	t.wg.Wait()
}

// After used as time.After.
func (t *Timer) After(timeout time.Duration) <-chan time.Time {
	c := make(chan time.Time, 1)
	t.AfterFunc(timeout, func() {
		c <- time.Now()
	})
	return c
}

// AfterFunc used as time.AfterFunc.
func (t *Timer) AfterFunc(timeout time.Duration, f func()) *Item {
	t.mux.Lock()

	now := time.Now()
	it := &Item{
		index:  len(t.items),
		expire: now.Add(timeout),
		f:      f,
		parent: t,
	}

	heap.Push(&t.items, it)
	if t.items[0] == it {
		t.trigger.Reset(timeout)
	}

	t.mux.Unlock()

	return it
}

// Async executes f in another goroutine.
func (t *Timer) Async(f func()) {
	if f != nil {
		t.mux.Lock()
		t.callings = append(t.callings, f)
		t.mux.Unlock()
		select {
		case t.chCalling <- struct{}{}:
		default:
		}
	}
}

func (t *Timer) removeTimer(it *Item) {
	t.mux.Lock()
	defer t.mux.Unlock()

	index := it.index
	if index < 0 || index >= len(t.items) {
		return
	}

	if t.items[index] == it {
		heap.Remove(&t.items, index)
		if len(t.items) > 0 {
			if index == 0 {
				t.trigger.Reset(time.Until(t.items[0].expire))
			}
		} else {
			t.trigger.Reset(TimeForever)
		}
	}
}

func (t *Timer) resetTimer(it *Item, d time.Duration) {
	t.mux.Lock()
	defer t.mux.Unlock()

	index := it.index
	if index < 0 || index >= len(t.items) {
		return
	}

	if t.items[index] == it {
		it.expire = time.Now().Add(d)
		heap.Fix(&t.items, index)
		if index == 0 || it.index == 0 {
			t.trigger.Reset(time.Until(t.items[0].expire))
		}
	}
}

func (t *Timer) loop() {
	defer t.wg.Done()
	logging.Debug("Timer[%v] timer start", t.name)
	defer logging.Debug("Timer[%v] timer stopped", t.name)
	for {
		select {
		case <-t.chCalling:
			for {
				t.mux.Lock()
				if len(t.callings) == 0 {
					t.callings = nil
					t.mux.Unlock()
					break
				}
				f := t.callings[0]
				t.callings = t.callings[1:]
				t.mux.Unlock()
				if t.executor != nil {
					t.executor(f)
				} else {
					func() {
						defer func() {
							err := recover()
							if err != nil {
								const size = 64 << 10
								buf := make([]byte, size)
								buf = buf[:runtime.Stack(buf, false)]
								logging.Error("Timer[%v] exec call failed: %v\n%v\n", t.name, err, *(*string)(unsafe.Pointer(&buf)))
							}
						}()
						f()
					}()
				}
			}
		case <-t.trigger.C:
			for {
				t.mux.Lock()
				if t.items.Len() == 0 {
					t.trigger.Reset(TimeForever)
					t.mux.Unlock()
					break
				}
				now := time.Now()
				it := t.items[0]
				if now.After(it.expire) {
					heap.Remove(&t.items, it.index)
					t.mux.Unlock()
					if t.executor != nil {
						t.executor(it.f)
					} else {
						func() {
							defer func() {
								err := recover()
								if err != nil {
									const size = 64 << 10
									buf := make([]byte, size)
									buf = buf[:runtime.Stack(buf, false)]
									logging.Error("NBIO[%v] exec timer failed: %v\n%v\n", t.name, err, *(*string)(unsafe.Pointer(&buf)))
								}
							}()
							it.f()
						}()
					}
				} else {
					t.trigger.Reset(it.expire.Sub(now))
					t.mux.Unlock()
					break
				}
			}
		case <-t.chClose:
			return
		}
	}
}

type timerHeap []*Item

func (h timerHeap) Len() int           { return len(h) }
func (h timerHeap) Less(i, j int) bool { return h[i].expire.Before(h[j].expire) }
func (h timerHeap) Swap(i, j int) {
	h[i], h[j] = h[j], h[i]
	h[i].index = i
	h[j].index = j
}

func (h *timerHeap) Push(x interface{}) {
	*h = append(*h, x.(*Item))
	n := len(*h)
	(*h)[n-1].index = n - 1
}

func (h *timerHeap) Pop() interface{} {
	old := *h
	n := len(old)
	x := old[n-1]
	old[n-1] = nil // avoid memory leak
	*h = old[0 : n-1]
	return x
}

// Item is a heap timer item.
type Item struct {
	index  int
	expire time.Time
	f      func()
	parent *Timer
}

// Stop stops a timer.
func (it *Item) Stop() {
	it.parent.removeTimer(it)
}

// Reset resets timer.
func (it *Item) Reset(timeout time.Duration) {
	it.parent.resetTimer(it, timeout)
}
