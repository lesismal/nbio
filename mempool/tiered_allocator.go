// Copyright 2020 lesismal. All rights reserved.
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package mempool

import (
	"sync"
	"sync/atomic"
)

// TierConfig represents the configuration of a memory pool tier
type TierConfig struct {
	// Block size
	BlockSize int
	// Number of blocks per chunk
	BlocksPerChunk int
	// Maximum number of chunks
	MaxChunks int
}

// Debugger is a debugging interface for memory allocators
type Debugger interface {
	OnMalloc(buf *[]byte, size int)
	OnFree(buf *[]byte)
}

// StdAllocatorDebugger is a standard debugger implementation
type StdAllocatorDebugger struct{}

// OnMalloc is called when memory is allocated
func (d *StdAllocatorDebugger) OnMalloc(buf *[]byte, size int) {}

// OnFree is called when memory is freed
func (d *StdAllocatorDebugger) OnFree(buf *[]byte) {}

// TieredAllocator is a tiered memory pool
// It uses multiple memory pools of different sizes to reduce memory fragmentation
type TieredAllocator struct {
	// Tier configurations
	tiers []TierConfig
	// Tier pools
	pools []*sync.Pool
	// Statistics
	hits        []int64
	misses      []int64
	allocations []int64
	frees       []int64
	// Debugger
	debugger Debugger
}

// NewTieredAllocator creates a new tiered memory pool
func NewTieredAllocator(sizes []int, maxSize int) *TieredAllocator {
	// Create tier configurations
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

// NewTieredAllocatorWithConfig creates a new tiered memory pool with configuration
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

	// Initialize memory pool for each tier
	for i, tier := range tiers {
		blockSize := tier.BlockSize
		index := i // 捕获变量
		allocator.pools[i] = &sync.Pool{
			New: func() interface{} {
				// Create a new memory block
				atomic.AddInt64(&allocator.misses[index], 1)
				atomic.AddInt64(&allocator.allocations[index], 1)
				buf := make([]byte, blockSize)
				return &buf
			},
		}
	}

	return allocator
}

// Malloc allocates a memory block of specified size
func (a *TieredAllocator) Malloc(size int) *[]byte {
	// Find the appropriate tier
	tierIndex := a.findTier(size)
	if tierIndex < 0 {
		// If no appropriate tier, allocate directly
		buf := make([]byte, size)
		return &buf
	}

	// Get memory block from the corresponding tier pool
	buf := a.pools[tierIndex].Get().(*[]byte)
	atomic.AddInt64(&a.hits[tierIndex], 1)

	// Adjust size
	if len(*buf) < size {
		// If memory block is too small, reallocate
		*buf = make([]byte, a.tiers[tierIndex].BlockSize)
	}

	// Truncate to requested size
	*buf = (*buf)[:size]

	// Debug information
	a.debugger.OnMalloc(buf, size)

	return buf
}

// Free releases a memory block
func (a *TieredAllocator) Free(buf *[]byte) {
	if buf == nil || *buf == nil {
		return
	}

	// Debug information
	a.debugger.OnFree(buf)

	// Find the appropriate tier
	tierIndex := a.findTierByCapacity(cap(*buf))
	if tierIndex < 0 {
		// If no appropriate tier, discard directly
		*buf = nil
		return
	}

	// Reset memory block
	*buf = (*buf)[:cap(*buf)]

	// Return to the corresponding tier pool
	a.pools[tierIndex].Put(buf)
	atomic.AddInt64(&a.frees[tierIndex], 1)
}

// Realloc reallocates a memory block
func (a *TieredAllocator) Realloc(buf *[]byte, size int) *[]byte {
	if buf == nil {
		return a.Malloc(size)
	}

	if *buf == nil {
		return a.Malloc(size)
	}

	if cap(*buf) >= size {
		// If existing capacity is sufficient, adjust size directly
		*buf = (*buf)[:size]
		return buf
	}

	// Allocate new memory block
	newBuf := a.Malloc(size)

	// Copy data
	copy(*newBuf, *buf)

	// Free old memory block
	a.Free(buf)

	return newBuf
}

// Append appends data to a memory block
//
//nolint:golint
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

// AppendString appends a string to a memory block
//
//nolint:golint
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

// findTier finds the appropriate tier based on size
func (a *TieredAllocator) findTier(size int) int {
	for i, tier := range a.tiers {
		if size <= tier.BlockSize {
			return i
		}
	}
	return -1
}

// findTierByCapacity finds the appropriate tier based on capacity
func (a *TieredAllocator) findTierByCapacity(capacity int) int {
	for i, tier := range a.tiers {
		if capacity == tier.BlockSize {
			return i
		}
	}
	return -1
}

// GetStats gets memory pool statistics
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

// Register global memory pool
var (
	// Global memory pool map
	allocators = make(map[string]Allocator)
)

// RegisterAllocator registers a memory pool
func RegisterAllocator(name string, allocator Allocator) {
	allocators[name] = allocator
}

// GetAllocator gets a memory pool
func GetAllocator(name string) Allocator {
	if allocator, ok := allocators[name]; ok {
		return allocator
	}
	return DefaultMemPool
}

// Register as default memory pool
func init() {
	// Create default tiered memory pool configuration
	sizes := []int{128, 256, 512, 1024, 2048, 4096, 8192, 16384, 32768, 65536}

	// Create tiered memory pool
	tieredAllocator := NewTieredAllocator(sizes, 1024*1024*1024)

	// Register as optional memory pool
	RegisterAllocator("tiered", tieredAllocator)
}
