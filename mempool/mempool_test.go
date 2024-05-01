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
	b := pool.Malloc(32769)
	pool.Free(b)
	pool.Free(make([]byte, 60001))
	for i := 0; i < 1024*64+1024; i += 1 {
		buf := pool.Malloc(i)
		if len(buf) != i {
			t.Fatalf("invalid length: %v != %v", len(buf), i)
		}
		pool.Free(buf)
	}
	for i := minAlignedBufferSizeBits; i < maxAlignedBufferSizeBits; i++ {
		size := 1 << i
		buf := pool.Malloc(size)
		if len(buf) != size || cap(buf) > size*2 {
			t.Fatalf("invalid len or cap: %v, %v %v, %v ", i, len(buf), cap(buf), size)
		}
		buf = pool.Malloc(size + 1)
		if i != maxAlignedBufferSizeBits {
			if len(buf) != size+1 || cap(buf) != size*2 || cap(buf) > (size+1)*2 {
				t.Fatalf("invalid len or cap: %v, %v %v, %v ", i, len(buf), cap(buf), size)
			}
		} else {
			if len(buf) != size+1 || cap(buf) != size+1 {
				t.Fatalf("invalid len or cap: %v, %v %v, %v ", i, len(buf), cap(buf), size)
			}
		}
		pool.Free(buf)
	}
	for i := -10; i < 0; i++ {
		buf := pool.Malloc(i)
		if buf != nil {
			t.Fatalf("invalid malloc, should be nil but got: %v, %v", len(buf), cap(buf))
		}
	}
	for i := 1 << maxAlignedBufferSizeBits; i < 1<<maxAlignedBufferSizeBits+1024; i++ {
		buf := pool.Malloc(i)
		if len(buf) != i || cap(buf) != i {
			t.Fatalf("invalid len or cap: %v, %v, %v ", i, len(buf), cap(buf))
		}
	}
}

func TestTraceDebugerPool(t *testing.T) {
	pool := NewTraceDebuger(New(1, 1))
	// for i := 0; i < 1024*1024; i++ {
	// 	buf := pool.Malloc(i)
	// 	if len(buf) != i {
	// 		t.Fatalf("invalid len: %v != %v", len(buf), i)
	// 	}
	// 	pool.Free(buf)
	// }
	// for i := 1024 * 1024; i < 1024*1024*1024; i += 1024 * 1024 {
	// 	buf := pool.Malloc(i)
	// 	if len(buf) != i {
	// 		t.Fatalf("invalid len: %v != %v", len(buf), i)
	// 	}
	// 	pool.Free(buf)
	// }

	buf := pool.Malloc(1)
	for i := 1; i < 1024; i++ {
		buf = pool.Append(buf[:1], make([]byte, i)...)
		if len(buf) != i+1 {
			t.Fatalf("invalid len: %v != %v", len(buf), i)
		}
	}
	pool.Free(buf)
	// pool.Free(buf)
}

func TestStackFuncs(t *testing.T) {
	stack1, stackPtr := getStackAndPtr()
	stack2 := ptr2StackString(stackPtr)
	if stack1 != stack2 {
		t.Fatalf("stack not equal:\n\n%v\n\n%v", stack1, stack2)
	}

	buf := []byte{1}
	p1 := bytesPointer(buf)
	buf[0] = 2
	p2 := bytesPointer(buf)
	if p1 != p2 {
		t.Fatalf("buf pointer not equal: %v, %v", p1, p2)
	}
}
