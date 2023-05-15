// Copyright 2020 lesismal. All rights reserved.
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package websocket

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"math/rand"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/lesismal/nbio/logging"
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
	commonFields
	net.Conn

	mux sync.Mutex

	session interface{}

	sendQueue     [][]byte
	sendQueueSize int

	subprotocol string

	closed                   bool
	isClient                 bool
	remoteCompressionEnabled bool
	enableWriteCompression   bool
	isBlockingMod            bool
	expectingFragments       bool
	compress                 bool
	opcode                   MessageType
	buffer                   []byte
	message                  []byte
}

// Close .
func (c *Conn) Close() error {
	return c.Conn.Close()
}

func (c *Conn) clearSendQueue() {
	c.mux.Lock()
	sendQueue := c.sendQueue
	c.sendQueue = [][]byte{}
	c.mux.Unlock()
	for _, buf := range sendQueue {
		mempool.Free(buf)
	}
}

// CompressionEnabled .
func (c *Conn) CompressionEnabled() bool {
	return c.compress
}

func (c *Conn) handleDataFrame(p *nbhttp.Parser, opcode MessageType, fin bool, data []byte) {
	h := c.dataFrameHandler
	if c.isBlockingMod {
		h(c, opcode, fin, data)
	} else {
		p.Execute(func() {
			h(c, opcode, fin, data)
		})
	}
}

func (c *Conn) handleMessage(p *nbhttp.Parser, opcode MessageType, body []byte) {
	if c.isBlockingMod {
		c.handleWsMessage(opcode, body)
	} else {
		if !p.Execute(func() {
			c.handleWsMessage(opcode, body)
		}) {
			if len(body) > 0 {
				c.Engine.BodyAllocator.Free(body)
			}
		}
	}
}

func (c *Conn) handleProtocolMessage(p *nbhttp.Parser, opcode MessageType, body []byte) {
	if c.isBlockingMod {
		c.handleWsMessage(opcode, body)
		if len(body) > 0 && c.Engine.ReleaseWebsocketPayload {
			c.Engine.BodyAllocator.Free(body)
		}
	} else {
		if !p.Execute(func() {
			c.handleWsMessage(opcode, body)
			if len(body) > 0 && c.Engine.ReleaseWebsocketPayload {
				c.Engine.BodyAllocator.Free(body)
			}
		}) {
			if len(body) > 0 {
				c.Engine.BodyAllocator.Free(body)
			}
		}
	}
}

func (c *Conn) handleWsMessage(opcode MessageType, data []byte) {
	if c.KeepaliveTime > 0 {
		defer c.SetReadDeadline(time.Now().Add(c.KeepaliveTime))
	}
	switch opcode {
	case BinaryMessage:
		c.messageHandler(c, opcode, data)
	case TextMessage:
		if !c.Engine.CheckUtf8(data) {
			const errText = "Invalid UTF-8 bytes"
			protoErrorData := make([]byte, 2+len(errText))
			binary.BigEndian.PutUint16(protoErrorData, 1002)
			copy(protoErrorData[2:], errText)
			c.WriteMessage(CloseMessage, protoErrorData)
			return
		}
		c.messageHandler(c, opcode, data)
	case CloseMessage:
		if len(data) >= 2 {
			code := int(binary.BigEndian.Uint16(data[:2]))
			if !validCloseCode(code) || !c.Engine.CheckUtf8(data[2:]) {
				protoErrorCode := make([]byte, 2)
				binary.BigEndian.PutUint16(protoErrorCode, 1002)
				c.WriteMessage(CloseMessage, protoErrorCode)
			} else {
				c.closeMessageHandler(c, code, string(data[2:]))
			}
		} else {
			c.WriteMessage(CloseMessage, nil)
		}
		// close immediately, no need to wait for data flushed on a blocked conn
		c.Conn.Close()
	case PingMessage:
		c.pingMessageHandler(c, string(data))
	case PongMessage:
		c.pongMessageHandler(c, string(data))
	case FragmentMessage:
		logging.Debug("invalid fragment message")
		c.Conn.Close()
	default:
		c.Conn.Close()
	}
}

