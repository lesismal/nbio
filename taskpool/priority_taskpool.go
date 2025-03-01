// Copyright 2020 lesismal. All rights reserved.
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package taskpool

import (
	"fmt"
	"runtime"
	"sync"
	"sync/atomic"
	"time"
)

type PriorityLevel int

const (
	PriorityHigh PriorityLevel = iota
	PriorityNormal
	PriorityLow
	PriorityCount
)

type PriorityTask struct {
	f        func()
	priority PriorityLevel
}

type PriorityTaskPool struct {
	workers     int
	queues      []*workerQueue
	wg          sync.WaitGroup
	closed      int32
	minWorkers  int
	maxWorkers  int
	taskCount   int64
	stealCount  int64
	resizeCount int64
}

type workerQueue struct {
	tasks [PriorityCount][]PriorityTask
	mu    sync.Mutex
	cond  *sync.Cond
	pool  *PriorityTaskPool
	id    int
	busy  bool
}

func NewPriorityTaskPool(workers int) *PriorityTaskPool {
	if workers <= 0 {
		workers = runtime.NumCPU()
	}

	pool := &PriorityTaskPool{
		workers:    workers,
		queues:     make([]*workerQueue, workers),
		minWorkers: workers / 2,
		maxWorkers: workers * 2,
	}

	for i := 0; i < workers; i++ {
		queue := &workerQueue{
			pool: pool,
			id:   i,
		}
		queue.cond = sync.NewCond(&queue.mu)
		pool.queues[i] = queue
	}

	pool.wg.Add(workers)
	for i := 0; i < workers; i++ {
		go pool.worker(i)
	}

	go pool.adaptiveResize()

	return pool
}

func (p *PriorityTaskPool) Go(f func()) {
	p.GoPriority(f, PriorityNormal)
}

func (p *PriorityTaskPool) GoPriority(f func(), priority PriorityLevel) {
	if atomic.LoadInt32(&p.closed) == 1 {
		return
	}

	atomic.AddInt64(&p.taskCount, 1)

	queueID := int(atomic.AddInt64(&p.taskCount, 1)) % p.workers
	queue := p.queues[queueID]

	task := PriorityTask{f: f, priority: priority}

	queue.mu.Lock()
	queue.tasks[priority] = append(queue.tasks[priority], task)
	queue.mu.Unlock()

	queue.cond.Signal()
}

func (p *PriorityTaskPool) worker(id int) {
	defer p.wg.Done()

	queue := p.queues[id]

	for {
		if atomic.LoadInt32(&p.closed) == 1 {
			return
		}

		task, ok := queue.getTask()
		if !ok {
			task, ok = p.stealTask(id)
			if ok {
				atomic.AddInt64(&p.stealCount, 1)
			}
		}

		if ok {
			queue.busy = true
			safeExecute(task.f)
			queue.busy = false
		} else {
			queue.mu.Lock()
			if !queue.hasTask() && atomic.LoadInt32(&p.closed) == 0 {
				queue.cond.Wait()
			}
			queue.mu.Unlock()
		}
	}
}

func (q *workerQueue) getTask() (PriorityTask, bool) {
	q.mu.Lock()
	defer q.mu.Unlock()

	for p := PriorityHigh; p < PriorityCount; p++ {
		if len(q.tasks[p]) > 0 {
			task := q.tasks[p][0]
			q.tasks[p] = q.tasks[p][1:]
			return task, true
		}
	}

	return PriorityTask{}, false
}

func (q *workerQueue) hasTask() bool {
	for p := PriorityHigh; p < PriorityCount; p++ {
		if len(q.tasks[p]) > 0 {
			return true
		}
	}
	return false
}

