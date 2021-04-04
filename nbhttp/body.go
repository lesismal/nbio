// Copyright 2020 lesismal. All rights reserved.
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package nbhttp

import (
	"io"

	"github.com/lesismal/nbio/mempool"
)

// BodyReader .
type BodyReader struct {
	index  int
	buffer []byte
}

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
		if available == need {
			br.Close()
		}
		return need, nil
	}
	copy(p[:available], br.buffer[br.index:])
	br.Close()
	return available, io.EOF
}

// Append .
func (br *BodyReader) Append(b []byte) {
	if len(b) > 0 {
		br.buffer = mempool.Realloc(br.buffer, len(br.buffer)+len(b))
		copy(br.buffer[len(br.buffer)-len(b):], b)
	}
}

// Close implements io. Closer
func (br *BodyReader) Close() error {
	if br.buffer != nil {
		mempool.Free(br.buffer)
		br.buffer = nil
		br.index = 0
	}
	return nil
}

// NewBodyReader creates a BodyReader
func NewBodyReader(buffer []byte, index int) *BodyReader {
	return &BodyReader{
		index:  index,
		buffer: buffer,
	}
}
