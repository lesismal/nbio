package mempool

// stdAllocator .
type stdAllocator struct {
	*debugger
}

// Malloc .
//
//go:norace
func (a *stdAllocator) Malloc(size int) []byte {
	ret := make([]byte, size)
	a.incrMalloc(ret)
	return ret
}

// Realloc .
//
//go:norace
func (a *stdAllocator) Realloc(buf []byte, size int) []byte {
	if size <= cap(buf) {
		return buf[:size]
	}
	newBuf := make([]byte, size)
	copy(newBuf, buf)
	return newBuf
}

// Free .
//
//go:norace
func (a *stdAllocator) Free(buf []byte) {
	a.incrFree(buf)
}

//go:norace
func (a *stdAllocator) Append(buf []byte, more ...byte) []byte {
	return append(buf, more...)
}

//go:norace
func (a *stdAllocator) AppendString(buf []byte, more string) []byte {
	return append(buf, more...)
}

//go:norace
func NewSTD() Allocator {
	return &stdAllocator{
		debugger: &debugger{},
	}
}