func (p *PriorityTaskPool) stealTask(selfID int) (PriorityTask, bool) {
	startID := int(atomic.AddInt64(&p.taskCount, 1)) % p.workers

	for i := 0; i < p.workers; i++ {
		victimID := (startID + i) % p.workers
		if victimID == selfID {
			continue
		}

		victim := p.queues[victimID]
		victim.mu.Lock()

		for p := PriorityHigh; p < PriorityCount; p++ {
			if len(victim.tasks[p]) > 0 {
				task := victim.tasks[p][0]
				victim.tasks[p] = victim.tasks[p][1:]
				victim.mu.Unlock()
				return task, true
			}
		}

		victim.mu.Unlock()
	}

	return PriorityTask{}, false
}

func (p *PriorityTaskPool) adaptiveResize() {
	ticker := time.NewTicker(time.Second * 5)
	defer ticker.Stop()

	for {
		if atomic.LoadInt32(&p.closed) == 1 {
			return
		}

		<-ticker.C

		busyCount := 0
		for _, q := range p.queues {
			if q.busy {
				busyCount++
			}
		}

		loadFactor := float64(busyCount) / float64(p.workers)

		if loadFactor > 0.8 && p.workers < p.maxWorkers {
			p.addWorkers(p.workers / 4)
			atomic.AddInt64(&p.resizeCount, 1)
		} else if loadFactor < 0.2 && p.workers > p.minWorkers {
			p.removeWorkers(p.workers / 4)
			atomic.AddInt64(&p.resizeCount, 1)
		}
	}
}

func (p *PriorityTaskPool) addWorkers(count int) {
	newWorkers := p.workers + count
	if newWorkers > p.maxWorkers {
		newWorkers = p.maxWorkers
	}

	if newWorkers <= p.workers {
		return
	}

	oldWorkers := p.workers
	newQueues := make([]*workerQueue, newWorkers)
	copy(newQueues, p.queues)

	for i := oldWorkers; i < newWorkers; i++ {
		queue := &workerQueue{
			pool: p,
			id:   i,
		}
		queue.cond = sync.NewCond(&queue.mu)
		newQueues[i] = queue
	}

	p.queues = newQueues
	p.workers = newWorkers

	p.wg.Add(newWorkers - oldWorkers)
	for i := oldWorkers; i < newWorkers; i++ {
		go p.worker(i)
	}
}

func (p *PriorityTaskPool) removeWorkers(count int) {
	newWorkers := p.workers - count
	if newWorkers < p.minWorkers {
		newWorkers = p.minWorkers
	}

	if newWorkers >= p.workers {
		return
	}

	for i := newWorkers; i < p.workers; i++ {
		queue := p.queues[i]
		queue.mu.Lock()

		for priority := PriorityHigh; priority < PriorityCount; priority++ {
			for _, task := range queue.tasks[priority] {
				targetID := int(atomic.AddInt64(&p.taskCount, 1)) % newWorkers
				target := p.queues[targetID]

				target.mu.Lock()
				target.tasks[priority] = append(target.tasks[priority], task)
				target.cond.Signal()
				target.mu.Unlock()
			}
			queue.tasks[priority] = nil
		}

		queue.mu.Unlock()
	}

	p.workers = newWorkers
	p.queues = p.queues[:newWorkers]
}

func (p *PriorityTaskPool) Stop() {
	if !atomic.CompareAndSwapInt32(&p.closed, 0, 1) {
		return
	}

	for _, queue := range p.queues {
		queue.cond.Broadcast()
	}

	p.wg.Wait()
}

func (p *PriorityTaskPool) GetStats() map[string]int64 {
	return map[string]int64{
		"workers":     int64(p.workers),
		"taskCount":   atomic.LoadInt64(&p.taskCount),
		"stealCount":  atomic.LoadInt64(&p.stealCount),
		"resizeCount": atomic.LoadInt64(&p.resizeCount),
	}
}

func safeExecute(f func()) {
	defer func() {
		if err := recover(); err != nil {
			buf := make([]byte, 64<<10)
			n := runtime.Stack(buf, false)
			buf = buf[:n]
			fmt.Printf("panic: %v\n%s\n", err, buf)
		}
	}()
	f()
}
