// Copyright 2020 lesismal. All rights reserved.
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package nbhttp

import (
	"io"
	"sync"
)

var (
	emptyBodyReader = BodyReader{}
	bodyReaderPool  = sync.Pool{
		New: func() interface{} {
			return &BodyReader{}
		},
	}
)

// BodyReader implements io.ReadCloser and is to be used as HTTP body.
type BodyReader struct {
	index   int       // first buffer read index
	left    int       // num of byte left
	buffers []*[]byte // buffers that storage HTTP body
	engine  *Engine   // allocator that manages buffers
	closed  bool
}

// Read reads body bytes to p, returns the num of bytes read and error.
//
//go:norace
func (br *BodyReader) Read(p []byte) (int, error) {
	need := len(p)
	if br.left <= 0 {
		return 0, io.EOF
	}
	ncopy := 0
	for ncopy < need && br.left > 0 {
		pbuf := br.buffers[0]
		nc := copy(p[ncopy:], (*pbuf)[br.index:])
		if nc+br.index >= len(*pbuf) {
			br.engine.BodyAllocator.Free(pbuf)
			br.buffers[0] = nil
			br.buffers = br.buffers[1:]
			br.index = 0
		} else {
			br.index += nc
		}
		ncopy += nc
		br.left -= nc
	}
	return ncopy, nil
}

// Close frees buffers and resets itself to empty value.
//
//go:norace
func (br *BodyReader) Close() error {
	if br.closed {
		return nil
	}
	br.closed = true
	if br.buffers != nil {
		for _, b := range br.buffers {
			br.engine.BodyAllocator.Free(b)
		}
	}
	// *br = emptyBodyReader
	// bodyReaderPool.Put(br)
	return nil
}

// Index returns current head buffer's reading index.
//
//go:norace
func (br *BodyReader) Index() int {
	return br.index
}

// Left returns how many bytes are left for reading.
//
//go:norace
func (br *BodyReader) Left() int {
	return br.left
}

// Buffers returns the underlayer buffers that store the HTTP Body.
//
//go:norace
func (br *BodyReader) Buffers() []*[]byte {
	return br.buffers
}

// RawBodyBuffers returns a reference of BodyReader's current buffers.
// The buffers returned will be closed(released automatically when closed)
// HTTP Handler is called, users should not free the buffers and should
// not hold it any longer after the HTTP Handler is called.
//
//go:norace
func (br *BodyReader) RawBodyBuffers() [][]byte {
	buffers := make([][]byte, len(br.buffers))
	for i, pbuf := range br.buffers {
		if i == 0 {
			buffers[i] = (*pbuf)[br.index:]
		} else {
			buffers[i] = *pbuf
		}
	}
	return buffers
}

// Engine returns Engine that creates this HTTP Body.
//
//go:norace
func (br *BodyReader) Engine() *Engine {
	return br.engine
}

// append appends data to buffers.
//
//go:norace
func (br *BodyReader) append(data []byte) error {
	if len(data) == 0 {
		return nil
	}

	if br.engine.MaxHTTPBodySize > 0 && len(data)+br.left > br.engine.MaxHTTPBodySize {
		return ErrTooLong
	}

	br.left += (len(data))
	if len(br.buffers) == 0 {
		pbuf := br.engine.BodyAllocator.Malloc(len(data))
		copy(*pbuf, data)
		br.buffers = append(br.buffers, pbuf)
	} else {
		i := len(br.buffers) - 1
		pbuf := br.buffers[i]
		l := len(*pbuf)
		bLeft := cap(*pbuf) - len(*pbuf)
		if bLeft > 0 {
			if bLeft > len(data) {
				*pbuf = (*pbuf)[:l+len(data)]
			} else {
				*pbuf = (*pbuf)[:cap(*pbuf)]
			}
			nc := copy((*pbuf)[l:], data)
			data = data[nc:]
			br.buffers[i] = pbuf
		}
		if len(data) > 0 {
			pbuf = br.engine.BodyAllocator.Malloc(len(data))
			copy(*pbuf, data)
			br.buffers = append(br.buffers, pbuf)
		}
	}
	return nil
}

// NewBodyReader creates a BodyReader.
//
//go:norace
func NewBodyReader(engine *Engine) *BodyReader {
	br := bodyReaderPool.Get().(*BodyReader)
	br.engine = engine
	return br
}
