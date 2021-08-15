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
