// Copyright 2020 lesismal. All rights reserved.
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package mempool

import (
	"sync"
	"sync/atomic"
)

// TierConfig 表示一个内存池层级的配置
type TierConfig struct {
	// 块大小
	BlockSize int
	// 每个块组的块数量
	BlocksPerChunk int
	// 最大块组数量
	MaxChunks int
}

// Debugger 是内存分配器的调试接口
type Debugger interface {
	OnMalloc(buf *[]byte, size int)
	OnFree(buf *[]byte)
}

// StdAllocatorDebugger 是一个标准的调试器实现
type StdAllocatorDebugger struct{}

// OnMalloc 在分配内存时调用
func (d *StdAllocatorDebugger) OnMalloc(buf *[]byte, size int) {}

// OnFree 在释放内存时调用
func (d *StdAllocatorDebugger) OnFree(buf *[]byte) {}

// TieredAllocator 是一个分级内存池
// 它使用多个不同大小的内存池来减少内存碎片
type TieredAllocator struct {
	// 层级配置
	tiers []TierConfig
	// 层级池
	pools []*sync.Pool
	// 统计信息
	hits        []int64
	misses      []int64
	allocations []int64
	frees       []int64
	// 调试器
	debugger Debugger
}

// NewTieredAllocator 创建一个新的分级内存池
func NewTieredAllocator(sizes []int, maxSize int) *TieredAllocator {
	// 创建层级配置
	tiers := make([]TierConfig, len(sizes))
	for i, size := range sizes {
		tiers[i] = TierConfig{
			BlockSize:      size,
			BlocksPerChunk: 1024,
			MaxChunks:      64,
		}
	}

	return NewTieredAllocatorWithConfig(tiers, nil)
}

// NewTieredAllocatorWithConfig 使用配置创建一个新的分级内存池
func NewTieredAllocatorWithConfig(tiers []TierConfig, debugger Debugger) *TieredAllocator {
	if debugger == nil {
		debugger = &StdAllocatorDebugger{}
	}

	allocator := &TieredAllocator{
		tiers:       tiers,
		pools:       make([]*sync.Pool, len(tiers)),
		hits:        make([]int64, len(tiers)),
		misses:      make([]int64, len(tiers)),
		allocations: make([]int64, len(tiers)),
		frees:       make([]int64, len(tiers)),
		debugger:    debugger,
	}

	// 初始化每个层级的内存池
	for i, tier := range tiers {
		blockSize := tier.BlockSize
		index := i // 捕获变量
		allocator.pools[i] = &sync.Pool{
			New: func() interface{} {
				// 创建一个新的内存块
				atomic.AddInt64(&allocator.misses[index], 1)
				atomic.AddInt64(&allocator.allocations[index], 1)
				buf := make([]byte, blockSize)
				return &buf
			},
		}
	}

	return allocator
}

// Malloc 分配一个指定大小的内存块
func (a *TieredAllocator) Malloc(size int) *[]byte {
	// 找到合适的层级
	tierIndex := a.findTier(size)
	if tierIndex < 0 {
		// 如果没有合适的层级，直接分配
		buf := make([]byte, size)
		return &buf
	}

	// 从对应层级的池中获取内存块
	buf := a.pools[tierIndex].Get().(*[]byte)
	atomic.AddInt64(&a.hits[tierIndex], 1)

	// 调整大小
	if len(*buf) < size {
		// 如果内存块太小，重新分配
		*buf = make([]byte, a.tiers[tierIndex].BlockSize)
	}

	// 截断到请求的大小
	*buf = (*buf)[:size]

	// 调试信息
	a.debugger.OnMalloc(buf, size)

	return buf
}

// Free 释放一个内存块
func (a *TieredAllocator) Free(buf *[]byte) {
	if buf == nil || *buf == nil {
		return
	}

	// 调试信息
	a.debugger.OnFree(buf)

	// 找到合适的层级
	tierIndex := a.findTierByCapacity(cap(*buf))
	if tierIndex < 0 {
		// 如果没有合适的层级，直接丢弃
		*buf = nil
		return
	}

	// 重置内存块
	*buf = (*buf)[:cap(*buf)]

	// 归还到对应层级的池中
	a.pools[tierIndex].Put(buf)
	atomic.AddInt64(&a.frees[tierIndex], 1)
}

// Realloc 重新分配一个内存块
func (a *TieredAllocator) Realloc(buf *[]byte, size int) *[]byte {
	if buf == nil {
		return a.Malloc(size)
	}

	if *buf == nil {
		return a.Malloc(size)
	}

	if cap(*buf) >= size {
		// 如果现有容量足够，直接调整大小
		*buf = (*buf)[:size]
		return buf
	}

	// 分配新的内存块
	newBuf := a.Malloc(size)

	// 复制数据
	copy(*newBuf, *buf)

	// 释放旧的内存块
	a.Free(buf)

	return newBuf
}

