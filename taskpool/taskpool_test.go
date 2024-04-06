package taskpool

import (
	"runtime"
	"sync"
	"testing"
	"time"
	"unsafe"

	"github.com/lesismal/nbio/logging"
)

const testLoopNum = 1024 * 8
const sleepTime = time.Nanosecond * 0

func BenchmarkGo(b *testing.B) {
	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		wg := sync.WaitGroup{}
		wg.Add(testLoopNum)
		for j := 0; j < testLoopNum; j++ {
			go func() {
				defer func() {
					if err := recover(); err != nil {
						const size = 64 << 10
						buf := make([]byte, size)
						buf = buf[:runtime.Stack(buf, false)]
						logging.Error("taskpool call failed: %v\n%v\n", err, *(*string)(unsafe.Pointer(&buf)))
					}
				}()
				if sleepTime > 0 {
					time.Sleep(sleepTime)
				}
				wg.Done()
			}()
		}
		wg.Wait()
	}
}

func BenchmarkTaskPool(b *testing.B) {
	p := New(32, 1024)
	defer p.Stop()

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		wg := sync.WaitGroup{}
		wg.Add(testLoopNum)
		for j := 0; j < testLoopNum; j++ {
			p.Go(func() {
				if sleepTime > 0 {
					time.Sleep(sleepTime)
				}
				wg.Done()
			})
		}
		wg.Wait()
	}
}

func BenchmarkIOTaskPool(b *testing.B) {
	p := NewIO(32, 1024, 1024)
	defer p.Stop()

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		wg := sync.WaitGroup{}
		wg.Add(testLoopNum)
		for j := 0; j < testLoopNum; j++ {
			p.Go(func(buf []byte) {
				if sleepTime > 0 {
					time.Sleep(sleepTime)
				}
				wg.Done()
			})
		}
		wg.Wait()
	}
}
