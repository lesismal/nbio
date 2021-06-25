package websocket

import (
	"encoding/binary"
	"net"
	"sync"

	"github.com/lesismal/nbio/nbhttp"
)

const (
	maxFrameHeaderSize         = 14
	maxControlFramePayloadSize = 125
	framePayloadSize           = 4096 - maxFrameHeaderSize
)

// The message types are defined in RFC 6455, section 11.8.
const (
	TextMessage   int8 = 1
	BinaryMessage int8 = 2
	CloseMessage  int8 = 8
	PingMessage   int8 = 9
	PongMessage   int8 = 10
)

type Conn struct {
	net.Conn

	index int

	mux sync.Mutex

	subprotocol string

	pingHandler    func(c *Conn, appData string)
	pongHandler    func(c *Conn, appData string)
	messageHandler func(c *Conn, messageType int8, data []byte)
	closeHandler   func(c *Conn, code int, text string)

	onClose func(c *Conn, err error)
	Server  *nbhttp.Server
}

func (c *Conn) handleMessage(opcode int8, data []byte) {
	switch opcode {
	case TextMessage, BinaryMessage:
		c.messageHandler(c, opcode, data)
	case CloseMessage:
		if len(data) >= 2 {
			code := int(binary.BigEndian.Uint16(data[:2]))
			c.closeHandler(c, code, string(data[2:]))
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

func (c *Conn) OnMessage(h func(*Conn, int8, []byte)) {
	if h != nil {
		c.messageHandler = func(c *Conn, messageType int8, data []byte) {
			c.Server.MessageHandlerExecutor(c.index, func() {
				h(c, messageType, data)
				c.Server.Free(data)
			})
		}
	}
}

func (c *Conn) OnClose(h func(*Conn, error)) {
	if h != nil {
		c.onClose = h
	}
}

func (c *Conn) WriteMessage(messageType int8, data []byte) error {
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
		return c.writeMessage(messageType, true, []byte{})

	} else {
		for len(data) > 0 {
			n := len(data)
			if n > framePayloadSize {
				n = framePayloadSize
			}
			err := c.writeMessage(messageType, n == len(data), data[:n])
			if err != nil {
				return err
			}
			data = data[n:]
		}
	}

	return nil
}

func (c *Conn) writeMessage(messageType int8, fin bool, data []byte) error {
	var (
		buf     []byte
		bodyLen = len(data)
		offset  = 2
	)
	if bodyLen < 126 {
		buf = c.Server.Malloc(len(data) + 2)
		buf[1] = byte(bodyLen)
	} else if bodyLen < 65535 {
		buf = c.Server.Malloc(len(data) + 4)
		buf[1] = 126
		binary.BigEndian.PutUint16(buf[2:4], uint16(bodyLen))
		offset = 4
	} else {
		buf = c.Server.Malloc(len(data) + 10)
		buf[1] = 127
		binary.BigEndian.PutUint64(buf[2:10], uint64(bodyLen))
		offset = 10
	}
	copy(buf[offset:], data)

	// opcode
	buf[0] = byte(messageType)

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
		messageHandler: func(*Conn, int8, []byte) {},
		onClose:        func(*Conn, error) {},
	}
	conn.pingHandler = func(*Conn, string) {
		conn.WriteMessage(PongMessage, nil)
	}
	conn.closeHandler = func(c *Conn, code int, text string) {
		if len(text)+2 > maxControlFramePayloadSize {
			return //ErrInvalidControlFrame
		}
		buf := c.Server.Malloc(len(text) + 2)
		binary.BigEndian.PutUint16(buf[:2], uint16(code))
		copy(buf[2:], text)
		conn.WriteMessage(CloseMessage, buf)
		c.Server.Free(buf)
	}
	return conn
}
