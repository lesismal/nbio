// Copyright 2020 lesismal. All rights reserved.
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package taskpool

import (
	"runtime"
	"sync"
	"sync/atomic"
	"time"
)

type LockFreeTask struct {
	f func()
}

type LockFreeTaskPool struct {
	globalQueue *LockFreeQueue
	localQueues []*LockFreeQueue
	workers     int
	wg          sync.WaitGroup
	closed      int32
	minWorkers  int
	maxWorkers  int
	taskCount   int64
	stealCount  int64
	resizeCount int64
}

func NewLockFreeTaskPool(workers int) *LockFreeTaskPool {
	if workers <= 0 {
		workers = runtime.NumCPU()
	}

	pool := &LockFreeTaskPool{
		globalQueue: NewLockFreeQueue(),
		localQueues: make([]*LockFreeQueue, workers),
		workers:     workers,
		minWorkers:  workers / 2,
		maxWorkers:  workers * 2,
	}

	for i := 0; i < workers; i++ {
		pool.localQueues[i] = NewLockFreeQueue()
	}

	pool.wg.Add(workers)
	for i := 0; i < workers; i++ {
		go pool.worker(i)
	}

	go pool.adaptiveResize()

	return pool
}

func (p *LockFreeTaskPool) Go(f func()) {
	if atomic.LoadInt32(&p.closed) == 1 {
		return
	}

	atomic.AddInt64(&p.taskCount, 1)

	task := LockFreeTask{f: f}

	queueID := int(atomic.AddInt64(&p.taskCount, 1)) % p.workers
	p.localQueues[queueID].Enqueue(task)
}

func (p *LockFreeTaskPool) worker(id int) {
	defer p.wg.Done()

	localQueue := p.localQueues[id]

	for {
		if atomic.LoadInt32(&p.closed) == 1 {
			return
		}

		value, ok := localQueue.Dequeue()
		if !ok {
			value, ok = p.globalQueue.Dequeue()
			if !ok {
				value, ok = p.stealTask(id)
				if ok {
					atomic.AddInt64(&p.stealCount, 1)
				}
			}
		}

		if ok {
			task := value.(LockFreeTask)
			safeExecute(task.f)
		} else {
			time.Sleep(time.Millisecond)
		}
	}
}

func (p *LockFreeTaskPool) stealTask(selfID int) (interface{}, bool) {
	startID := int(atomic.AddInt64(&p.taskCount, 1)) % p.workers

	for i := 0; i < p.workers; i++ {
		victimID := (startID + i) % p.workers
		if victimID == selfID {
			continue
		}

		victim := p.localQueues[victimID]
		value, ok := victim.Dequeue()
		if ok {
			return value, true
		}
	}

	return nil, false
}

func (p *LockFreeTaskPool) adaptiveResize() {
	ticker := time.NewTicker(time.Second * 5)
	defer ticker.Stop()

	for {
		if atomic.LoadInt32(&p.closed) == 1 {
			return
		}

		<-ticker.C

		totalTasks := p.globalQueue.Size()
		for _, q := range p.localQueues {
			totalTasks += q.Size()
		}

		loadFactor := float64(totalTasks) / float64(p.workers)

		if loadFactor > 2.0 && p.workers < p.maxWorkers {
			p.addWorkers(p.workers / 4)
			atomic.AddInt64(&p.resizeCount, 1)
		} else if loadFactor < 0.5 && p.workers > p.minWorkers {
			p.removeWorkers(p.workers / 4)
			atomic.AddInt64(&p.resizeCount, 1)
		}
	}
}

func (p *LockFreeTaskPool) addWorkers(count int) {
	newWorkers := p.workers + count
	if newWorkers > p.maxWorkers {
		newWorkers = p.maxWorkers
	}

	if newWorkers <= p.workers {
		return
	}

	oldWorkers := p.workers
	newQueues := make([]*LockFreeQueue, newWorkers)
	copy(newQueues, p.localQueues)

	for i := oldWorkers; i < newWorkers; i++ {
		newQueues[i] = NewLockFreeQueue()
	}

	p.localQueues = newQueues
	p.workers = newWorkers

	p.wg.Add(newWorkers - oldWorkers)
	for i := oldWorkers; i < newWorkers; i++ {
		go p.worker(i)
	}
}

func (p *LockFreeTaskPool) removeWorkers(count int) {
	newWorkers := p.workers - count
	if newWorkers < p.minWorkers {
		newWorkers = p.minWorkers
	}

	if newWorkers >= p.workers {
		return
	}

	for i := newWorkers; i < p.workers; i++ {
		queue := p.localQueues[i]
		for {
			value, ok := queue.Dequeue()
			if !ok {
				break
			}
			p.globalQueue.Enqueue(value)
		}
	}

	p.workers = newWorkers
	p.localQueues = p.localQueues[:newWorkers]
}

func (p *LockFreeTaskPool) Stop() {
	if !atomic.CompareAndSwapInt32(&p.closed, 0, 1) {
		return
	}

	p.wg.Wait()
}

func (p *LockFreeTaskPool) GetStats() map[string]int64 {
	return map[string]int64{
		"workers":     int64(p.workers),
		"taskCount":   atomic.LoadInt64(&p.taskCount),
		"stealCount":  atomic.LoadInt64(&p.stealCount),
		"resizeCount": atomic.LoadInt64(&p.resizeCount),
		"queueSize":   p.globalQueue.Size(),
	}
}
