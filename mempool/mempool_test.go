package mempool

import (
	"testing"
)

func TestMemPool(t *testing.T) {
	pool := New(1024*1024*1024, 1024*1024*1024)
	for i := 0; i < 1024*1024; i++ {
		buf := pool.Malloc(i)
		if len(buf) != i {
			t.Fatalf("invalid len: %v != %v", len(buf), i)
		}
		pool.Free(buf)
	}
	for i := 1024 * 1024; i < 1024*1024*1024; i += 1024 * 1024 {
		buf := pool.Malloc(i)
		if len(buf) != i {
			t.Fatalf("invalid len: %v != %v", len(buf), i)
		}
		pool.Free(buf)
	}

	buf := pool.Malloc(0)
	for i := 1; i < 1024*1024; i++ {
		buf = pool.Realloc(buf, i)
		if len(buf) != i {
			t.Fatalf("invalid len: %v != %v", len(buf), i)
		}
	}
	pool.Free(buf)
}

func TestAlignedMemPool(t *testing.T) {
	pool := NewAligned()
	for i := 0; i < 1024*64+1024; i += 1 {
		buf := pool.MallocAligned(i)
		if len(buf) != i {
			t.Fatalf("invalid length: %v != %v", len(buf), i)
		}
		pool.FreeAligned(buf)
	}
	for i := minAlignedBufferSizeBits; i < maxAlignedBufferSizeBits; i++ {
		size := 1 << i
		buf := pool.MallocAligned(size)
		if len(buf) != size || cap(buf) > size*2 {
			t.Fatalf("invalid len or cap: %v, %v %v, %v ", i, len(buf), cap(buf), size)
		}
		buf = pool.MallocAligned(size + 1)
		if i != maxAlignedBufferSizeBits-1 {
			if len(buf) != size+1 || cap(buf) != size*2 || cap(buf) > (size+1)*2 {
				t.Fatalf("invalid len or cap: %v, %v %v, %v ", i, len(buf), cap(buf), size)
			}
		} else {
			if len(buf) != size+1 || cap(buf) != size+1 {
				t.Fatalf("invalid len or cap: %v, %v %v, %v ", i, len(buf), cap(buf), size)
			}
		}
		pool.FreeAligned(buf)
	}
	for i := -10; i < 0; i++ {
		buf := pool.MallocAligned(i)
		if buf != nil {
			t.Fatalf("invalid malloc, should be nil but got: %v, %v", len(buf), cap(buf))
		}
	}
	for i := 1 << maxAlignedBufferSizeBits; i < 1<<maxAlignedBufferSizeBits+1024; i++ {
		buf := pool.MallocAligned(i)
		if len(buf) != i || cap(buf) != i {
			t.Fatalf("invalid len or cap: %v, %v, %v ", i, len(buf), cap(buf))
		}
	}
}
