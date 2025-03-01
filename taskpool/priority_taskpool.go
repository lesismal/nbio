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

// PriorityLevel 表示任务的优先级
type PriorityLevel int

const (
	// PriorityHigh 高优先级任务
	PriorityHigh PriorityLevel = iota
	// PriorityNormal 普通优先级任务
	PriorityNormal
	// PriorityLow 低优先级任务
	PriorityLow
	// PriorityCount 优先级数量
	PriorityCount
)

// PriorityTask 表示一个带优先级的任务
type PriorityTask struct {
	f        func()
	priority PriorityLevel
}

// PriorityTaskPool 是一个支持优先级和工作窃取的任务池
type PriorityTaskPool struct {
	// 工作线程数量
	workers int
	// 每个工作线程的任务队列
	queues []*workerQueue
	// 工作线程
	wg sync.WaitGroup
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

// workerQueue 表示一个工作线程的任务队列
type workerQueue struct {
	// 任务队列，按优先级分类
	tasks [PriorityCount][]PriorityTask
	// 互斥锁
	mu sync.Mutex
	// 条件变量，用于通知有新任务
	cond *sync.Cond
	// 所属的任务池
	pool *PriorityTaskPool
	// 工作线程ID
	id int
	// 是否正在处理任务
	busy bool
}

// NewPriorityTaskPool 创建一个新的优先级任务池
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

	// 初始化工作队列
	for i := 0; i < workers; i++ {
		queue := &workerQueue{
			pool: pool,
			id:   i,
		}
		queue.cond = sync.NewCond(&queue.mu)
		pool.queues[i] = queue
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
func (p *PriorityTaskPool) Go(f func()) {
	p.GoPriority(f, PriorityNormal)
}

// GoPriority 提交一个带优先级的任务到任务池
func (p *PriorityTaskPool) GoPriority(f func(), priority PriorityLevel) {
	if atomic.LoadInt32(&p.closed) == 1 {
		return
	}

	// 增加任务计数
	atomic.AddInt64(&p.taskCount, 1)

	// 选择一个工作队列
	queueID := int(atomic.AddInt64(&p.taskCount, 1)) % p.workers
	queue := p.queues[queueID]

	task := PriorityTask{f: f, priority: priority}

	queue.mu.Lock()
	queue.tasks[priority] = append(queue.tasks[priority], task)
	queue.mu.Unlock()

	// 通知工作线程有新任务
	queue.cond.Signal()
}

// worker 是工作线程的主循环
func (p *PriorityTaskPool) worker(id int) {
	defer p.wg.Done()

	queue := p.queues[id]

	for {
		// 检查是否已关闭
		if atomic.LoadInt32(&p.closed) == 1 {
			return
		}

		// 尝试从自己的队列获取任务
		task, ok := queue.getTask()
		if !ok {
			// 如果没有任务，尝试从其他队列窃取任务
			task, ok = p.stealTask(id)
			if ok {
				atomic.AddInt64(&p.stealCount, 1)
			}
		}

		if ok {
			queue.busy = true
			// 执行任务
			safeExecute(task.f)
			queue.busy = false
		} else {
			// 如果没有任务，等待通知
			queue.mu.Lock()
			if !queue.hasTask() && atomic.LoadInt32(&p.closed) == 0 {
				queue.cond.Wait()
			}
			queue.mu.Unlock()
		}
	}
}

// getTask 从队列中获取一个任务
func (q *workerQueue) getTask() (PriorityTask, bool) {
	q.mu.Lock()
	defer q.mu.Unlock()

	// 按优先级顺序获取任务
	for p := PriorityHigh; p < PriorityCount; p++ {
		if len(q.tasks[p]) > 0 {
			task := q.tasks[p][0]
			q.tasks[p] = q.tasks[p][1:]
			return task, true
		}
	}

	return PriorityTask{}, false
}

// hasTask 检查队列是否有任务
func (q *workerQueue) hasTask() bool {
	for p := PriorityHigh; p < PriorityCount; p++ {
		if len(q.tasks[p]) > 0 {
			return true
		}
	}
	return false
}

// stealTask 从其他队列窃取任务
func (p *PriorityTaskPool) stealTask(selfID int) (PriorityTask, bool) {
	// 随机选择起始队列
	startID := int(atomic.AddInt64(&p.taskCount, 1)) % p.workers

	// 尝试从其他队列窃取任务
	for i := 0; i < p.workers; i++ {
		victimID := (startID + i) % p.workers
		if victimID == selfID {
			continue
		}

		victim := p.queues[victimID]
		victim.mu.Lock()

		// 按优先级顺序窃取任务
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

// adaptiveResize 自适应调整工作线程数量
func (p *PriorityTaskPool) adaptiveResize() {
	ticker := time.NewTicker(time.Second * 5)
	defer ticker.Stop()

	for {
		if atomic.LoadInt32(&p.closed) == 1 {
			return
		}

		<-ticker.C

		// 计算忙碌的工作线程数量
		busyCount := 0
		for _, q := range p.queues {
			if q.busy {
				busyCount++
			}
		}

		// 计算负载率
		loadFactor := float64(busyCount) / float64(p.workers)

		// 根据负载率调整工作线程数量
		if loadFactor > 0.8 && p.workers < p.maxWorkers {
			// 负载高，增加工作线程
			p.addWorkers(p.workers / 4)
			atomic.AddInt64(&p.resizeCount, 1)
		} else if loadFactor < 0.2 && p.workers > p.minWorkers {
			// 负载低，减少工作线程
			p.removeWorkers(p.workers / 4)
			atomic.AddInt64(&p.resizeCount, 1)
		}
	}
}

// addWorkers 增加工作线程
func (p *PriorityTaskPool) addWorkers(count int) {
	newWorkers := p.workers + count
	if newWorkers > p.maxWorkers {
		newWorkers = p.maxWorkers
	}

	if newWorkers <= p.workers {
		return
	}

	// 创建新的工作队列
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

	// 启动新的工作线程
	p.wg.Add(newWorkers - oldWorkers)
	for i := oldWorkers; i < newWorkers; i++ {
		go p.worker(i)
	}
}

// removeWorkers 减少工作线程
func (p *PriorityTaskPool) removeWorkers(count int) {
	newWorkers := p.workers - count
	if newWorkers < p.minWorkers {
		newWorkers = p.minWorkers
	}

	if newWorkers >= p.workers {
		return
	}

	// 将要移除的工作队列中的任务重新分配
	for i := newWorkers; i < p.workers; i++ {
		queue := p.queues[i]
		queue.mu.Lock()

		// 将任务重新分配到其他队列
		for priority := PriorityHigh; priority < PriorityCount; priority++ {
			for _, task := range queue.tasks[priority] {
				// 选择一个新的队列
				targetID := int(atomic.AddInt64(&p.taskCount, 1)) % newWorkers
				target := p.queues[targetID]

				target.mu.Lock()
				target.tasks[priority] = append(target.tasks[priority], task)
				target.cond.Signal()
				target.mu.Unlock()
			}
			queue.tasks[priority] = nil
		}

		// 标记为关闭
		queue.mu.Unlock()
	}

	// 更新工作线程数量
	p.workers = newWorkers
	p.queues = p.queues[:newWorkers]
}

// Stop 停止任务池
func (p *PriorityTaskPool) Stop() {
	if !atomic.CompareAndSwapInt32(&p.closed, 0, 1) {
		return
	}

	// 通知所有工作线程退出
	for _, queue := range p.queues {
		queue.cond.Broadcast()
	}

	// 等待所有工作线程退出
	p.wg.Wait()
}

// GetStats 获取任务池的统计信息
func (p *PriorityTaskPool) GetStats() map[string]int64 {
	return map[string]int64{
		"workers":     int64(p.workers),
		"taskCount":   atomic.LoadInt64(&p.taskCount),
		"stealCount":  atomic.LoadInt64(&p.stealCount),
		"resizeCount": atomic.LoadInt64(&p.resizeCount),
	}
}

// safeExecute 安全地执行函数，捕获panic
func safeExecute(f func()) {
	defer func() {
		if err := recover(); err != nil {
			buf := make([]byte, 64<<10)
			n := runtime.Stack(buf, false)
			buf = buf[:n]
			// 记录panic信息
			// logging.Error("panic in task: %v\n%s", err, buf)
		}
	}()
	f()
}
