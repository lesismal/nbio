package mempool

// stdAllocator .
type stdAllocator struct {
	*debugger
}

// Malloc .
//
//go:norace
func (a *stdAllocator) Malloc(size int) *[]byte {
	ret := make([]byte, size)
	a.incrMalloc(&ret)
	return &ret
}

// Realloc .
//
//go:norace
func (a *stdAllocator) Realloc(pbuf *[]byte, size int) *[]byte {
	if size <= cap(*pbuf) {
		*pbuf = (*pbuf)[:size]
		return pbuf
	}
	newBuf := make([]byte, size)
	copy(newBuf, *pbuf)
	return &newBuf
}

// Free .
//
//go:norace
func (a *stdAllocator) Free(pbuf *[]byte) {
	a.incrFree(pbuf)
}

//go:norace
func (a *stdAllocator) Append(pbuf *[]byte, more ...byte) *[]byte {
	*pbuf = append(*pbuf, more...)
	return pbuf
}

//go:norace
func (a *stdAllocator) AppendString(pbuf *[]byte, more string) *[]byte {
	*pbuf = append(*pbuf, more...)
	return pbuf
}

//go:norace
func NewSTD() Allocator {
	return &stdAllocator{
		debugger: &debugger{},
	}
}