func (c *Conn) nextFrame() (opcode MessageType, body []byte, ok, fin, res1, res2, res3 bool) {
	l := int64(len(c.buffer))
	headLen := int64(2)
	if l >= 2 {
		opcode = MessageType(c.buffer[0] & 0xF)
		res1 = int8(c.buffer[0]&0x40) != 0
		res2 = int8(c.buffer[0]&0x20) != 0
		res3 = int8(c.buffer[0]&0x10) != 0
		fin = ((c.buffer[0] & 0x80) != 0)
		payloadLen := c.buffer[1] & 0x7F
		bodyLen := int64(-1)

		switch payloadLen {
		case 126:
			if l >= 4 {
				bodyLen = int64(binary.BigEndian.Uint16(c.buffer[2:4]))
				headLen = 4
			}
		case 127:
			if len(c.buffer) >= 10 {
				bodyLen = int64(binary.BigEndian.Uint64(c.buffer[2:10]))
				headLen = 10
			}
		default:
			bodyLen = int64(payloadLen)
		}
		if bodyLen >= 0 {
			masked := (c.buffer[1] & 0x80) != 0
			if masked {
				headLen += 4
			}
			total := headLen + bodyLen
			if l >= total {
				body = c.buffer[headLen:total]
				if masked {
					maskKey := c.buffer[headLen-4 : headLen]
					for i := 0; i < len(body); i++ {
						body[i] ^= maskKey[i%4]
					}
				}

				ok = true
				c.buffer = c.buffer[total:l]
			}
		}
	}

	return opcode, body, ok, fin, res1, res2, res3
}

// Read .
func (c *Conn) Read(p *nbhttp.Parser, data []byte) error {
	oldLen := len(c.buffer)
	readLimit := c.Engine.ReadLimit
	if readLimit > 0 && ((oldLen+len(data) > readLimit) || ((oldLen + len(c.message) + len(data)) > readLimit)) {
		return nbhttp.ErrTooLong
	}

	var oldBuffer []byte
	if oldLen == 0 {
		c.buffer = data
	} else {
		c.buffer = mempool.Append(c.buffer, data...)
		oldBuffer = c.buffer
	}

	var err error
	for i := 0; true; i++ {
		opcode, body, ok, fin, res1, res2, res3 := c.nextFrame()
		if !ok {
			break
		}
		if err = c.validFrame(opcode, fin, res1, res2, res3, c.expectingFragments); err != nil {
			break
		}
		if opcode == FragmentMessage || opcode == TextMessage || opcode == BinaryMessage {
			if c.opcode == 0 {
				c.opcode = opcode
				c.compress = res1
			}
			bl := len(body)
			if c.dataFrameHandler != nil {
				var frame []byte
				if bl > 0 {
					if c.isMessageTooLarge(bl) {
						err = ErrMessageTooLarge
						break
					}
					frame = c.Engine.BodyAllocator.Malloc(bl)
					copy(frame, body)
				}
				if c.opcode == TextMessage && len(frame) > 0 && !c.Engine.CheckUtf8(frame) {
					c.Conn.Close()
				} else {
					c.handleDataFrame(p, c.opcode, fin, frame)
				}
			}
			if bl > 0 && c.messageHandler != nil {
				if c.message == nil {
					if c.isMessageTooLarge(len(body)) {
						err = ErrMessageTooLarge
						break
					}
					c.message = c.Engine.BodyAllocator.Malloc(len(body))
					copy(c.message, body)
				} else {
					if c.isMessageTooLarge(len(c.message) + len(body)) {
						err = ErrMessageTooLarge
						break
					}
					c.message = c.Engine.BodyAllocator.Append(c.message, body...)
				}
			}
			if fin {
				if c.messageHandler != nil {
					if c.compress {
						if c.Engine.WebsocketDecompressor != nil {
							var b []byte
							decompressor := c.Engine.WebsocketDecompressor()
							defer decompressor.Close()
							b, err = decompressor.Decompress(c.message)
							if err != nil {
								break
							}
							c.Engine.BodyAllocator.Free(c.message)
							c.message = b
						} else {
							var b []byte
							rc := decompressReader(io.MultiReader(bytes.NewBuffer(c.message), strings.NewReader(flateReaderTail)))
							b, err = c.readAll(rc, len(c.message)*2)
							c.Engine.BodyAllocator.Free(c.message)
							c.message = b
							rc.Close()
							if err != nil {
								break
							}
						}
					}
					c.handleMessage(p, c.opcode, c.message)
				}
				c.compress = false
				c.expectingFragments = false
				c.message = nil
				c.opcode = 0
			} else {
				c.expectingFragments = true
			}
		} else {
			var frame []byte
			if len(body) > 0 {
				if c.isMessageTooLarge(len(body)) {
					err = ErrMessageTooLarge
					break
				}
				frame = c.Engine.BodyAllocator.Malloc(len(body))
				copy(frame, body)
			}
			c.handleProtocolMessage(p, opcode, frame)
		}

		if len(c.buffer) == 0 {
			break
		}
	}

	if oldLen == 0 {
		if len(c.buffer) > 0 {
			tmp := c.buffer
			c.buffer = mempool.Malloc(len(tmp))
			copy(c.buffer, tmp)
		} else {
			c.buffer = nil
		}
	} else {
		if len(c.buffer) == 0 {
			mempool.Free(oldBuffer)
			c.buffer = nil
		} else if len(c.buffer) < len(oldBuffer) {
			tmp := mempool.Malloc(len(c.buffer))
			copy(tmp, c.buffer)
			c.buffer = tmp
			mempool.Free(oldBuffer)
		}
	}

	return err
}

