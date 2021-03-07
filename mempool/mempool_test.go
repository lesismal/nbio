package mempool

import (
	"math"
	"math/rand"
	"testing"
	"unsafe"
)

const maxMemSize = 1024 * 16

func TestPoolMalloc(t *testing.T) {
	pool := New(maxMemSize)
	if pool.Malloc(0) != nil {
		t.Fatal(0)
	}
	if len(pool.Malloc(1)) != 1 || cap(pool.Malloc(1)) != 64 {
		t.Fatal(1)
	}
	if len(pool.Malloc(maxMemSize)) != maxMemSize || cap(pool.Malloc(maxMemSize)) != maxMemSize {
		t.Fatal(1)
	}
	if pool.Malloc(maxMemSize+1) != nil {
		t.Fatal(maxMemSize + 1)
	}

	for i := 0; i < int(pool.maxBits(maxMemSize)); i++ {
		low := 1<<i + 1
		high := 1 << (i + 1)
		step := (high - low) / 64
		if step == 0 {
			step = 1
		}
		for j := low; j <= high && j <= maxMemSize; j += step {
			if len(pool.Malloc(j)) != j {
				t.Fatalf("[%v, %v, %v, %v, %v]", i, j, low, high, step)
			}
			if j < 64 {
				if cap(pool.Malloc(j)) != 64 {
					t.Fatalf("[%v, %v, %v, %v, %v]", i, j, low, high, step)
				}
			} else if cap(pool.Malloc(j)) != 1<<(i+1) {
				t.Fatalf("[%v, %v, %v, %v, %v]", i, j, low, high, step)
			}
		}
	}
}

func TestPoolRealloc(t *testing.T) {
	pool := New(maxMemSize)

	for i := 0; i < int(pool.maxBits(maxMemSize)); i++ {
		low := 1 << i
		high := 1 << (i + 1)
		step := (high - low) / 64
		if step == 0 {
			step = 1
		}

		for j := low; j <= high && j <= maxMemSize; j += step {
			if j <= 64 {
				buf := pool.Malloc(low)
				newBuf := pool.Realloc(buf, j)
				if cap(newBuf) != 64 {
					t.Fatalf("[%v, %v, %v, %v, %v]", i, j, low, high, step)
				}
				if unsafe.Pointer(&newBuf[0]) != unsafe.Pointer(&buf[0]) {
					t.Fatalf("[%v, %v, %v, %v, %v]", i, j, low, high, step)
				}
			} else {
				buf := pool.Malloc(high)
				newBuf := pool.Realloc(buf, j)
				if cap(newBuf) != 1<<(i+1) {
					t.Fatalf("[%v, %v, %v, %v, %v]", i, j, low, high, step)
				}
				if unsafe.Pointer(&newBuf[0]) != unsafe.Pointer(&buf[0]) {
					t.Fatalf("[%v, %v, %v, %v, %v]", i, j, low, high, step)
				}
			}
		}
		for j := high; j >= low && j <= maxMemSize; j -= step {
			if j <= 64 {
				buf := pool.Malloc(low)
				newBuf := pool.Realloc(buf, j)
				if cap(newBuf) != 64 {
					t.Fatalf("[%v, %v, %v, %v, %v]", i, j, low, high, step)
				}
				if unsafe.Pointer(&newBuf[0]) != unsafe.Pointer(&buf[0]) {
					t.Fatalf("[%v, %v, %v, %v, %v]", i, j, low, high, step)
				}
			} else {
				buf := pool.Malloc(high)
				newBuf := pool.Realloc(buf, j)
				if cap(newBuf) != 1<<(i+1) {
					t.Fatalf("[%v, %v, %v, %v, %v]", i, j, low, high, step)
				}
				if unsafe.Pointer(&newBuf[0]) != unsafe.Pointer(&buf[0]) {
					t.Fatalf("[%v, %v, %v, %v, %v]", i, j, low, high, step)
				}
			}
		}
	}

	for i := 0; i < int(pool.maxBits(maxMemSize)); i++ {
		low := 1 << i
		high := 1 << (i + 1)
		step := (high - low) / 64
		if step == 0 {
			step = 1
		}

		for j := low + 1; j <= high && j <= maxMemSize; j += step {
			buf := pool.Malloc(low)
			newBuf := pool.Realloc(buf, j)
			if j <= 64 {
				if cap(newBuf) != 64 {
					t.Fatalf("[%v, %v, %v, %v, %v]", i, j, low, high, step)
				}
				if unsafe.Pointer(&newBuf[0]) != unsafe.Pointer(&buf[0]) {
					t.Fatalf("[%v, %v, %v, %v, %v]", i, j, low, high, step)
				}
			} else {
				if cap(newBuf) != 1<<(i+1) {
					t.Fatalf("[%v, %v, %v, %v, %v, %v]", i, j, 1<<(i+1), low, high, step)
				}
				if unsafe.Pointer(&newBuf[0]) == unsafe.Pointer(&buf[0]) {
					t.Fatalf("[%v, %v, %v, %v, %v]", i, j, low, high, step)
				}
			}
		}
	}
}

