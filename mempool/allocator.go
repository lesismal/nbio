package mempool

// DefaultMemPool .
// 使用分级内存池作为默认内存池，提供更好的内存管理
var DefaultMemPool Allocator = NewTieredAllocator(
	[]int{64, 256, 1024, 4096, 16384, 65536, 262144, 1048576}, // 64B到1MB的分级
	1024*1024*1024, // 1GB最大可复用大小
)

type Allocator interface {
	Malloc(size int) *[]byte
	Realloc(buf *[]byte, size int) *[]byte // deprecated.
	Append(buf *[]byte, more ...byte) *[]byte
	AppendString(buf *[]byte, more string) *[]byte
	Free(buf *[]byte)
}

type DebugAllocator interface {
	Allocator
	String() string
	SetDebug(bool)
}

//go:norace
func Malloc(size int) *[]byte {
	return DefaultMemPool.Malloc(size)
}

//go:norace
func Realloc(pbuf *[]byte, size int) *[]byte {
	return DefaultMemPool.Realloc(pbuf, size)
}

//go:norace
func Append(pbuf *[]byte, more ...byte) *[]byte {
	return DefaultMemPool.Append(pbuf, more...)
}

//go:norace
func AppendString(pbuf *[]byte, more string) *[]byte {
	return DefaultMemPool.AppendString(pbuf, more)
}

//go:norace
func Free(pbuf *[]byte) {
	DefaultMemPool.Free(pbuf)
}
