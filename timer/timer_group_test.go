package timer

import (
	"log"
	"testing"
	"time"
)

func TestTimerGroupSchedule(t *testing.T) {
	tg := NewGroup("nbio", 4, nil)
	tg.Start()
	defer tg.Stop()

	count := 0
	interval := time.Second / 100
	var f func()
	done := make(chan int)
	f = func() {
		if count < 5 {
			tg.AfterFunc(interval, f)
			count++
			log.Printf("timer schedule count: %v", count)
		} else {
			close(done)
		}
	}

	tg.AfterFunc(interval, f)

	<-done
}

func TestTimerGroup(t *testing.T) {
	tg := NewGroup("nbio", 4, nil)
	tg.Start()
	defer tg.Stop()

	timeout := time.Second / 10

	testTimerGroupNormal(tg, timeout)
	testTimerGroupExecPanic(tg, timeout)
	testTimerGroupNormalExecMany(tg, timeout)
}

func testTimerGroupNormal(tg *TimerGroup, timeout time.Duration) {
	t1 := time.Now()
	ch1 := make(chan int)
	tg.AfterFunc(timeout*5, func() {
		close(ch1)
	})
	<-ch1
	to1 := time.Since(t1)
	if to1 < timeout*4 || to1 > timeout*10 {
		log.Panicf("invalid to1: %v", to1)
	}

	t2 := time.Now()
	ch2 := make(chan int)
	it2 := tg.AfterFunc(timeout, func() {
		close(ch2)
	})
	it2.Reset(timeout * 5)
	<-ch2
	to2 := time.Since(t2)
	if to2 < timeout*4 || to2 > timeout*10 {
		log.Panicf("invalid to2: %v", to2)
	}

	ch3 := make(chan int)
	it3 := tg.AfterFunc(timeout, func() {
		close(ch3)
	})
	it3.Stop()
	<-tg.After(timeout * 2)
	select {
	case <-ch3:
		log.Panicf("stop failed")
	default:
	}
}

func testTimerGroupExecPanic(tg *TimerGroup, timeout time.Duration) {
	tg.AfterFunc(timeout, func() {
		panic("test")
	})
}

func testTimerGroupNormalExecMany(tg *TimerGroup, timeout time.Duration) {
	ch4 := make(chan int, 5)
	for i := 0; i < 5; i++ {
		n := i + 1
		if n == 3 {
			n = 5
		} else if n == 5 {
			n = 3
		}

		tg.AfterFunc(timeout*time.Duration(n), func() {
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
