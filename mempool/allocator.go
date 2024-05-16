package mempool

// DefaultMemPool .
var DefaultMemPool = New(1024, 1024*1024*1024)

type Allocator interface {
	Malloc(size int) []byte
	Realloc(buf []byte, size int) []byte // deprecated.
	Append(buf []byte, more ...byte) []byte
	AppendString(buf []byte, more string) []byte
	Free(buf []byte)
}

type DebugAllocator interface {
	Allocator
	String() string
	SetDebug(bool)
}

func Malloc(size int) []byte {
	return DefaultMemPool.Malloc(size)
}

func Realloc(buf []byte, size int) []byte {
	return DefaultMemPool.Realloc(buf, size)
}

func Append(buf []byte, more ...byte) []byte {
	return DefaultMemPool.Append(buf, more...)
}

func AppendString(buf []byte, more string) []byte {
	return DefaultMemPool.AppendString(buf, more)
}

func Free(buf []byte) {
	DefaultMemPool.Free(buf)
}

// func Init(bufSize, freeSize int) {
// 	DefaultMemPool = New(bufSize, freeSize)
// }
