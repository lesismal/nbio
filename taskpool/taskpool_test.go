package taskpool

import (
	"sync"
	"testing"
	"time"
)

const testLoopNum = 1024

func BenchmarkGo(b *testing.B) {
	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		wg := sync.WaitGroup{}
		wg.Add(testLoopNum)
		for j := 0; j < testLoopNum; j++ {
			go call(func() {
				// time.Sleep(time.Microsecond)
				wg.Done()
			})
		}
		wg.Wait()
	}
}

func BenchmarkFixedPoolGo(b *testing.B) {
	p := NewFixedPool(32, 512)
	defer p.Stop()

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		wg := sync.WaitGroup{}
		wg.Add(testLoopNum)
		for j := 0; j < testLoopNum; j++ {
			p.Go(func() {
				// time.Sleep(time.Microsecond)
				wg.Done()
			})
		}
		wg.Wait()
	}
}

func BenchmarkFixedPoolGoByIndex(b *testing.B) {
	p := NewFixedPool(32, 512)
	defer p.Stop()

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		wg := sync.WaitGroup{}
		wg.Add(testLoopNum)
		for j := 0; j < testLoopNum; j++ {
			p.GoByIndex(j, func() {
				// time.Sleep(time.Microsecond)
				wg.Done()
			})
		}
		wg.Wait()
	}
}

func BenchmarkFixedNoOrderPool(b *testing.B) {
	p := NewFixedNoOrderPool(32, 1024)
	defer p.Stop()

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		wg := sync.WaitGroup{}
		wg.Add(testLoopNum)
		for j := 0; j < testLoopNum; j++ {
			p.Go(func() {
				// time.Sleep(time.Microsecond)
				wg.Done()
			})
		}
		wg.Wait()
	}
}

func BenchmarkMixedPool(b *testing.B) {
	p := NewMixedPool(1024, 4, 512)
	defer p.Stop()

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		wg := sync.WaitGroup{}
		wg.Add(testLoopNum)
		for j := 0; j < testLoopNum; j++ {
			p.Go(func() {
				// time.Sleep(time.Microsecond)
				wg.Done()
			})
		}
		wg.Wait()
	}
}

func BenchmarkTaskPool(b *testing.B) {
	p := New(32, time.Second*10)
	defer p.Stop()

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		wg := sync.WaitGroup{}
		wg.Add(testLoopNum)
		for j := 0; j < testLoopNum; j++ {
			p.Go(func() {
				// time.Sleep(time.Microsecond)
				wg.Done()
			})
		}
		wg.Wait()
	}
}
