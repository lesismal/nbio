// Copyright 2020 lesismal. All rights reserved.
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package websocket

import (
	"bufio"
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

	closeErr                 error
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

// IsClient .
func (c *Conn) IsClient() bool {
	return c.isClient
}

// SetClient .
func (c *Conn) SetClient(isClient bool) {
	c.isClient = isClient
}

// IsBlockingMod .
func (c *Conn) IsBlockingMod() bool {
	return c.isBlockingMod
}

// IsAsyncWrite .
func (c *Conn) IsAsyncWrite() bool {
	return c.sendQueue != nil
}

// Close .
func (c *Conn) Close() error {
	if c.Conn == nil {
		return nil
	}
	return c.Conn.Close()
}

// CloseWithError .
func (c *Conn) CloseWithError(err error) error {
	c.SetCloseError(err)
	return c.Conn.Close()
}

// SetCloseError .
func (c *Conn) SetCloseError(err error) {
	c.mux.Lock()
	if c.closeErr == nil {
		c.closeErr = err
	}
	c.mux.Unlock()
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
	const errInvalidUtf8Text = "invalid UTF-8 bytes"

	if c.KeepaliveTime > 0 {
		defer c.SetReadDeadline(time.Now().Add(c.KeepaliveTime))
	}

	switch opcode {
	case BinaryMessage:
		c.messageHandler(c, opcode, data)
		return
	case TextMessage:
		if !c.Engine.CheckUtf8(data) {
			protoErrorData := make([]byte, 2+len(errInvalidUtf8Text))
			binary.BigEndian.PutUint16(protoErrorData, 1002)
			copy(protoErrorData[2:], errInvalidUtf8Text)
			c.SetCloseError(ErrInvalidUtf8)
			c.WriteMessage(CloseMessage, protoErrorData)
			goto ErrExit
		}
		c.messageHandler(c, opcode, data)
		return
	case PingMessage:
		c.pingMessageHandler(c, string(data))
		return
	case PongMessage:
		c.pongMessageHandler(c, string(data))
		return
	case CloseMessage:
		if len(data) >= 2 {
			code := int(binary.BigEndian.Uint16(data[:2]))
			if !validCloseCode(code) {
				protoErrorCode := make([]byte, 2)
				binary.BigEndian.PutUint16(protoErrorCode, 1002)
				c.SetCloseError(ErrInvalidCloseCode)
				c.WriteMessage(CloseMessage, protoErrorCode)
				goto ErrExit
			}
			if !c.Engine.CheckUtf8(data[2:]) {
				protoErrorData := make([]byte, 2+len(errInvalidUtf8Text))
				binary.BigEndian.PutUint16(protoErrorData, 1002)
				copy(protoErrorData[2:], errInvalidUtf8Text)
				c.SetCloseError(ErrInvalidUtf8)
				c.WriteMessage(CloseMessage, protoErrorData)
				goto ErrExit
			}

			reson := string(data[2:])
			if code != 1000 {
				c.SetCloseError(&CloseError{
					Code:   code,
					Reason: reson,
				})
			}
			c.closeMessageHandler(c, code, reson)
		} else {
			c.SetCloseError(ErrInvalidControlFrame)
		}
	case FragmentMessage:
		logging.Debug("invalid fragment message")
		c.SetCloseError(ErrInvalidFragmentMessage)
	default:
		logging.Debug("invalid message type: %v", opcode)
		c.SetCloseError(fmt.Errorf("websocket: invalid message type: %v", opcode))
	}

ErrExit:
	if c.IsAsyncWrite() {
		if c.Engine.IsTimerRunning() {
			c.Engine.AfterFunc(time.Second, func() { c.Conn.Close() })
		} else {
			time.AfterFunc(time.Second, func() { c.Conn.Close() })
		}
	} else {
		c.Conn.Close()
	}
}

func (c *Conn) nextFrame() (opcode MessageType, body []byte, ok, fin, compress, reserve bool, err error) {
	l := int64(len(c.buffer))
	headLen := int64(2)
	if l >= 2 {
		opcode = MessageType(c.buffer[0] & 0xF)
		compress = int8(c.buffer[0]&0x40) != 0
		reserve = int8(c.buffer[0]&0x30) != 0
		fin = ((c.buffer[0] & 0x80) != 0)
		payloadLen := c.buffer[1] & 0x7F
		frameLen := int64(-1)

		switch payloadLen {
		case 126:
			if l >= 4 {
				frameLen = int64(binary.BigEndian.Uint16(c.buffer[2:4]))
				headLen = 4
			}
		case 127:
			if len(c.buffer) >= 10 {
				frameLen = int64(binary.BigEndian.Uint64(c.buffer[2:10]))
				headLen = 10
			}
		default:
			frameLen = int64(payloadLen)
		}

		if (frameLen > maxControlFramePayloadSize) &&
			((opcode == PingMessage) || (opcode == PongMessage) || (opcode == CloseMessage)) {
			err = ErrControlMessageTooBig
			return
		}

		if frameLen >= 0 {
			masked := (c.buffer[1] & 0x80) != 0
			if masked {
				headLen += 4
			}
			total := headLen + frameLen
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

	return opcode, body, ok, fin, compress, reserve, err
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
		opcode, body, ok, fin, compress, reserve, e := c.nextFrame()
		if e != nil {
			err = e
			break
		}
		if !ok {
			break
		}
		if err = c.validFrame(opcode, fin, compress, reserve, c.expectingFragments); err != nil {
			break
		}
		if opcode == FragmentMessage || opcode == TextMessage || opcode == BinaryMessage {
			if c.opcode == 0 {
				c.opcode = opcode
				c.compress = compress
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
						var b []byte
						var rc io.ReadCloser
						if c.Engine.WebsocketDecompressor != nil {
							rc = c.Engine.WebsocketDecompressor(io.MultiReader(bytes.NewBuffer(c.message), strings.NewReader(flateReaderTail)))
						} else {
							rc = decompressReader(io.MultiReader(bytes.NewBuffer(c.message), strings.NewReader(flateReaderTail)))
						}
						b, err = c.readAll(rc, len(c.message)*2)
						c.Engine.BodyAllocator.Free(c.message)
						c.message = b
						rc.Close()
						if err != nil {
							break
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
	c.onClose = h
}

// WriteClose .
func (c *Conn) WriteClose(code int, reason string) error {
	buf := make([]byte, 2+len(reason))
	binary.BigEndian.PutUint16(buf[:2], uint16(code))
	copy(buf[2:], reason)
	return c.WriteMessage(CloseMessage, buf)
}

// WriteMessage .
func (c *Conn) WriteMessage(messageType MessageType, data []byte) error {
	switch messageType {
	case TextMessage:
	case BinaryMessage:
	case PingMessage, PongMessage, CloseMessage:
		if len(data) > maxControlFramePayloadSize {
			return ErrControlMessageTooBig
		}
	case FragmentMessage:
	default:
	}

	compress := c.enableWriteCompression && (messageType == TextMessage || messageType == BinaryMessage)
	if compress {
		// compress = true
		// if user customize mempool, they should promise it's safe to mempool.Free a buffer which is not from their mempool.Malloc
		// or we need to implement a writebuffer that use mempool.Realloc to grow or append the buffer
		w := &writeBuffer{
			Buffer: bytes.NewBuffer(mempool.Malloc(len(data))),
		}
		defer w.Close()
		w.Reset()

		var cw io.WriteCloser
		if c.Engine.WebsocketCompressor != nil {
			cw = c.Engine.WebsocketCompressor(w, c.compressionLevel)
		} else {
			cw = compressWriter(w, c.compressionLevel)
		}
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

// CloseAndClean .
func (c *Conn) CloseAndClean(err error) {
	c.mux.Lock()
	closed := c.closed
	c.closed = true
	if closed {
		c.mux.Unlock()
		return
	} else {
		for i, b := range c.sendQueue {
			if b != nil {
				mempool.Free(b)
				c.sendQueue[i] = nil
			}
		}

		if c.closeErr == nil {
			c.closeErr = err
		}
	}
	c.mux.Unlock()

	if c.Conn != nil {
		c.Conn.Close()
	}

	if c.onClose != nil {
		c.onClose(c, c.closeErr)
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
		if isHead {
			c.sendQueue[0] = nil
		}
		c.mux.Unlock()

		if isHead {
			go func() {
				i := 0
				for {
					_, err := c.Conn.Write(buf)
					mempool.Free(buf)
					if err != nil {
						c.CloseWithError(err)
						return
					}

					i++
					c.mux.Lock()
					if c.closed {
						c.mux.Unlock()
						return
					}
					if len(c.sendQueue) <= i {
						c.sendQueue = c.sendQueue[:0]
						c.mux.Unlock()
						return
					}

					buf = c.sendQueue[i]
					c.sendQueue[i] = nil

					c.mux.Unlock()

					if buf == nil {
						return
					}
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
	if asyncWrite {
		wsc.sendQueue = make([][]byte, u.BlockingModSendQueueInitSize)[:0]
		wsc.sendQueueSize = u.BlockingModSendQueueMaxSize
	}
	return wsc
}

type header [14]byte

func (h *header) MessageType() MessageType {
	return MessageType((*h)[0] & 0xF)
}

func (h *header) Fin() bool {
	return int8((*h)[0]&0x80) != 0
}

func (h *header) Compress() bool {
	return int8((*h)[0]&0x40) != 0
}

func (h *header) Reserve() bool {
	return int8((*h)[0]&0x30) != 0
}

func (h *header) Masked() bool {
	return (*h)[1]&0x80 != 0
}

func (h *header) Mask() []byte {
	return (*h)[10:]
}

func (c *Conn) readHead(reader *bufio.Reader, head *header) (int, error) {
	_, err := io.ReadFull(reader, (*head)[:2])
	if err != nil {
		return 0, err
	}

	frameLen := int((*head)[1] & 0x7F)
	switch frameLen {
	case 126:
		_, err = io.ReadFull(reader, (*head)[2:4])
		if err != nil {
			return 0, err
		}
		frameLen = int(binary.BigEndian.Uint16((*head)[2:4]))
	case 127:
		_, err = io.ReadFull(reader, (*head)[2:10])
		if err != nil {
			return 0, err
		}
		frameLen = int(binary.BigEndian.Uint64((*head)[2:10]))
	default:
	}

	if err = c.validFrame(head.MessageType(), head.Fin(), head.Compress(), head.Reserve(), c.expectingFragments); err != nil {
		return frameLen, err
	}

	if c.isMessageTooLarge(frameLen) {
		return frameLen, ErrMessageTooLarge
	}

	switch head.MessageType() {
	case PingMessage, PongMessage, CloseMessage:
		if frameLen > maxControlFramePayloadSize {
			return frameLen, ErrControlMessageTooBig
		}
	default:
	}

	if head.Masked() {
		_, err = io.ReadFull(reader, (*head)[10:14])
	}

	return frameLen, err
}

func (c *Conn) readBody(reader *bufio.Reader, head *header, frameLen int) ([]byte, error) {
	reserveLen := 0
	if head.Fin() {
		reserveLen = frameLen
		if reserveLen > 4096 {
			reserveLen = 4096
		}
	}
	cached := len(c.message)
	if c.isMessageTooLarge(cached + frameLen) {
		return nil, ErrMessageTooLarge
	}
	if c.message == nil {
		c.message = c.Engine.BodyAllocator.Malloc(frameLen + reserveLen)[:frameLen]
	} else {
		capSize := cap(c.message)
		futureLen := len(c.message) + frameLen + reserveLen
		thisLen := len(c.message) + frameLen
		if cap(c.message) >= futureLen {
			c.message = c.message[:thisLen]
		} else {
			c.message = append(c.message[:capSize], make([]byte, futureLen-capSize)...)
		}
	}
	_, err := io.ReadFull(reader, c.message[cached:])
	if err != nil {
		return nil, err
	}

	if head.Masked() {
		maskKey := (*head)[10:14]
		body := c.message[cached:]
		for i := 0; i < frameLen; i++ {
			body[i] ^= maskKey[i%4]
		}
	}

	return c.message[cached:], nil
}

func (c *Conn) getReader(rw *bufio.ReadWriter) *bufio.Reader {
	var reader *bufio.Reader
	if rw == nil {
		reader = bufio.NewReaderSize(c.Conn, 4096)
	} else {
		reader = rw.Reader
	}
	return reader
}

func (c *Conn) BlockingModReadLoopByReadWriter(reader *bufio.Reader) {
	var (
		err      error
		frameLen int
		header   = &header{}
		frame    []byte
	)

	defer func() {
		c.CloseAndClean(err)
	}()

	for {
		frameLen, err = c.readHead(reader, header)
		if err != nil {
			return
		}

		frame, err = c.readBody(reader, header, frameLen)
		if err != nil {
			return
		}
		if c.dataFrameHandler != nil {
			if header.MessageType() == TextMessage && len(frame) > 0 && !c.Engine.CheckUtf8(frame) {
				err = ErrInvalidUtf8
				c.Conn.Close()
			}
			frameCopy := frame
			if c.Engine.ReleaseWebsocketPayload {
				frameCopy = c.Engine.BodyAllocator.Malloc(frameLen)
				copy(frameCopy, frame)
			}
			c.handleDataFrame(nil, header.MessageType(), header.Fin(), frameCopy)
		}

		switch header.MessageType() {
		case PingMessage, PongMessage, CloseMessage:
			c.handleWsMessage(header.MessageType(), c.message)
			c.message = nil
		case TextMessage, BinaryMessage:
			if header.Fin() {
				if header.Compress() {
					var b []byte
					var rc io.ReadCloser
					if c.Engine.WebsocketDecompressor != nil {
						rc = c.Engine.WebsocketDecompressor(io.MultiReader(bytes.NewBuffer(c.message), strings.NewReader(flateReaderTail)))
					} else {
						rc = decompressReader(io.MultiReader(bytes.NewBuffer(c.message), strings.NewReader(flateReaderTail)))
					}
					b, err = c.readAll(rc, len(c.message)*2)
					c.Engine.BodyAllocator.Free(c.message)
					c.message = b
					rc.Close()
					if err != nil {
						break
					}
				}
				c.handleWsMessage(header.MessageType(), c.message)
				c.message = nil
				c.expectingFragments = false
			} else {
				c.expectingFragments = true
			}
		default:
		}
	}
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

func (c *Conn) validFrame(opcode MessageType, fin, compress, reserve bool, expectingFragments bool) error {
	if compress && !c.enableCompression {
		return ErrReserveBitSet
	}
	if reserve {
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
