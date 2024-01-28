package mempool

import (
	"sync"
	"unsafe"
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
	println("minAlignedBufferSizeBits:", minAlignedBufferSizeBits)
	println("maxAlignedBufferSizeBits:", maxAlignedBufferSizeBits)
	println("minAlignedBufferSize:", minAlignedBufferSize)
	println("minAlignedBufferSizeMask:", minAlignedBufferSizeMask)
	println("maxAlignedBufferSize:", maxAlignedBufferSize)
	println("alignedPoolBucketNum:", alignedPoolBucketNum)
}

// NewAligned .
func NewAligned() Allocator {
	amp := &AlignedAllocator{
		debugger: &debugger{},
	}
	amp.init()
	return amp
}

// AlignedAllocator .
type AlignedAllocator struct {
	*debugger
	pools   [alignedPoolBucketNum]sync.Pool
	indexes [maxAlignedBufferSize/2 + 1]byte
}

func (amp *AlignedAllocator) init() {
	var poolSizes [alignedPoolBucketNum]int
	for i := range amp.pools {
		size := 1 << (i + minAlignedBufferSizeBits)
		poolSizes[i] = size
		amp.pools[i].New = func() interface{} {
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

	for i := range amp.indexes {
		n := i << 1
		shift := (n & 0x1) << 2
		amp.indexes[n>>1] |= (getPoolBySize(n) << shift)
		n = i<<1 + 1
		shift = (n & 0x1) << 2
		amp.indexes[n>>1] |= (getPoolBySize(n) << shift)
	}
}

func (amp *AlignedAllocator) index(size int) byte {
	v := amp.indexes[size>>1]
	shift := (size & 0x1) << 2
	return (v >> shift) & 0xF
}

// Malloc .
func (amp *AlignedAllocator) Malloc(size int) []byte {
	if size < 0 {
		return nil
	}
	var ret []byte
	if size <= maxAlignedBufferSize {
		idx := amp.index(size)
		ret = (*(amp.pools[idx].Get().(*[]byte)))[:size]
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
	idx := amp.index(size)
	amp.pools[idx].Put(&buf)
}
