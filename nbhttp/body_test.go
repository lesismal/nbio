package nbhttp

import (
	"bytes"
	"crypto/rand"
	"io"
	"testing"
)

func TestBodyReader(t *testing.T) {
	engine := NewEngine(Config{})
	b1 := make([]byte, 1997)
	rand.Read(b1)
	b2 := make([]byte, 4096)
	rand.Read(b2)
	b3 := make([]byte, 11111)
	rand.Read(b3)

	allBytes := append(b1, b2...)
	allBytes = append(allBytes, b3...)

	newBR := func() *BodyReader {
		br := NewBodyReader(engine)
		br.append(b1)
		br.append(b2)
		br.append(b3)
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
}
