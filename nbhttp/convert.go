package nbhttp

import (
	"crypto/tls"
	"encoding/binary"
	"fmt"
	"net"
	"unsafe"

	ltls "github.com/lesismal/llib/std/crypto/tls"
	"github.com/lesismal/nbio"
)

const (
	uintptrSize   = int(unsafe.Sizeof(uintptr(0)))
	connValueSize = uintptrSize + 1
)

const (
	connTypNONE byte = 0
	connTypNBIO byte = 1
	connTypTCP  byte = 2
	connTypUNIX byte = 3
	connTypTLS  byte = 4
	connTypLTLS byte = 5
)

// We can use this array-value as map key to reduce gc cost.
// Ref: https://github.com/lesismal/nbio/pull/304#issuecomment-1583880587
type connValue [connValueSize]byte

// Convert net.Conn to array value.
//
//go:norace
func conn2Array(conn net.Conn) (connValue, error) {
	var p uintptr
	var b connValue
	switch vt := conn.(type) {
	case *nbio.Conn:
		p = uintptr(unsafe.Pointer(vt))
		b[uintptrSize] = connTypNBIO
	case *net.TCPConn:
		p = uintptr(unsafe.Pointer(vt))
		b[uintptrSize] = connTypTCP
	case *net.UnixConn:
		p = uintptr(unsafe.Pointer(vt))
		b[uintptrSize] = connTypUNIX
	case *tls.Conn:
		p = uintptr(unsafe.Pointer(vt))
		b[uintptrSize] = connTypTLS
	case *ltls.Conn:
		p = uintptr(unsafe.Pointer(vt))
		b[uintptrSize] = connTypLTLS
	default:
		return b, fmt.Errorf("invalid conn type: %v", vt)
	}
	switch uintptrSize {
	case 4:
		binary.LittleEndian.PutUint32(b[:uintptrSize], uint32(p))
	case 8:
		binary.LittleEndian.PutUint64(b[:uintptrSize], uint64(p))
	default:
		return b, fmt.Errorf("unsupported platform: invalid uintptr size %v", uintptrSize)
	}
	return b, nil
}

// Convert array value to net.Conn.
//
//go:norace
func array2Conn(b connValue) (net.Conn, error) {
	var p uintptr
	switch uintptrSize {
	case 4:
		p = uintptr(binary.LittleEndian.Uint32(b[:uintptrSize]))
	case 8:
		p = uintptr(binary.LittleEndian.Uint64(b[:uintptrSize]))
	default:
		return nil, fmt.Errorf("unsupported platform: invalid uintptr size %v", uintptrSize)
	}

	switch b[uintptrSize] {
	case connTypNBIO:
		conn := *((**nbio.Conn)(unsafe.Pointer(&p)))
		return conn, nil
	case connTypTCP:
		conn := *((**net.TCPConn)(unsafe.Pointer(&p)))
		return conn, nil
	case connTypUNIX:
		conn := *((**net.UnixConn)(unsafe.Pointer(&p)))
		return conn, nil
	case connTypTLS:
		conn := *((**tls.Conn)(unsafe.Pointer(&p)))
		return conn, nil
	case connTypLTLS:
		conn := *((**ltls.Conn)(unsafe.Pointer(&p)))
		return conn, nil
	default:
	}

	return nil, fmt.Errorf("invalid conn type: %v", b[uintptrSize])
}
