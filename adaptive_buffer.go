// Copyright 2020 lesismal. All rights reserved.
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package nbio

import (
	"sync"
	"sync/atomic"
	"time"
)

// AdaptiveBufferConfig 自适应缓冲区配置
type AdaptiveBufferConfig struct {
	// 初始缓冲区大小
	InitialSize int
	// 最小缓冲区大小
	MinSize int
	// 最大缓冲区大小
	MaxSize int
	// 历史记录大小
	HistorySize int
	// 增长因子
	GrowFactor float64
	// 收缩因子
	ShrinkFactor float64
	// 调整间隔
	ResizeInterval time.Duration
}

// AdaptiveBuffer 是一个自适应大小的缓冲区池
// 它会根据历史使用情况自动调整缓冲区大小
type AdaptiveBuffer struct {
	// 配置
	config AdaptiveBufferConfig
	// 缓冲区池
	pool sync.Pool
	// 使用历史
	usageHistory []int
	historyIndex int
	// 上次调整时间
	lastResizeTime time.Time
	// 统计信息
	growCount   int64
	shrinkCount int64
	currentSize int64
	// 互斥锁
	mu sync.Mutex
}

// NewAdaptiveBuffer 创建一个新的自适应缓冲区
func NewAdaptiveBuffer(config AdaptiveBufferConfig) *AdaptiveBuffer {
	// 设置默认值
	if config.InitialSize <= 0 {
		config.InitialSize = DefaultReadBufferSize
	}
	if config.MinSize <= 0 {
		config.MinSize = DefaultReadBufferSize / 2
	}
	if config.MaxSize <= 0 {
		config.MaxSize = DefaultReadBufferSize * 4
	}
	if config.HistorySize <= 0 {
		config.HistorySize = 10
	}
	if config.GrowFactor <= 0 {
		config.GrowFactor = 1.5
	}
	if config.ShrinkFactor <= 0 {
		config.ShrinkFactor = 0.5
	}
	if config.ResizeInterval <= 0 {
		config.ResizeInterval = time.Second * 10
	}

	ab := &AdaptiveBuffer{
		config:         config,
		usageHistory:   make([]int, config.HistorySize),
		historyIndex:   0,
		lastResizeTime: time.Now(),
	}

	// 初始化缓冲区池
	ab.pool.New = func() interface{} {
		buf := make([]byte, config.InitialSize)
		return &buf
	}

	return ab
}

// Get 从池中获取一个缓冲区
func (b *AdaptiveBuffer) Get() *[]byte {
	return b.pool.Get().(*[]byte)
}

// Put 将缓冲区归还到池中
func (b *AdaptiveBuffer) Put(buf *[]byte) {
	// 如果缓冲区大小不符合当前大小，不放回池中
	if len(*buf) != int(atomic.LoadInt64(&b.currentSize)) {
		// 创建一个新的缓冲区
		newBuf := make([]byte, atomic.LoadInt64(&b.currentSize))
		*buf = newBuf
	}

	b.pool.Put(buf)
}

// RecordRead 记录读取的字节数，用于自适应调整
func (b *AdaptiveBuffer) RecordRead(size int) {
	b.mu.Lock()
	defer b.mu.Unlock()

	// 记录使用率
	currentSize := atomic.LoadInt64(&b.currentSize)
	if currentSize == 0 {
		currentSize = int64(b.config.InitialSize)
	}

	usageRatio := float64(size) / float64(currentSize)
	usagePercent := int(usageRatio * 100)

	// 更新历史记录
	b.usageHistory[b.historyIndex] = usagePercent
	b.historyIndex = (b.historyIndex + 1) % len(b.usageHistory)

	// 检查是否需要调整大小
	b.maybeResize(size)
}

// maybeResize 根据使用情况调整缓冲区大小
func (b *AdaptiveBuffer) maybeResize(lastReadSize int) {
	// 检查是否达到调整间隔
	if time.Since(b.lastResizeTime) < b.config.ResizeInterval {
		return
	}

	// 计算平均使用率
	sum := 0
	for _, usage := range b.usageHistory {
		sum += usage
	}
	avgUsage := float64(sum) / float64(len(b.usageHistory))

	// 获取当前大小
	currentSize := atomic.LoadInt64(&b.currentSize)
	if currentSize == 0 {
		currentSize = int64(b.config.InitialSize)
		atomic.StoreInt64(&b.currentSize, currentSize)
	}

	// 根据平均使用率调整大小
	if avgUsage > 80 && currentSize < int64(b.config.MaxSize) {
		// 使用率高，扩大缓冲区
		newSize := int64(float64(currentSize) * b.config.GrowFactor)
		if newSize > int64(b.config.MaxSize) {
			newSize = int64(b.config.MaxSize)
		}
		atomic.StoreInt64(&b.currentSize, newSize)
		atomic.AddInt64(&b.growCount, 1)
	} else if avgUsage < 30 && currentSize > int64(b.config.MinSize) && lastReadSize < int(currentSize/2) {
		// 使用率低，缩小缓冲区
		newSize := int64(float64(currentSize) * b.config.ShrinkFactor)
		if newSize < int64(b.config.MinSize) {
			newSize = int64(b.config.MinSize)
		}
		atomic.StoreInt64(&b.currentSize, newSize)
		atomic.AddInt64(&b.shrinkCount, 1)
	}

	b.lastResizeTime = time.Now()
}

// GetStats 获取缓冲区的统计信息
func (b *AdaptiveBuffer) GetStats() map[string]interface{} {
	b.mu.Lock()
	defer b.mu.Unlock()

	// 计算平均使用率
	sum := 0
	for _, usage := range b.usageHistory {
		sum += usage
	}
	avgUsage := float64(sum) / float64(len(b.usageHistory))

	return map[string]interface{}{
		"currentSize": atomic.LoadInt64(&b.currentSize),
		"minSize":     b.config.MinSize,
		"maxSize":     b.config.MaxSize,
		"avgUsage":    avgUsage,
		"growCount":   atomic.LoadInt64(&b.growCount),
		"shrinkCount": atomic.LoadInt64(&b.shrinkCount),
	}
}
