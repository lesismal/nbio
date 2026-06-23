package nbhttp

import (
	"bytes"
	"crypto/rand"
	"io"
	"testing"

	"github.com/lesismal/nbio/mempool"
)

func TestBodyReaderPool(t *testing.T) {
	br := bodyReaderPool.Get().(*BodyReader)
	buf := make([]byte, 10)
	pbuf := &buf
	br.buffers = append(br.buffers, pbuf)
	*br = emptyBodyReader
	bodyReaderPool.Put(br)

	for i := 0; i < 1000; i++ {
		br2 := bodyReaderPool.Get().(*BodyReader)
		if br2.buffers != nil {
			t.Fatal("len>0")
		}
		buf = make([]byte, 10)
		pbuf = &buf
		br2.buffers = append(br2.buffers, pbuf)
		*br2 = emptyBodyReader
		bodyReaderPool.Put(br2)
	}
}

func TestBodyReader(t *testing.T) {
	engine := NewEngine(Config{
		BodyAllocator: mempool.NewAligned(),
	})
	var (
		b0 []byte
		b1 = make([]byte, 2049)
		b2 = make([]byte, 1132)
		b3 = make([]byte, 11111)
	)
	_, _ = rand.Read(b1)
	_, _ = rand.Read(b2)
	_, _ = rand.Read(b3)

	allBytes := append(b0, b1...)
	allBytes = append(allBytes, b2...)
	allBytes = append(allBytes, b3...)

	newBR := func() *BodyReader {
		br := NewBodyReader(engine)
		_ = br.append(b1)
		_ = br.append(b2)
		_ = br.append(b3)
		return br
	}

	br1 := newBR()
	body1, err := io.ReadAll(br1)
	if err != nil {
		t.Fatalf("io.ReadAll(br1) failed: %v", err)
	}
	if !bytes.Equal(allBytes, body1) {
		t.Fatalf("!bytes.Equal(allBytes, body1)")
	}
	_ = br1.Close()

	br2 := newBR()
	body2 := make([]byte, len(allBytes))
	for i := range body2 {
		_, err := br2.Read(body2[i : i+1])
		if err != nil {
			t.Fatalf("br2.Readbody2[%d:%d] failed: %v", i, i+1, err)
		}
	}
	if !bytes.Equal(allBytes, body2) {
		t.Fatalf("!bytes.Equal(allBytes, body2)")
	}
	_ = br2.Close()
}

func TestBodyReaderReadAfterClose(t *testing.T) {
	engine := NewEngine(Config{
		BodyAllocator: mempool.NewAligned(),
	})
	br := NewBodyReader(engine)
	data := []byte("hello")
	if err := br.append(data); err != nil {
		t.Fatalf("append failed: %v", err)
	}
	if err := br.Close(); err != nil {
		t.Fatalf("close failed: %v", err)
	}
	buf := make([]byte, 8)
	n, err := br.Read(buf)
	if n != 0 || err != io.EOF {
		t.Fatalf("Read after Close = (%d, %v), want (0, EOF)", n, err)
	}
}

func TestBodyReaderLeftWithoutBuffers(t *testing.T) {
	engine := NewEngine(Config{
		BodyAllocator: mempool.NewAligned(),
	})
	br := NewBodyReader(engine)
	br.left = 10
	br.buffers = nil
	buf := make([]byte, 8)
	n, err := br.Read(buf)
	if n != 0 || err != io.EOF {
		t.Fatalf("Read with left>0 and no buffers = (%d, %v), want (0, EOF)", n, err)
	}
	if br.left != 0 {
		t.Fatalf("left = %d, want 0", br.left)
	}
}

func TestBodyReaderPooledReuse(t *testing.T) {
	engine := NewEngine(Config{
		BodyAllocator: mempool.NewAligned(),
	})
	br := NewBodyReader(engine)
	if err := br.append([]byte("x")); err != nil {
		t.Fatalf("append failed: %v", err)
	}
	_ = br.Close()
	*br = emptyBodyReader
	bodyReaderPool.Put(br)

	br2 := NewBodyReader(engine)
	if br2.left != 0 || br2.index != 0 || br2.buffers != nil || br2.closed {
		t.Fatalf("pooled BodyReader not reset: left=%d index=%d buffers=%v closed=%v",
			br2.left, br2.index, br2.buffers, br2.closed)
	}
}
