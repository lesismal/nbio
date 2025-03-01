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

// LockFreeTask 表示一个任务
type LockFreeTask struct {
	f func()
}

// LockFreeTaskPool 是一个基于无锁队列的任务池
type LockFreeTaskPool struct {
	// 全局任务队列
	globalQueue *LockFreeQueue
	// 每个工作线程的本地队列
	localQueues []*LockFreeQueue
	// 工作线程
	workers int
	wg      sync.WaitGroup
	// 是否已关闭
	closed int32
	// 自适应大小调整
	minWorkers int
	maxWorkers int
	// 负载统计
	taskCount   int64
	stealCount  int64
	resizeCount int64
}

// NewLockFreeTaskPool 创建一个新的无锁任务池
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

	// 初始化本地队列
	for i := 0; i < workers; i++ {
		pool.localQueues[i] = NewLockFreeQueue()
	}

	// 启动工作线程
	pool.wg.Add(workers)
	for i := 0; i < workers; i++ {
		go pool.worker(i)
	}

	// 启动自适应大小调整
	go pool.adaptiveResize()

	return pool
}

// Go 提交一个任务到任务池
func (p *LockFreeTaskPool) Go(f func()) {
	if atomic.LoadInt32(&p.closed) == 1 {
		return
	}

	// 增加任务计数
	atomic.AddInt64(&p.taskCount, 1)

	// 创建任务
	task := LockFreeTask{f: f}

	// 选择一个本地队列
	queueID := int(atomic.AddInt64(&p.taskCount, 1)) % p.workers
	p.localQueues[queueID].Enqueue(task)
}

// worker 是工作线程的主循环
func (p *LockFreeTaskPool) worker(id int) {
	defer p.wg.Done()

	localQueue := p.localQueues[id]

	for {
		// 检查是否已关闭
		if atomic.LoadInt32(&p.closed) == 1 {
			return
		}

		// 尝试从本地队列获取任务
		value, ok := localQueue.Dequeue()
		if !ok {
			// 尝试从全局队列获取任务
			value, ok = p.globalQueue.Dequeue()
			if !ok {
				// 尝试从其他队列窃取任务
				value, ok = p.stealTask(id)
				if ok {
					atomic.AddInt64(&p.stealCount, 1)
				}
			}
		}

		if ok {
			// 执行任务
			task := value.(LockFreeTask)
			safeExecute(task.f)
		} else {
			// 如果没有任务，短暂休眠
			time.Sleep(time.Millisecond)
		}
	}
}

// stealTask 从其他队列窃取任务
func (p *LockFreeTaskPool) stealTask(selfID int) (interface{}, bool) {
	// 随机选择起始队列
	startID := int(atomic.AddInt64(&p.taskCount, 1)) % p.workers

	// 尝试从其他队列窃取任务
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

// adaptiveResize 自适应调整工作线程数量
func (p *LockFreeTaskPool) adaptiveResize() {
	ticker := time.NewTicker(time.Second * 5)
	defer ticker.Stop()

	for {
		if atomic.LoadInt32(&p.closed) == 1 {
			return
		}

		<-ticker.C

		// 计算队列中的任务数量
		totalTasks := p.globalQueue.Size()
		for _, q := range p.localQueues {
			totalTasks += q.Size()
		}

		// 计算负载率
		loadFactor := float64(totalTasks) / float64(p.workers)

		// 根据负载率调整工作线程数量
		if loadFactor > 2.0 && p.workers < p.maxWorkers {
			// 负载高，增加工作线程
			p.addWorkers(p.workers / 4)
			atomic.AddInt64(&p.resizeCount, 1)
		} else if loadFactor < 0.5 && p.workers > p.minWorkers {
			// 负载低，减少工作线程
			p.removeWorkers(p.workers / 4)
			atomic.AddInt64(&p.resizeCount, 1)
		}
	}
}

// addWorkers 增加工作线程
func (p *LockFreeTaskPool) addWorkers(count int) {
	newWorkers := p.workers + count
	if newWorkers > p.maxWorkers {
		newWorkers = p.maxWorkers
	}

	if newWorkers <= p.workers {
		return
	}

	// 创建新的本地队列
	oldWorkers := p.workers
	newQueues := make([]*LockFreeQueue, newWorkers)
	copy(newQueues, p.localQueues)

	for i := oldWorkers; i < newWorkers; i++ {
		newQueues[i] = NewLockFreeQueue()
	}

	p.localQueues = newQueues
	p.workers = newWorkers

	// 启动新的工作线程
	p.wg.Add(newWorkers - oldWorkers)
	for i := oldWorkers; i < newWorkers; i++ {
		go p.worker(i)
	}
}

// removeWorkers 减少工作线程
func (p *LockFreeTaskPool) removeWorkers(count int) {
	newWorkers := p.workers - count
	if newWorkers < p.minWorkers {
		newWorkers = p.minWorkers
	}

	if newWorkers >= p.workers {
		return
	}

	// 将要移除的本地队列中的任务重新分配到全局队列
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

	// 更新工作线程数量
	p.workers = newWorkers
	p.localQueues = p.localQueues[:newWorkers]
}

// Stop 停止任务池
func (p *LockFreeTaskPool) Stop() {
	if !atomic.CompareAndSwapInt32(&p.closed, 0, 1) {
		return
	}

	// 等待所有工作线程退出
	p.wg.Wait()
}

// GetStats 获取任务池的统计信息
func (p *LockFreeTaskPool) GetStats() map[string]int64 {
	return map[string]int64{
		"workers":     int64(p.workers),
		"taskCount":   atomic.LoadInt64(&p.taskCount),
		"stealCount":  atomic.LoadInt64(&p.stealCount),
		"resizeCount": atomic.LoadInt64(&p.resizeCount),
		"queueSize":   p.globalQueue.Size(),
	}
}
