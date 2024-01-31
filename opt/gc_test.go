package test

import (
	"fmt"
	"runtime"
	"sync"
	"testing"
	"time"
	"unsafe"

	"github.com/lesismal/nbio/nbhttp"
	"github.com/lesismal/nbio/nbhttp/websocket"
)

const N int = 500 * 10000 // 500

func timeGC() time.Duration {
	start := time.Now()
	runtime.GC()
	return time.Since(start)
}

type commonFields struct {
	Engine                     *nbhttp.Engine
	KeepaliveTime              time.Duration
	MessageLengthLimit         int
	BlockingModAsyncCloseDelay time.Duration

	enableCompression bool
	compressionLevel  int

	pingMessageHandler  func(c *websocket.Conn, appData string)
	pongMessageHandler  func(c *websocket.Conn, appData string)
	closeMessageHandler func(c *websocket.Conn, code int, text string)

	openHandler      func(*websocket.Conn)
	messageHandler   func(c *websocket.Conn, messageType websocket.MessageType, data []byte)
	dataFrameHandler func(c *websocket.Conn, messageType websocket.MessageType, fin bool, data []byte)
	onClose          func(c *websocket.Conn, err error)
}

type Upgrader struct {
	commonFields
}

type ConnWithPointer struct {
	*commonFields
}

type ConnWithStruct struct {
	commonFields
}

type ConnWithUintptr struct {
	commonFields uintptr
}

func (c *ConnWithUintptr) CommonFields() *commonFields {
	fields := *(**commonFields)(unsafe.Pointer(&c.commonFields))
	return fields
}

func (c *ConnWithUintptr) setCommonFields(fields *commonFields) {
	var key commonFieldsKey
	h := [3]uintptr{uintptr(unsafe.Pointer(fields)), commonFieldsKeySize, commonFieldsKeySize}
	b := *(*[]byte)(unsafe.Pointer(&h))
	copy(key[:], b)
	commonFieldsMux.Lock()
	value, ok := commonFieldsCache[key]
	if !ok {
		// key 包内一直缓存持有
		commonFieldsCache[key] = value
		// value uintptr 指向key
		value = (uintptr)(unsafe.Pointer(&key))
	}
	commonFieldsMux.Unlock()
	// 使用时
	c.commonFields = value
}

const commonFieldsKeySize = unsafe.Sizeof(commonFields{})

type commonFieldsKey [commonFieldsKeySize]byte

var commonFieldsMux sync.Mutex
var commonFieldsCache = map[commonFieldsKey]uintptr{}

func TestPointerCommonFields(t *testing.T) {
	u := &Upgrader{}
	conns := make([]ConnWithPointer, N)
	for i := 0; i < N; i++ {
		conns[i].commonFields = &u.commonFields
	}
	fmt.Printf("With %T, GC took %s\n", conns, timeGC())
}

func TestStructCommonFields(t *testing.T) {
	u := &Upgrader{}
	conns := make([]ConnWithStruct, N)
	for i := 0; i < N; i++ {
		conns[i].commonFields = u.commonFields
	}
	fmt.Printf("With %T, GC took %s\n", conns, timeGC())
}
func TestUintptrCommonFields(t *testing.T) {
	u := &Upgrader{}
	conns := make([]ConnWithUintptr, N)
	for i := 0; i < N; i++ {
		conns[i].setCommonFields(&u.commonFields)
	}
	fmt.Printf("With %T, GC took %s\n", conns, timeGC())
}