// SetCloseHandler .
func (c *Conn) SetCloseHandler(h func(*Conn, int, string)) {
	if h != nil {
		c.closeMessageHandler = h
	}
}

// SetPingHandler .
func (c *Conn) SetPingHandler(h func(*Conn, string)) {
	if h != nil {
		c.pingMessageHandler = h
	}
}

// SetPongHandler .
func (c *Conn) SetPongHandler(h func(*Conn, string)) {
	if h != nil {
		c.pongMessageHandler = h
	}
}

// OnOpen .
func (c *Conn) OnOpen(h func(*Conn)) {
	c.openHandler = h
}

// OnMessage .
func (c *Conn) OnMessage(h func(*Conn, MessageType, []byte)) {
	if h != nil {
		c.messageHandler = func(c *Conn, messageType MessageType, data []byte) {
			if c.Engine.ReleaseWebsocketPayload && len(data) > 0 {
				defer c.Engine.BodyAllocator.Free(data)
			}
			h(c, messageType, data)
		}
	}
}

// OnDataFrame .
func (c *Conn) OnDataFrame(h func(*Conn, MessageType, bool, []byte)) {
	if h != nil {
		c.dataFrameHandler = func(c *Conn, messageType MessageType, fin bool, data []byte) {
			if c.Engine.ReleaseWebsocketPayload {
				defer c.Engine.BodyAllocator.Free(data)
			}
			h(c, messageType, fin, data)
		}
	}
}

// EnableCompression .
func (c *Conn) EnableCompression(enable bool) {
	c.enableCompression = enable
}

func (c *Conn) OnClose(h func(*Conn, error)) {
	if h == nil {
		h = func(*Conn, error) {}
	}
	c.onClose = func(c *Conn, err error) {
		c.mux.Lock()
		closed := c.closed
		c.closed = true
		c.mux.Unlock()
		if !closed {
			h(c, err)
			c.clearSendQueue()
		}
	}
}

