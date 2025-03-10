package nbhttp

import (
	"bytes"
	"crypto/rand"
	"io"
	"testing"

	"github.com/lesismal/nbio/mempool"
)

func TestHTTPBodyPool(t *testing.T) {
	br := httpBodyPool.Get().(*HTTPBody)
	buf := make([]byte, 10)
	pbuf := &buf
	br.buffers = append(br.buffers, pbuf)
	*br = emptyHTTPBody
	httpBodyPool.Put(br)

	for i := 0; i < 1000; i++ {
		br2 := httpBodyPool.Get().(*HTTPBody)
		if br2.buffers != nil {
			t.Fatal("len>0")
		}
		buf = make([]byte, 10)
		pbuf = &buf
		br2.buffers = append(br.buffers, pbuf)
		*br2 = emptyHTTPBody
		httpBodyPool.Put(br)
	}
}

func TestHTTPBody(t *testing.T) {
	engine := NewEngine(Config{
		BodyAllocator: mempool.NewAligned(),
	})
	var (
		b0 []byte
		b1 = make([]byte, 2049)
		b2 = make([]byte, 1132)
		b3 = make([]byte, 11111)
	)
	rand.Read(b1)
	rand.Read(b2)
	rand.Read(b3)

	allBytes := append(b0, b1...)
	allBytes = append(allBytes, b2...)
	allBytes = append(allBytes, b3...)

	newBR := func() *HTTPBody {
		br := NewHTTPBody(engine)
		br.append(b1, 1000000)
		br.append(b2, 1000000)
		br.append(b3, 1000000)
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
	br1.Close()

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
	br2.Close()
}
