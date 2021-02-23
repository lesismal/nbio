package nbio

import (
	"math"
	"time"
)

const (
	timeForever = time.Duration(math.MaxInt64)
)

// Timer type for export
type Timer struct {
	*htimer
}

// heap timer item
type htimer struct {
	index  int
	expire time.Time
	f      func()
	parent *Gopher
}

// cancel timer
func (it *htimer) Stop() {
	it.parent.removeTimer(it)
}

// reset timer
func (it *htimer) Reset(timeout time.Duration) {
	it.expire = time.Now().Add(timeout)
	it.parent.resetTimer(it)
}

type timerHeap []*htimer

func (h timerHeap) Len() int           { return len(h) }
func (h timerHeap) Less(i, j int) bool { return h[i].expire.Before(h[j].expire) }
func (h timerHeap) Swap(i, j int) {
	h[i], h[j] = h[j], h[i]
	h[i].index = i
	h[j].index = j
}

func (h *timerHeap) Push(x interface{}) {
	*h = append(*h, x.(*htimer))
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
