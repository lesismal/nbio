package mempool

import (
	"testing"
)

func TestMemPool(t *testing.T) {
	pool := New(1024*1024*1024, 1024*1024*1024)
	for i := 0; i < 1024*1024; i++ {
		pbuf := pool.Malloc(i)
		if len(*pbuf) != i {
			t.Fatalf("invalid len: %v != %v", len(*pbuf), i)
		}
		pool.Free(pbuf)
	}
	for i := 1024 * 1024; i < 1024*1024*1024; i += 1024 * 1024 {
		pbuf := pool.Malloc(i)
		if len(*pbuf) != i {
			t.Fatalf("invalid len: %v != %v", len(*pbuf), i)
		}
		pool.Free(pbuf)
	}

	pbuf := pool.Malloc(0)
	for i := 1; i < 1024*1024; i++ {
		pbuf = pool.Realloc(pbuf, i)
		if len(*pbuf) != i {
			t.Fatalf("invalid len: %v != %v", len(*pbuf), i)
		}
	}
	pool.Free(pbuf)
}

func TestAlignedMemPool(t *testing.T) {
	pool := NewAligned()
	b := pool.Malloc(32769)
	pool.Free(b)
	tmpBuf := make([]byte, 60001)
	pool.Free(&tmpBuf)
	for i := 0; i < 1024*64+1024; i += 1 {
		pbuf := pool.Malloc(i)
		if len(*pbuf) != i {
			t.Fatalf("invalid length: %v != %v", len(*pbuf), i)
		}
		pool.Free(pbuf)
	}
	for i := minAlignedBufferSizeBits; i < maxAlignedBufferSizeBits; i++ {
		size := 1 << i
		pbuf := pool.Malloc(size)
		if len(*pbuf) != size || cap(*pbuf) > size*2 {
			t.Fatalf("invalid len or cap: %v, %v %v, %v ", i, len(*pbuf), cap(*pbuf), size)
		}
		pbuf = pool.Malloc(size + 1)
		if i != maxAlignedBufferSizeBits {
			if len(*pbuf) != size+1 || cap(*pbuf) != size*2 || cap(*pbuf) > (size+1)*2 {
				t.Fatalf("invalid len or cap: %v, %v %v, %v ", i, len(*pbuf), cap(*pbuf), size)
			}
		} else {
			if len(*pbuf) != size+1 || cap(*pbuf) != size+1 {
				t.Fatalf("invalid len or cap: %v, %v %v, %v ", i, len(*pbuf), cap(*pbuf), size)
			}
		}
		pool.Free(pbuf)
	}
	for i := -10; i < 0; i++ {
		pbuf := pool.Malloc(i)
		if pbuf != nil {
			t.Fatalf("invalid malloc, should be nil but got: %v, %v", len(*pbuf), cap(*pbuf))
		}
	}
	for i := 1 << maxAlignedBufferSizeBits; i < 1<<maxAlignedBufferSizeBits+1024; i++ {
		pbuf := pool.Malloc(i)
		if len(*pbuf) != i || cap(*pbuf) != i {
			t.Fatalf("invalid len or cap: %v, %v, %v ", i, len(*pbuf), cap(*pbuf))
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

	pbuf := pool.Malloc(1)
	for i := 1; i < 1024; i++ {
		*pbuf = (*pbuf)[:1]
		pbuf = pool.Append(pbuf, make([]byte, i)...)
		if len(*pbuf) != i+1 {
			t.Fatalf("invalid len: %v != %v", len(*pbuf), i)
		}
	}
	pool.Free(pbuf)
	// pool.Free(buf)
}

func TestStackFuncs(t *testing.T) {
	stack1, stackPtr := getStackAndPtr()
	stack2 := ptr2StackString(stackPtr)
	if stack1 != stack2 {
		t.Fatalf("stack not equal:\n\n%v\n\n%v", stack1, stack2)
	}

	buf := []byte{1}
	pbuf := &buf
	p1 := bytesPointer(pbuf)
	buf[0] = 2
	p2 := bytesPointer(pbuf)
	if p1 != p2 {
		t.Fatalf("buf pointer not equal: %v, %v", p1, p2)
	}
}
