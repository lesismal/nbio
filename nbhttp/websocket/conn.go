// Copyright 2020 lesismal. All rights reserved.
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package websocket

import (
	"bytes"
	"encoding/binary"
	"errors"
	"math/rand"
	"net"
	"sync"

	"github.com/lesismal/nbio/mempool"
	"github.com/lesismal/nbio/nbhttp"
)

const (
	maxControlFramePayloadSize = 125
)

// MessageType .
type MessageType int8

// The message types are defined in RFC 6455, section 11.8.t .
const (
	// FragmentMessage .
	FragmentMessage MessageType = 0 // Must be preceded by Text or Binary message
	// TextMessage .
	TextMessage MessageType = 1
	// BinaryMessage .
	BinaryMessage MessageType = 2
	// CloseMessage .
	CloseMessage MessageType = 8
	// PingMessage .
	PingMessage MessageType = 9
	// PongMessage .
	PongMessage MessageType = 10
)

const (
	maskBit = 1 << 7
)

// Conn .
type Conn struct {
	net.Conn

	mux sync.Mutex

	isClient bool

	onCloseCalled            bool
	remoteCompressionEnabled bool
	enableWriteCompression   bool
	compressionLevel         int

	subprotocol string

	session interface{}

	onClose func(c *Conn, err error)
	Engine  *nbhttp.Engine
}

func validCloseCode(code int) bool {
	switch code {
	case 1000:
		return true //| Normal Closure  | hybi@ietf.org | RFC 6455  |
	case 1001:
		return true //      | Going Away      | hybi@ietf.org | RFC 6455  |
	case 1002:
		return true //   | Protocol error  | hybi@ietf.org | RFC 6455  |
	case 1003:
		return true //     | Unsupported Data| hybi@ietf.org | RFC 6455  |
	case 1004:
		return false //     | ---Reserved---- | hybi@ietf.org | RFC 6455  |
	case 1005:
		return false //      | No Status Rcvd  | hybi@ietf.org | RFC 6455  |
	case 1006:
		return false //      | Abnormal Closure| hybi@ietf.org | RFC 6455  |
	case 1007:
		return true //      | Invalid frame   | hybi@ietf.org | RFC 6455  |
		//      |            | payload data    |               |           |
	case 1008:
		return true //     | Policy Violation| hybi@ietf.org | RFC 6455  |
	case 1009:
		return true //       | Message Too Big | hybi@ietf.org | RFC 6455  |
	case 1010:
		return true //       | Mandatory Ext.  | hybi@ietf.org | RFC 6455  |
	case 1011:
		return true //       | Internal Server | hybi@ietf.org | RFC 6455  |
		//     |            | Error           |               |           |
	case 1015:
		return true //  | TLS handshake   | hybi@ietf.org | RFC 6455
	default:
	}
	// IANA registration policy and should be granted in the range 3000-3999.
	// The range of status codes from 4000-4999 is designated for Private
	if code >= 3000 && code < 5000 {
		return true
	}
	return false
}

// OnClose .
func (c *Conn) OnClose(h func(*Conn, error)) {
	if h != nil {
		c.onClose = func(c *Conn, err error) {
			c.mux.Lock()
			defer c.mux.Unlock()
			if !c.onCloseCalled {
				c.onCloseCalled = true
				h(c, err)
			}
		}

		// now all the upgrade, frames/messages and close are called in order
		// nbc, ok := c.Conn.(*nbio.Conn)
		// if ok {
		// 	nbc.Lock()
		// 	defer nbc.Unlock()
		// 	closed, err := nbc.IsClosed()
		// 	if closed {
		// 		c.onClose(c, err)
		// 	}
		// }
	}
}

