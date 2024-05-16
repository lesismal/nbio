package mempool

import (
	"bytes"
	"fmt"
	"os"
	"runtime"
	"sync"
	"unsafe"
)

var (
	stackMux    = sync.Mutex{}
	stackBuf    = [1024 * 64]byte{}
	stackWriter = bytes.NewBuffer(stackBuf[:0])
	stackMap    = map[string][2]uintptr{}
	nilStackPtr = [2]uintptr{0, 0}
)

type TraceDebugger struct {
	mux       sync.Mutex
	pAlloced  map[uintptr][2]uintptr
	allocator Allocator
}

func NewTraceDebuger(allocator Allocator) *TraceDebugger {
	return &TraceDebugger{
		allocator: allocator,
		pAlloced:  map[uintptr][2]uintptr{},
	}
}

// Malloc .
func (td *TraceDebugger) Malloc(size int) []byte {
	td.mux.Lock()
	defer td.mux.Unlock()

	buf := td.allocator.Malloc(size)
	ptr := bytesPointer(buf)
	if stackPtr, ok := td.pAlloced[ptr]; ok {
		printStack(fmt.Sprintf("malloc got a buf which has been malloced by otherwhere: %v", ptr), stackPtr)
	}
	td.setBufferPointer(ptr)
	return buf
}

// deprecated.
func (td *TraceDebugger) Realloc(buf []byte, size int) []byte {
	newBuf := td.allocator.Realloc(buf, size)
	return newBuf
}

// Append .
func (td *TraceDebugger) Append(buf []byte, more ...byte) []byte {
	td.mux.Lock()
	defer td.mux.Unlock()

	pold := bytesPointer(buf)
	if _, ok := td.pAlloced[pold]; !ok {
		printStack("Append to a buf which has not been malloced", nilStackPtr)
	}
	newBuf := td.allocator.Append(buf, more...)
	pnew := bytesPointer(newBuf)
	if pnew != pold {
		if preStack, ok := td.pAlloced[pnew]; ok {
			printStack(fmt.Sprintf("Append got another new buf which has been malloced by otherwhere: %v", pnew), preStack)
		}
		td.deleteBufferPointer(pold)
		td.setBufferPointer(pnew)
	}
	return newBuf
}

// AppendString .
func (td *TraceDebugger) AppendString(buf []byte, more string) []byte {
	td.mux.Lock()
	defer td.mux.Unlock()

	pold := bytesPointer(buf)
	if _, ok := td.pAlloced[pold]; !ok {
		printStack("AppendString to a buf which has not been malloced", nilStackPtr)
	}
	newBuf := td.allocator.AppendString(buf, more)
	pnew := bytesPointer(newBuf)
	if pnew != pold {
		if preStack, ok := td.pAlloced[pnew]; ok {
			printStack("AppendString got another new buf which has been malloced by otherwhere", preStack)
		}
		td.deleteBufferPointer(pold)
		td.setBufferPointer(pnew)
	}
	return newBuf
}

// Free .
func (td *TraceDebugger) Free(buf []byte) {
	td.mux.Lock()
	defer td.mux.Unlock()

	if cap(buf) == 0 {
		printStack("Free invalid buf with cap 0", nilStackPtr)
		return
	}
	ptr := bytesPointer(buf)
	_, ok := td.pAlloced[ptr]
	if !ok {
		printStack("Free a buf which is not malloced by allocator", nilStackPtr)
	}
	td.deleteBufferPointer(ptr)
	td.allocator.Free(buf)
}

func (td *TraceDebugger) setBufferPointer(ptr uintptr) {
	_, stackPtr := getStackAndPtr()
	td.pAlloced[ptr] = stackPtr
}

func (td *TraceDebugger) deleteBufferPointer(ptr uintptr) {
	delete(td.pAlloced, ptr)
}

// func getStack() string {
// 	stackMux.Lock()
// 	defer stackMux.Unlock()
// 	buf := stackBuf[:runtime.Stack(stackBuf, false)]
// 	return string(buf)
// }

func getStackAndPtr() (string, [2]uintptr) {
	stackMux.Lock()
	defer stackMux.Unlock()

	nwrite := 0
	stackWriter.Reset()
	for i := 2; i < 20; i++ {
		pc, file, line, ok := runtime.Caller(i)

		if !ok || i > 50 {
			break
		}
		n, err := fmt.Fprintf(stackWriter, "\t%d [file: %s] [func: %s] [line: %d]\n", i-1, file, runtime.FuncForPC(pc).Name(), line)
		if n > 0 {
			nwrite += n
		}
		if err != nil {
			break
		}
	}

	buf := stackBuf[:nwrite]
	stack := *(*string)(unsafe.Pointer(&buf))
	if ptr, ok := stackMap[stack]; ok {
		return ptr2StackString(ptr), ptr
	}
	stack = string(buf)
	ptr := *(*[2]uintptr)(unsafe.Pointer(&stack))
	ptrCopy := [2]uintptr{ptr[0], ptr[1]}
	stackMap[stack] = ptrCopy
	return stack, ptrCopy
}

func ptr2StackString(ptr [2]uintptr) string {
	if ptr[0] == 0 && ptr[1] == 0 {
		return "nil"
	}
	return *((*string)(unsafe.Pointer(&ptr)))
}

// func bytesToStr(b []byte) string {
// 	return *(*string)(unsafe.Pointer(&b))
// }

// func strToBytes(s string) []byte {
// 	x := (*[2]uintptr)(unsafe.Pointer(&s))
// 	h := [3]uintptr{x[0], x[1], x[1]}
// 	return *(*[]byte)(unsafe.Pointer(&h))
// }

func printStack(info string, preStackPtr [2]uintptr) {
	var (
		currStack, _ = getStackAndPtr()
		preStack     = ptr2StackString(preStackPtr)
	)
	fmt.Printf(`
-------------------------------------------
[mempool trace] %v ->

previous stack: 
%v

-------------------------------------------

current stack :
%v
-------------------------------------------

`, info, preStack, currStack)
	os.Exit(-1)
}

func bytesPointer(buf []byte) uintptr {
	return (uintptr)(unsafe.Pointer(&(buf[:1][0])))
}

// func stringPointer(s *string) uintptr {
// 	ptr := (*uintptr)(unsafe.Pointer(s))
// 	return (uintptr)(unsafe.Pointer(&ptr))
// }
