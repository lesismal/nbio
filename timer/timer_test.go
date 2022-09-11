package timer

import (
	"log"
	"math/rand"
	"testing"
	"time"
)

func TestHeapTimer(t *testing.T) {
	timer := New("nbio")
	defer timer.Stop()

	timeout := time.Second / 10

	testHeapTimerNormal(timer, timeout)
	testHeapTimerExecPanic(timer, timeout)
	testHeapTimerNormalExecMany(timer, timeout)
	testHeapTimerExecManyRandtime(timer)
}

func testHeapTimerNormal(timer *Timer, timeout time.Duration) {
	t1 := time.Now()
	ch1 := make(chan int)
	timer.AfterFunc(timeout*5, func() {
		close(ch1)
	})
	<-ch1
	to1 := time.Since(t1)
	if to1 < timeout*4 || to1 > timeout*10 {
		log.Panicf("invalid to1: %v", to1)
	}

	t2 := time.Now()
	ch2 := make(chan int)
	it2 := timer.AfterFunc(timeout, func() {
		close(ch2)
	})
	it2.Reset(timeout * 5)
	<-ch2
	to2 := time.Since(t2)
	if to2 < timeout*4 || to2 > timeout*10 {
		log.Panicf("invalid to2: %v", to2)
	}

	ch3 := make(chan int)
	it3 := timer.AfterFunc(timeout, func() {
		close(ch3)
	})
	it3.Stop()
	<-timer.After(timeout * 2)
	select {
	case <-ch3:
		log.Panicf("stop failed")
	default:
	}
}

func testHeapTimerExecPanic(timer *Timer, timeout time.Duration) {
	timer.AfterFunc(timeout, func() {
		panic("test")
	})
}

func testHeapTimerNormalExecMany(timer *Timer, timeout time.Duration) {
	ch4 := make(chan int, 5)
	for i := 0; i < 5; i++ {
		n := i + 1
		if n == 3 {
			n = 5
		} else if n == 5 {
			n = 3
		}

		timer.AfterFunc(timeout*time.Duration(n), func() {
			ch4 <- n
		})
	}

	for i := 0; i < 5; i++ {
		n := <-ch4
		if n != i+1 {
			log.Panicf("invalid n: %v, %v", i, n)
		}
	}
}

func testHeapTimerExecManyRandtime(timer *Timer) {
	its := make([]*Item, 100)[0:0]
	ch5 := make(chan int, 100)
	for i := 0; i < 100; i++ {
		n := 500 + rand.Int()%200
		to := time.Duration(n) * time.Second / 1000
		its = append(its, timer.AfterFunc(to, func() {
			ch5 <- n
		}))
	}
	if len(its) != 100 || noRaceLenTimers(timer.items) != 100 {
		log.Panicf("invalid timers length: %v, %v", len(its), timer.items.Len())
	}
	for i := 0; i < 50; i++ {
		if its[0] == nil {
			log.Panicf("invalid its[0]")
		}
		its[0].Stop()
		its = its[1:]
	}
	if len(its) != 50 || noRaceLenTimers(timer.items) != 50 {
		log.Panicf("invalid timers length: %v, %v", len(its), timer.items.Len())
	}
	recved := 0
LOOP_RECV:
	for {
		select {
		case <-ch5:
			recved++
		case <-time.After(time.Second):
			break LOOP_RECV
		}
	}
	if recved != 50 {
		log.Panicf("invalid recved num: %v", recved)
	}

	it := &Item{parent: timer, index: -1}
	it.Stop()
}

//go:norace
func noRaceLenTimers(ts timerHeap) int {
	return ts.Len()
}