func TestPoolFree(t *testing.T) {
	pool := New(maxMemSize)
	if err := pool.Free(nil); err == nil {
		t.Fatal("Free nil misbehavior")
	}
	if err := pool.Free(make([]byte, 3, 3)); err == nil {
		t.Fatal("Free elem:3 []bytes misbehavior")
	}
	if err := pool.Free(make([]byte, 4, 4)); err != nil {
		t.Fatal("Free elem:4 []bytes misbehavior")
	}
	if err := pool.Free(make([]byte, 1023, 1024)); err != nil {
		t.Fatal("Free elem:1024 []bytes misbehavior")
	}
	if err := pool.Free(make([]byte, maxMemSize, maxMemSize)); err != nil {
		t.Fatal("Free elem:65536 []bytes misbehavior")
	}
	if err := pool.Free(make([]byte, maxMemSize+1, maxMemSize+1)); err == nil {
		t.Fatal("Free elem:65537 []bytes misbehavior")
	}
}

func TestPoolFreeThenMalloc(t *testing.T) {
	pool := New(maxMemSize)
	data := pool.Malloc(4)
	pool.Free(data)
	newData := pool.Malloc(4)
	if cap(data) != cap(newData) {
		t.Fatal("different cap while pool.Malloc()")
	}
}

func TestRandSeedEqual(t *testing.T) {
	loop := 10000

	rand.Seed(99)
	vmap := make(map[int]int)
	for i := 0; i < loop; i++ {
		vmap[i] = rand.Int()
	}

	rand.Seed(99)
	for i := 0; i < loop; i++ {
		v := rand.Int()
		if vmap[i] != v {
			t.Fatalf("rand not equal [%v: %v != %v]", i, vmap[i], v)
		}
	}
}

func BenchmarkMaxBits(b *testing.B) {
	rand.Seed(99)
	pool := New(maxMemSize)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		pool.maxBits(rand.Intn(maxMemSize))
	}
}

func maxBitsMath(size int) byte {
	idx := byte(math.Ceil(math.Log2(float64(size))))
	if idx < 0 {
		idx = 0
	}
	return idx
}

func BenchmarkMaxBitsMath(b *testing.B) {
	rand.Seed(99)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		maxBitsMath(rand.Intn(maxMemSize))
	}
}

func TestBitsEqual(t *testing.T) {
	pool := New(maxMemSize)
	for i := 0; i < 1024*1024*32; i++ {
		if pool.maxBits(i) != byte(maxBitsMath(i)) {
			t.Fatalf("not equal: %v, [%v != %v]", i, pool.maxBits(i), maxBitsMath(i))
		}
	}
}
