// Copyright 2020 lesismal. All rights reserved.
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package nbio

import (
	"sync"
	"sync/atomic"
	"time"
)

type AdaptiveBufferConfig struct {
	InitialSize    int
	MinSize        int
	MaxSize        int
	HistorySize    int
	GrowFactor     float64
	ShrinkFactor   float64
	ResizeInterval time.Duration
}

type AdaptiveBuffer struct {
	config         AdaptiveBufferConfig
	pool           sync.Pool
	usageHistory   []int
	historyIndex   int
	lastResizeTime time.Time
	growCount      int64
	shrinkCount    int64
	currentSize    int64
	mu             sync.Mutex
}

func NewAdaptiveBuffer(config AdaptiveBufferConfig) *AdaptiveBuffer {
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

	ab.pool.New = func() interface{} {
		buf := make([]byte, config.InitialSize)
		return &buf
	}

	return ab
}

func (b *AdaptiveBuffer) Get() *[]byte {
	return b.pool.Get().(*[]byte)
}

func (b *AdaptiveBuffer) Put(buf *[]byte) {
	if len(*buf) != int(atomic.LoadInt64(&b.currentSize)) {
		newBuf := make([]byte, atomic.LoadInt64(&b.currentSize))
		*buf = newBuf
	}

	b.pool.Put(buf)
}

func (b *AdaptiveBuffer) RecordRead(size int) {
	b.mu.Lock()
	defer b.mu.Unlock()

	currentSize := atomic.LoadInt64(&b.currentSize)
	if currentSize == 0 {
		currentSize = int64(b.config.InitialSize)
	}

	usageRatio := float64(size) / float64(currentSize)
	usagePercent := int(usageRatio * 100)

	b.usageHistory[b.historyIndex] = usagePercent
	b.historyIndex = (b.historyIndex + 1) % len(b.usageHistory)

	b.maybeResize(size)
}

func (b *AdaptiveBuffer) maybeResize(lastReadSize int) {
	if time.Since(b.lastResizeTime) < b.config.ResizeInterval {
		return
	}

	sum := 0
	for _, usage := range b.usageHistory {
		sum += usage
	}
	avgUsage := float64(sum) / float64(len(b.usageHistory))

	currentSize := atomic.LoadInt64(&b.currentSize)
	if currentSize == 0 {
		currentSize = int64(b.config.InitialSize)
		atomic.StoreInt64(&b.currentSize, currentSize)
	}

	if avgUsage > 80 && currentSize < int64(b.config.MaxSize) {
		newSize := int64(float64(currentSize) * b.config.GrowFactor)
		if newSize > int64(b.config.MaxSize) {
			newSize = int64(b.config.MaxSize)
		}
		atomic.StoreInt64(&b.currentSize, newSize)
		atomic.AddInt64(&b.growCount, 1)
	} else if avgUsage < 30 && currentSize > int64(b.config.MinSize) && lastReadSize < int(currentSize/2) {
		newSize := int64(float64(currentSize) * b.config.ShrinkFactor)
		if newSize < int64(b.config.MinSize) {
			newSize = int64(b.config.MinSize)
		}
		atomic.StoreInt64(&b.currentSize, newSize)
		atomic.AddInt64(&b.shrinkCount, 1)
	}

	b.lastResizeTime = time.Now()
}

func (b *AdaptiveBuffer) GetStats() map[string]interface{} {
	b.mu.Lock()
	defer b.mu.Unlock()

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