// WriteMessage .
func (c *Conn) WriteMessage(messageType MessageType, data []byte) error {
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
		// compress = true
		// if user customize mempool, they should promise it's safe to mempool.Free a buffer which is not from their mempool.Malloc
		// or we need to implement a writebuffer that use mempool.Realloc to grow or append the buffer
		if c.Engine.WebsocketCompressor != nil {
			compressor := c.Engine.WebsocketCompressor()
			defer compressor.Close()
			data = compressor.Compress(data)
		} else {
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

	c.mux.Lock()
	if c.closed {
		c.mux.Unlock()
		mempool.Free(buf)
		return net.ErrClosed
	}

	if c.sendQueue != nil {
		if c.sendQueueSize > 0 && len(c.sendQueue) >= c.sendQueueSize {
			c.mux.Unlock()
			mempool.Free(buf)
			return ErrMessageSendQuqueIsFull
		}
		c.sendQueue = append(c.sendQueue, buf)
		isHead := (len(c.sendQueue) == 1)
		c.mux.Unlock()

		if isHead {
			go func() {
				i := 0
				for {
					b := c.sendQueue[i]
					c.sendQueue[i] = nil
					_, err := c.Conn.Write(b)
					mempool.Free(b)
					if err != nil {
						c.sendQueue = c.sendQueue[i:]
						return
					}

					i++
					c.mux.Lock()
					if c.closed {
						c.sendQueue = c.sendQueue[i:]
						c.mux.Unlock()
						return
					}
					if len(c.sendQueue) == i {
						c.sendQueue = c.sendQueue[0:0]
						c.mux.Unlock()
						return
					}

					c.mux.Unlock()
				}
			}()
		}
		return nil
	}
	c.mux.Unlock()

	_, err := c.Conn.Write(buf)
	mempool.Free(buf)

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

// Subprotocol returns the negotiated websocket subprotocol.
func (c *Conn) Subprotocol() string {
	return c.subprotocol
}

func NewConn(u *Upgrader, c net.Conn, subprotocol string, remoteCompressionEnabled bool, asyncWrite bool) *Conn {
	wsc := &Conn{
		commonFields:             u.commonFields,
		Conn:                     c,
		subprotocol:              subprotocol,
		remoteCompressionEnabled: remoteCompressionEnabled,
	}
	wsc.OnClose(u.onClose)
	if asyncWrite {
		wsc.sendQueue = make([][]byte, 16)[:0]
		wsc.sendQueueSize = u.BlockingModAsyncWriteQueueSize
	}
	return wsc
}

// BlockingModReadLoop .
func (c *Conn) BlockingModReadLoop(bufSize int) {
	var (
		n   int
		err error
		buf []byte
	)

	if bufSize <= 0 {
		bufSize = DefaultBlockingReadBufferSize
	}
	buf = make([]byte, bufSize)

	defer func() {
		c.CloseAndClean(err)
	}()

	for {
		n, err = c.Conn.Read(buf)
		if err != nil {
			break
		}
		err = c.Read(nil, buf[:n])
		if err != nil {
			break
		}
	}
}

// return false if length is ok.
func (c *Conn) isMessageTooLarge(len int) bool {
	if c.MessageLengthLimit == 0 {
		// 0 means unlimitted size
		return false
	}
	return len > c.MessageLengthLimit
}

func (c *Conn) validFrame(opcode MessageType, fin, res1, res2, res3, expectingFragments bool) error {
	if res1 && !c.enableCompression {
		return ErrReserveBitSet
	}
	if res2 || res3 {
		return ErrReserveBitSet
	}
	if opcode > BinaryMessage && opcode < CloseMessage {
		return fmt.Errorf("%w: opcode=%d", ErrReservedOpcodeSet, opcode)
	}
	if !fin && (opcode != FragmentMessage && opcode != TextMessage && opcode != BinaryMessage) {
		return fmt.Errorf("%w: opcode=%d", ErrControlMessageFragmented, opcode)
	}
	if expectingFragments && (opcode == TextMessage || opcode == BinaryMessage) {
		return ErrFragmentsShouldNotHaveBinaryOrTextOpcode
	}
	return nil
}

func (c *Conn) readAll(r io.Reader, size int) ([]byte, error) {
	const maxAppendSize = 1024 * 1024 * 4
	buf := c.Engine.BodyAllocator.Malloc(size)[0:0]
	for {
		n, err := r.Read(buf[len(buf):cap(buf)])
		if n > 0 {
			buf = buf[:len(buf)+n]
		}
		if err != nil {
			if err == io.EOF {
				err = nil
			}
			return buf, err
		}
		if len(buf) == cap(buf) {
			l := len(buf)
			al := l
			if al > maxAppendSize {
				al = maxAppendSize
			}
			buf = c.Engine.BodyAllocator.Append(buf, make([]byte, al)...)[:l]
		}
	}
}

// Close .
func (c *Conn) CloseAndClean(err error) {
	if c.Conn != nil {
		c.Conn.Close()
		c.onClose(c, err)
	}
	if c.buffer != nil {
		mempool.Free(c.buffer)
		c.buffer = nil
	}
	if c.message != nil {
		mempool.Free(c.message)
		c.message = nil
	}
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