// WriteMessage .
func (c *Conn) WriteMessage(messageType MessageType, data []byte) error {
	c.mux.Lock()
	defer c.mux.Unlock()

	switch messageType {
	case TextMessage:
	case BinaryMessage:
	case PingMessage, PongMessage, CloseMessage:
		if len(data) > maxControlFramePayloadSize {
			return ErrInvalidControlFrame
		}
	case FragmentMessage:
	default:
	}

	compress := c.enableWriteCompression && (messageType == TextMessage || messageType == BinaryMessage)
	if compress {
		compress = true
		w := &writeBuffer{
			Buffer: bytes.NewBuffer(mempool.Malloc(len(data))),
		}
		defer w.Close()
		w.Reset()
		cw := compressWriter(w, c.compressionLevel)
		_, err := cw.Write(data)
		if err != nil {
			compress = false
		} else {
			cw.Close()
			data = w.Bytes()
		}
	}

	if len(data) > 0 {
		sendOpcode := true
		for len(data) > 0 {
			n := len(data)
			if n > c.Engine.MaxWebsocketFramePayloadSize {
				n = c.Engine.MaxWebsocketFramePayloadSize
			}
			err := c.writeFrame(messageType, sendOpcode, n == len(data), data[:n], compress)
			if err != nil {
				return err
			}
			sendOpcode = false
			data = data[n:]
		}
		return nil
	}

	return c.writeFrame(messageType, true, true, []byte{}, compress)
}

// Session returns user session.
func (c *Conn) Session() interface{} {
	return c.session
}

// SetSession sets user session.
func (c *Conn) SetSession(session interface{}) {
	c.session = session
}

type writeBuffer struct {
	*bytes.Buffer
}

// Close .
func (w *writeBuffer) Close() error {
	mempool.Free(w.Bytes())
	return nil
}

// WriteFrame .
func (c *Conn) WriteFrame(messageType MessageType, sendOpcode, fin bool, data []byte) error {
	return c.writeFrame(messageType, sendOpcode, fin, data, false)
}

func (c *Conn) writeFrame(messageType MessageType, sendOpcode, fin bool, data []byte, compress bool) error {
	var (
		buf     []byte
		byte1   byte
		maskLen int
		headLen int
		bodyLen = len(data)
	)

	if c.isClient {
		byte1 |= maskBit
		maskLen = 4
	}

	if bodyLen < 126 {
		headLen = 2 + maskLen
		buf = mempool.Malloc(len(data) + headLen)
		buf[0] = 0
		buf[1] = (byte1 | byte(bodyLen))
	} else if bodyLen <= 65535 {
		headLen = 4 + maskLen
		buf = mempool.Malloc(len(data) + headLen)
		buf[0] = 0
		buf[1] = (byte1 | 126)
		binary.BigEndian.PutUint16(buf[2:4], uint16(bodyLen))
	} else {
		headLen = 10 + maskLen
		buf = mempool.Malloc(len(data) + headLen)
		buf[0] = 0
		buf[1] = (byte1 | 127)
		binary.BigEndian.PutUint64(buf[2:10], uint64(bodyLen))
	}

	if c.isClient {
		u32 := rand.Uint32()
		maskKey := []byte{byte(u32), byte(u32 >> 8), byte(u32 >> 16), byte(u32 >> 24)}
		copy(buf[headLen-4:headLen], maskKey)
		for i := 0; i < len(data); i++ {
			buf[headLen+i] = (data[i] ^ maskKey[i%4])
		}
	} else {
		copy(buf[headLen:], data)
	}

	// opcode
	if sendOpcode {
		buf[0] = byte(messageType)
	} else {
		buf[0] = 0
	}

	if compress {
		buf[0] |= 0x40
	}

	// fin
	if fin {
		buf[0] |= byte(0x80)
	}

	_, err := c.Conn.Write(buf)
	return err
}

// Write overwrites nbio.Conn.Write.
func (c *Conn) Write(data []byte) (int, error) {
	return -1, ErrInvalidWriteCalling
}

// EnableWriteCompression .
func (c *Conn) EnableWriteCompression(enable bool) {
	if enable {
		if c.remoteCompressionEnabled {
			c.enableWriteCompression = enable
		}
	} else {
		c.enableWriteCompression = enable
	}
}

// SetCompressionLevel .
func (c *Conn) SetCompressionLevel(level int) error {
	if !isValidCompressionLevel(level) {
		return errors.New("websocket: invalid compression level")
	}
	c.compressionLevel = level
	return nil
}

func newConn(u *Upgrader, c net.Conn, subprotocol string, remoteCompressionEnabled bool) *Conn {
	conn := &Conn{
		Conn:                     c,
		subprotocol:              subprotocol,
		remoteCompressionEnabled: remoteCompressionEnabled,
		compressionLevel:         defaultCompressionLevel,
		onClose:                  func(*Conn, error) {},
	}
	conn.EnableWriteCompression(u.enableWriteCompression)
	conn.SetCompressionLevel(u.compressionLevel)

	return conn
}
