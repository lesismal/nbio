// Copyright 2020 lesismal. All rights reserved.
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package nbhttp

import (
	"io"
	"sync"

	"github.com/lesismal/nbio/mempool"
)

var (
	bodyReaderPool = sync.Pool{
		New: func() interface{} {
			return &BodyReader{}
		},
	}
)

// BodyReader .
type BodyReader struct {
	index  int
	buffer []byte
}

var mux = sync.Mutex{}

// Read implements io.Reader
func (br *BodyReader) Read(p []byte) (int, error) {
	need := len(p)
	available := len(br.buffer) - br.index
	if available <= 0 {
		return 0, io.EOF
	}
	if available >= need {
		copy(p, br.buffer[br.index:br.index+need])
		br.index += need
		// if available == need {
		// 	br.Close()
		// }
		return need, nil
	}
	copy(p[:available], br.buffer[br.index:])
	br.index += available
	return available, io.EOF
}

// Append .
func (br *BodyReader) Append(b []byte) {
	if len(b) > 0 {
		br.buffer = mempool.Realloc(br.buffer, len(br.buffer)+len(b))
		copy(br.buffer[len(br.buffer)-len(b):], b)
	}
}

// RawBody returns BodyReader's buffer directly,
// the buffer returned would be released to the mempool after http handler func,
// the application layer should not hold it any longer after the http handler func.
func (br *BodyReader) RawBody() []byte {
	return br.buffer
}

// TakeOver returns BodyReader's buffer,
// the buffer returned would not be released to the mempool after http handler func,
// the application layer could hold it longer and should manage when to release the buffer to the mempool.
func (br *BodyReader) TakeOver() []byte {
	b := br.buffer
	br.buffer = nil
	br.index = 0
	return b
}

// Close implements io. Closer
func (br *BodyReader) Close() error {
	return nil
}

func (br *BodyReader) close() error {
	if br.buffer != nil {
		mempool.Free(br.buffer)
		br.buffer = nil
		br.index = 0
	}
	return nil
}

// NewBodyReader creates a BodyReader
func NewBodyReader(buffer []byte) *BodyReader {
	br := bodyReaderPool.Get().(*BodyReader)
	br.index = 0
	br.buffer = buffer
	return br
}
