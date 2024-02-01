package mempool

import (
	"sync"
	"unsafe"
)

var (
	alignedPools   [alignedPoolBucketNum]sync.Pool
	alignedIndexes [maxAlignedBufferSize + 1]byte
)

const (
	minAlignedBufferSizeBits = 5
	maxAlignedBufferSizeBits = 16
	minAlignedBufferSize     = 1 << minAlignedBufferSizeBits                           // 32
	minAlignedBufferSizeMask = minAlignedBufferSize - 1                                // 31
	maxAlignedBufferSize     = 1 << maxAlignedBufferSizeBits                           // 64k
	alignedPoolBucketNum     = maxAlignedBufferSizeBits - minAlignedBufferSizeBits + 1 // 12
)

func init() {
	var poolSizes [alignedPoolBucketNum]int
	for i := range alignedPools {
		size := 1 << (i + minAlignedBufferSizeBits)
		poolSizes[i] = size
		alignedPools[i].New = func() interface{} {
			b := make([]byte, size)
			return &b
		}
	}

	getPoolBySize := func(size int) byte {
		for i, n := range poolSizes {
			if size <= n {
				return byte(i)
			}
		}
		return 0xFF
	}

	for i := range alignedIndexes {
		alignedIndexes[i] = getPoolBySize(i)
	}
}

// NewAligned .
func NewAligned() Allocator {
	amp := &AlignedAllocator{
		debugger: &debugger{},
	}
	return amp
}

// AlignedAllocator .
type AlignedAllocator struct {
	*debugger
}

// Malloc .
func (amp *AlignedAllocator) Malloc(size int) []byte {
	if size < 0 {
		return nil
	}
	var ret []byte
	if size <= maxAlignedBufferSize {
		idx := alignedIndexes[size]
		ret = (*(alignedPools[idx].Get().(*[]byte)))[:size]
	} else {
		ret = make([]byte, size)
	}
	amp.incrMalloc(ret)
	return ret
}

// Realloc .
func (amp *AlignedAllocator) Realloc(buf []byte, size int) []byte {
	if size <= cap(buf) {
		return buf[:size]
	}
	newBuf := amp.Malloc(size)
	copy(newBuf, buf)
	return newBuf
}

// Append .
func (amp *AlignedAllocator) Append(buf []byte, more ...byte) []byte {
	if cap(buf)-len(buf) >= len(more) {
		return append(buf, more...)
	}
	newBuf := amp.Malloc(len(buf) + len(more))
	copy(newBuf, buf)
	copy(newBuf[len(buf):], more)
	return newBuf
}

// AppendString .
func (amp *AlignedAllocator) AppendString(buf []byte, s string) []byte {
	x := (*[2]uintptr)(unsafe.Pointer(&s))
	h := [3]uintptr{x[0], x[1], x[1]}
	more := *(*[]byte)(unsafe.Pointer(&h))
	return amp.Append(buf, more...)
}

// Free .
func (amp *AlignedAllocator) Free(buf []byte) {
	size := cap(buf)
	if (size&minAlignedBufferSizeMask) != 0 || size > maxAlignedBufferSize {
		return
	}
	amp.incrFree(buf)
	idx := alignedIndexes[size]
	alignedPools[idx].Put(&buf)
}
