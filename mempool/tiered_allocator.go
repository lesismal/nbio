// Copyright 2020 lesismal. All rights reserved.
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package mempool

import (
	"sync"
	"sync/atomic"
)

type TierConfig struct {
	BlockSize      int
	BlocksPerChunk int
	MaxChunks      int
}

type Debugger interface {
	OnMalloc(buf *[]byte, size int)
	OnFree(buf *[]byte)
}

type StdAllocatorDebugger struct{}

func (d *StdAllocatorDebugger) OnMalloc(buf *[]byte, size int) {}

func (d *StdAllocatorDebugger) OnFree(buf *[]byte) {}

type TieredAllocator struct {
	tiers       []TierConfig
	pools       []*sync.Pool
	hits        []int64
	misses      []int64
	allocations []int64
	frees       []int64
	debugger    Debugger
}

func NewTieredAllocator(sizes []int, maxSize int) *TieredAllocator {
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

	for i, tier := range tiers {
		blockSize := tier.BlockSize
		index := i
		allocator.pools[i] = &sync.Pool{
			New: func() interface{} {
				atomic.AddInt64(&allocator.misses[index], 1)
				atomic.AddInt64(&allocator.allocations[index], 1)
				buf := make([]byte, blockSize)
				return &buf
			},
		}
	}

	return allocator
}

func (a *TieredAllocator) Malloc(size int) *[]byte {
	tierIndex := a.findTier(size)
	if tierIndex < 0 {
		buf := make([]byte, size)
		return &buf
	}

	buf := a.pools[tierIndex].Get().(*[]byte)
	atomic.AddInt64(&a.hits[tierIndex], 1)

	if len(*buf) < size {
		*buf = make([]byte, a.tiers[tierIndex].BlockSize)
	}

	*buf = (*buf)[:size]

	a.debugger.OnMalloc(buf, size)

	return buf
}

func (a *TieredAllocator) Free(buf *[]byte) {
	if buf == nil || *buf == nil {
		return
	}

	a.debugger.OnFree(buf)

	tierIndex := a.findTierByCapacity(cap(*buf))
	if tierIndex < 0 {
		*buf = nil
		return
	}

	*buf = (*buf)[:cap(*buf)]

	a.pools[tierIndex].Put(buf)
	atomic.AddInt64(&a.frees[tierIndex], 1)
}

func (a *TieredAllocator) Realloc(buf *[]byte, size int) *[]byte {
	if buf == nil {
		return a.Malloc(size)
	}

	if *buf == nil {
		return a.Malloc(size)
	}

	if cap(*buf) >= size {
		*buf = (*buf)[:size]
		return buf
	}

	newBuf := a.Malloc(size)

	copy(*newBuf, *buf)

	a.Free(buf)

	return newBuf
}

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

	// Calculate new size
	newSize := len(*buf) + len(data)

	// Check if capacity is sufficient
	if cap(*buf) >= newSize {
		// If capacity is sufficient, append directly
		*buf = append(*buf, data...)
		return buf
	}

	// Calculate new capacity
	newCap := cap(*buf) * 2
	if newCap < newSize {
		newCap = newSize
	}

	// Allocate new memory block
	newBuf := a.Malloc(newCap)

	// Copy original data
	copy(*newBuf, *buf)

	// Append new data
	*newBuf = append((*newBuf)[:len(*buf)], data...)

	// Free old memory block
	a.Free(buf)

	return newBuf
}

func (a *TieredAllocator) AppendString(buf *[]byte, data string) *[]byte {
	return a.Append(buf, []byte(data)...)
}

func (a *TieredAllocator) findTier(size int) int {
	for i, tier := range a.tiers {
		if size <= tier.BlockSize {
			return i
		}
	}
	return -1
}

func (a *TieredAllocator) findTierByCapacity(capacity int) int {
	for i, tier := range a.tiers {
		if capacity == tier.BlockSize {
			return i
		}
	}
	return -1
}

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

var (
	allocators = make(map[string]Allocator)
)

func RegisterAllocator(name string, allocator Allocator) {
	allocators[name] = allocator
}

func GetAllocator(name string) Allocator {
	if allocator, ok := allocators[name]; ok {
		return allocator
	}
	return DefaultMemPool
}

func init() {
	sizes := []int{128, 256, 512, 1024, 2048, 4096, 8192, 16384, 32768, 65536}

	tieredAllocator := NewTieredAllocator(sizes, 1024*1024*1024)

	RegisterAllocator("tiered", tieredAllocator)
}
