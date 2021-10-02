package mempool

import (
	"testing"
)

func TestMemPool(t *testing.T) {
	const minMemSize = 64
	pool := New(minMemSize)
	for i := 0; i < 1024*1024; i++ {
		buf := pool.Malloc(i)
		if len(buf) != i {
			t.Fatalf("invalid length: %v != %v", len(buf), i)
		}
		pool.Free(buf)
	}
	for i := 1024 * 1024; i < 1024*1024*1024; i += 1024 * 1024 {
		buf := pool.Malloc(i)
		if len(buf) != i {
			t.Fatalf("invalid length: %v != %v", len(buf), i)
		}
		pool.Free(buf)
	}

	buf := pool.Malloc(0)
	for i := 1; i < 1024*1024; i++ {
		buf = pool.Realloc(buf, i)
		if len(buf) != i {
			t.Fatalf("invalid length: %v != %v", len(buf), i)
		}
	}
	pool.Free(buf)
}

func TestApend(t *testing.T) {
	pool := New(5)
	buf := pool.Malloc(11)
	buf = buf[:5]
	copy(buf, []byte("hello"))
	buf = pool.Realloc(buf, 11)
	if len(buf) != 11 {
		t.Fatalf("re-alloc didn't increase length to 11, instead %d", len(buf))
	}
	copy(buf[5:], []byte(" world"))
	if string(buf) != "hello world" {
		t.Fatalf("re-alloc append did now work '%s' != 'hello world'", string(buf))
	}
}