// Append 追加数据到内存块
//
//go:nolint
func (a *TieredAllocator) Append(buf *[]byte, data ...byte) *[]byte {
	if buf == nil {
		newBuf := a.Malloc(len(data))
		copy(*newBuf, data)
		return newBuf
	}

	if *buf == nil {
		newBuf := a.Malloc(len(data))
		copy(*newBuf, data)
		return newBuf
	}

	// 计算新的大小
	newSize := len(*buf) + len(data)

	// 检查容量是否足够
	if cap(*buf) >= newSize {
		// 如果容量足够，直接追加
		*buf = append(*buf, data...)
		return buf
	}

	// 计算新的容量
	newCap := cap(*buf) * 2
	if newCap < newSize {
		newCap = newSize
	}

	// 分配新的内存块
	newBuf := a.Malloc(newCap)

	// 复制原始数据
	copy(*newBuf, *buf)

	// 追加新数据
	*newBuf = append((*newBuf)[:len(*buf)], data...)

	// 释放旧的内存块
	a.Free(buf)

	return newBuf
}

// AppendString 追加字符串到内存块
//
//go:nolint
func (a *TieredAllocator) AppendString(buf *[]byte, data string) *[]byte {
	if buf == nil {
		newBuf := a.Malloc(len(data))
		copy(*newBuf, data)
		return newBuf
	}

	if *buf == nil {
		newBuf := a.Malloc(len(data))
		copy(*newBuf, data)
		return newBuf
	}

	// 计算新的大小
	newSize := len(*buf) + len(data)

	// 检查容量是否足够
	if cap(*buf) >= newSize {
		// 如果容量足够，直接追加
		*buf = append(*buf, data...)
		return buf
	}

	// 计算新的容量
	newCap := cap(*buf) * 2
	if newCap < newSize {
		newCap = newSize
	}

	// 分配新的内存块
	newBuf := a.Malloc(newCap)

	// 复制原始数据
	copy(*newBuf, *buf)

	// 追加新数据
	*newBuf = append((*newBuf)[:len(*buf)], data...)

	// 释放旧的内存块
	a.Free(buf)

	return newBuf
}

// findTier 根据大小找到合适的层级
func (a *TieredAllocator) findTier(size int) int {
	for i, tier := range a.tiers {
		if size <= tier.BlockSize {
			return i
		}
	}
	return -1
}

// findTierByCapacity 根据容量找到合适的层级
func (a *TieredAllocator) findTierByCapacity(capacity int) int {
	for i, tier := range a.tiers {
		if capacity == tier.BlockSize {
			return i
		}
	}
	return -1
}

// GetStats 获取内存池的统计信息
func (a *TieredAllocator) GetStats() map[string]interface{} {
	stats := make(map[string]interface{})

	totalHits := int64(0)
	totalMisses := int64(0)
	totalAllocations := int64(0)
	totalFrees := int64(0)

	tierStats := make([]map[string]interface{}, len(a.tiers))

	for i, tier := range a.tiers {
		hits := atomic.LoadInt64(&a.hits[i])
		misses := atomic.LoadInt64(&a.misses[i])
		allocations := atomic.LoadInt64(&a.allocations[i])
		frees := atomic.LoadInt64(&a.frees[i])

		totalHits += hits
		totalMisses += misses
		totalAllocations += allocations
		totalFrees += frees

		tierStats[i] = map[string]interface{}{
			"blockSize":      tier.BlockSize,
			"blocksPerChunk": tier.BlocksPerChunk,
			"maxChunks":      tier.MaxChunks,
			"hits":           hits,
			"misses":         misses,
			"allocations":    allocations,
			"frees":          frees,
			"hitRatio":       float64(hits) / float64(hits+misses) * 100,
		}
	}

	stats["tiers"] = tierStats
	stats["totalHits"] = totalHits
	stats["totalMisses"] = totalMisses
	stats["totalAllocations"] = totalAllocations
	stats["totalFrees"] = totalFrees
	stats["totalHitRatio"] = float64(totalHits) / float64(totalHits+totalMisses) * 100

	return stats
}

// 注册全局内存池
var (
	// 全局内存池映射
	allocators = make(map[string]Allocator)
)

// RegisterAllocator 注册一个内存池
func RegisterAllocator(name string, allocator Allocator) {
	allocators[name] = allocator
}

// GetAllocator 获取一个内存池
func GetAllocator(name string) Allocator {
	if allocator, ok := allocators[name]; ok {
		return allocator
	}
	return DefaultMemPool
}

// 注册为默认内存池
func init() {
	// 创建默认的分级内存池配置
	sizes := []int{128, 256, 512, 1024, 2048, 4096, 8192, 16384, 32768, 65536}

	// 创建分级内存池
	tieredAllocator := NewTieredAllocator(sizes, 1024*1024*1024)

	// 注册为可选的内存池
	RegisterAllocator("tiered", tieredAllocator)
}
