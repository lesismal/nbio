package timer

import (
	"container/heap"
	"log"
	"math/rand"
	"sync"
	"testing"
	"time"
)

func TestTimer(t *testing.T) {
	tg := NewGroup("nbio", 4, nil)
	tg.Start()
	defer tg.Stop()

	timeout := time.Second / 50

	testAsync(tg)
	testTimerNormal(tg, timeout)
	testTimerExecPanic(tg, timeout)
	testTimerNormalExecMany(tg, timeout)
	testTimerExecManyRandtime(tg)
}

func testAsync(tg *TimerGroup) {
	loops := 3
	wg := sync.WaitGroup{}
	for i := 0; i < loops; i++ {
		wg.Add(1)
		tg.Async(func() {
			defer wg.Done()
		})
	}
	wg.Wait()
}

func testTimerNormal(tg *TimerGroup, timeout time.Duration) {
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

func testTimerExecPanic(tg *TimerGroup, timeout time.Duration) {
	tg.AfterFunc(timeout, func() {
		panic("test")
	})
}

func testTimerNormalExecMany(tg *TimerGroup, timeout time.Duration) {
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

func testTimerExecManyRandtime(tg *TimerGroup) {
	its := make([]*Item, 100)[0:0]
	ch5 := make(chan int, 100)
	for i := 0; i < 100; i++ {
		n := 500 + rand.Int()%200
		to := time.Duration(n) * time.Second / 1000
		its = append(its, tg.AfterFunc(to, func() {
			ch5 <- n
		}))
	}
	for i := 0; i < 50; i++ {
		if its[0] == nil {
			log.Panicf("invalid its[0]")
		}
		its[0].Stop()
		its = its[1:]
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
}

func TestTimerHeap(t *testing.T) {
	now := time.Now()
	th := make(timerHeap, 0, 10)
	for i := 0; i < 100; i++ {
		date := now.Add(time.Duration(rand.Int63n(10000)) * time.Second)
		heap.Push(&th, &Item{
			expire: date,
		})
	}

	last := now
	for i := 0; i < 100; i++ {
		if len(th) == 0 {
			break
		}

		item := heap.Pop(&th)
		if item == nil {
			break
		}
		cur := item.(*Item)
		if cur.expire.After(last) || cur.expire.Equal(last) {
			last = cur.expire
			continue
		}
		t.Error("timer error")
	}
}
