package websocket

import (
	"encoding/binary"
	"net"
	"sync"

	"github.com/lesismal/nbio/mempool"
	"github.com/lesismal/nbio/nbhttp"
)

const (
	maxFrameHeaderSize         = 14
	maxControlFramePayloadSize = 125
	framePayloadSize           = 65535
)

type MessageType int8

// The message types are defined in RFC 6455, section 11.8.t
const (
	FragmentMessage MessageType = 0 // Must be preceded by Text or Binary message
	TextMessage     MessageType = 1
	BinaryMessage   MessageType = 2
	CloseMessage    MessageType = 8
	PingMessage     MessageType = 9
	PongMessage     MessageType = 10
)

type Conn struct {
	net.Conn

	index int

	mux sync.Mutex

	subprotocol string

	pingHandler    func(c *Conn, appData string)
	pongHandler    func(c *Conn, appData string)
	messageHandler func(c *Conn, messageType MessageType, data []byte)
	closeHandler   func(c *Conn, code int, text string)

	onClose func(c *Conn, err error)
	Server  *nbhttp.Server
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

func (c *Conn) handleMessage(opcode MessageType, data []byte) {
	switch opcode {
	case TextMessage, BinaryMessage:
		c.messageHandler(c, opcode, data)
	case CloseMessage:
		if len(data) >= 2 {
			code := int(binary.BigEndian.Uint16(data[:2]))
			if !validCloseCode(code) || !c.Server.CheckUtf8(data[2:]) {
				protoErrorCode := make([]byte, 2)
				binary.BigEndian.PutUint16(protoErrorCode, 1002)
				c.WriteMessage(CloseMessage, protoErrorCode)
			} else {
				c.closeHandler(c, code, string(data[2:]))
			}
		} else {
			c.WriteMessage(CloseMessage, nil)
		}
		// close immediately, no need to wait for data flushed on a blocked conn
		c.Close()
	case PingMessage:
		c.pingHandler(c, string(data))
	case PongMessage:
		c.pongHandler(c, string(data))
	default:
		c.Close()
	}
}

func (c *Conn) SetCloseHandler(h func(*Conn, int, string)) {
	if h != nil {
		c.closeHandler = h
	}
}

func (c *Conn) SetPingHandler(h func(*Conn, string)) {
	if h != nil {
		c.pingHandler = h
	}
}

func (c *Conn) SetPongHandler(h func(*Conn, string)) {
	if h != nil {
		c.pongHandler = h
	}
}

func (c *Conn) OnMessage(h func(*Conn, MessageType, []byte)) {
	if h != nil {
		c.messageHandler = func(c *Conn, messageType MessageType, data []byte) {
			c.Server.MessageHandlerExecutor(c.index, func() {
				h(c, messageType, data)
				// mempool.Free(data)
			})
		}
	}
}

func (c *Conn) OnClose(h func(*Conn, error)) {
	if h != nil {
		c.onClose = h
	}
}

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
	default:
	}

	if len(data) == 0 {
		return c.writeMessage(messageType, true, true, []byte{})
	} else {
		sendOpcode := true
		for len(data) > 0 {
			n := len(data)
			if n > framePayloadSize {
				n = framePayloadSize
			}
			err := c.writeMessage(messageType, sendOpcode, n == len(data), data[:n])
			if err != nil {
				return err
			}
			sendOpcode = false
			data = data[n:]
		}
	}

	return nil
}

func (c *Conn) writeMessage(messageType MessageType, sendOpcode, fin bool, data []byte) error {
	var (
		buf     []byte
		bodyLen = len(data)
		offset  = 2
	)
	if bodyLen < 126 {
		buf = mempool.Malloc(len(data) + 2)
		buf[1] = byte(bodyLen)
	} else if bodyLen <= 65535 {
		buf = mempool.Malloc(len(data) + 4)
		buf[1] = 126
		binary.BigEndian.PutUint16(buf[2:4], uint16(bodyLen))
		offset = 4
	} else {
		buf = mempool.Malloc(len(data) + 10)
		buf[1] = 127
		binary.BigEndian.PutUint64(buf[2:10], uint64(bodyLen))
		offset = 10
	}
	copy(buf[offset:], data)

	// opcode
	if sendOpcode {
		buf[0] = byte(messageType)
	} else {
		buf[0] = 0
	}

	// fin
	if fin {
		buf[0] |= byte(0x80)
	}

	_, err := c.Conn.Write(buf)
	return err
}

// overwrite nbio.Conn.Write
func (c *Conn) Write(data []byte) (int, error) {
	return -1, ErrInvalidWriteCalling
}

func newConn(c net.Conn, index int, compress bool, subprotocol string) *Conn {
	conn := &Conn{
		Conn:           c,
		index:          index,
		subprotocol:    subprotocol,
		pongHandler:    func(*Conn, string) {},
		messageHandler: func(*Conn, MessageType, []byte) {},
		onClose:        func(*Conn, error) {},
	}
	conn.pingHandler = func(c *Conn, data string) {
		if len(data) > 125 {
			conn.Close()
			return
		}
		c.WriteMessage(PongMessage, []byte(data))
	}
	conn.closeHandler = func(c *Conn, code int, text string) {
		if len(text)+2 > maxControlFramePayloadSize {
			return //ErrInvalidControlFrame
		}
		buf := mempool.Malloc(len(text) + 2)
		binary.BigEndian.PutUint16(buf[:2], uint16(code))
		copy(buf[2:], text)
		conn.WriteMessage(CloseMessage, buf)
		mempool.Free(buf)
	}
	return conn
}
