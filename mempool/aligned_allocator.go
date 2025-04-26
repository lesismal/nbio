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
	maxAlignedBufferSizeBits = 15
	minAlignedBufferSize     = 1 << minAlignedBufferSizeBits                           // 32
	minAlignedBufferSizeMask = minAlignedBufferSize - 1                                // 31
	maxAlignedBufferSize     = 1 << maxAlignedBufferSizeBits                           // 32k
	alignedPoolBucketNum     = maxAlignedBufferSizeBits - minAlignedBufferSizeBits + 1 // 12
)

//go:norace
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
//
//go:norace
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
//
//go:norace
func (amp *AlignedAllocator) Malloc(size int) *[]byte {
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
	amp.incrMalloc(&ret)
	return &ret
}

// Realloc .
//
//go:norace
func (amp *AlignedAllocator) Realloc(pbuf *[]byte, size int) *[]byte {
	if size <= cap(*pbuf) {
		*pbuf = (*pbuf)[:size]
		return pbuf
	}
	newBufPtr := amp.Malloc(size)
	copy(*newBufPtr, *pbuf)
	amp.Free(pbuf)
	return newBufPtr
}

// Append .
//
//go:norace
func (amp *AlignedAllocator) Append(pbuf *[]byte, more ...byte) *[]byte {
	if cap(*pbuf)-len(*pbuf) >= len(more) {
		*pbuf = append(*pbuf, more...)
		return pbuf
	}
	newBufPtr := amp.Malloc(len(*pbuf) + len(more))
	copy(*newBufPtr, *pbuf)
	copy((*newBufPtr)[len(*pbuf):], more)
	amp.Free(pbuf)
	return newBufPtr
}

// AppendString .
//
//go:norace
func (amp *AlignedAllocator) AppendString(pbuf *[]byte, s string) *[]byte {
	x := (*[2]uintptr)(unsafe.Pointer(&s))
	h := [3]uintptr{x[0], x[1], x[1]}
	more := *(*[]byte)(unsafe.Pointer(&h))
	return amp.Append(pbuf, more...)
}

// Free .
//
//go:norace
func (amp *AlignedAllocator) Free(pbuf *[]byte) {
	size := cap(*pbuf)
	if (size&minAlignedBufferSizeMask) != 0 || size > maxAlignedBufferSize {
		return
	}
	amp.incrFree(pbuf)
	idx := alignedIndexes[size]
	alignedPools[idx].Put(pbuf)
}
