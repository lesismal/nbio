// Copyright 2020 lesismal. All rights reserved.
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package websocket

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
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
	*commonFields
	net.Conn

	mux sync.Mutex

	closeErr error

	chSessionInited chan struct{}
	session         interface{}

	subprotocol string

	compressionLevel int
	onClose          func(c *Conn, err error)

	sendQueue                []*[]byte
	sendQueueSize            uint16
	closed                   bool
	isClient                 bool
	enableCompression        bool
	remoteCompressionEnabled bool
	enableWriteCompression   bool
	isBlockingMod            bool
	isReadingByParser        bool
	isInReadingLoop          bool
	expectingFragments       bool
	compress                 bool
	releasePayload           bool
	msgType                  MessageType
	message                  *[]byte
	bytesCached              *[]byte

	Engine  *nbhttp.Engine
	Execute func(f func()) bool
}

//go:norace
func (c *Conn) UnderlayerConn() net.Conn {
	return c.Conn
}

// IsClient .
//
//go:norace
func (c *Conn) IsClient() bool {
	return c.isClient
}

// SetClient .
//
//go:norace
func (c *Conn) SetClient(isClient bool) {
	c.isClient = isClient
}

// IsBlockingMod .
//
//go:norace
func (c *Conn) IsBlockingMod() bool {
	return c.isBlockingMod
}

// IsAsyncWrite .
//
//go:norace
func (c *Conn) IsAsyncWrite() bool {
	return c.sendQueue != nil
}

// Close .
//
//go:norace
func (c *Conn) Close() error {
	if c.Conn == nil {
		return nil
	}
	if c.IsAsyncWrite() {
		c.Engine.AfterFunc(c.BlockingModAsyncCloseDelay, func() { _ = c.Conn.Close() })
		return nil
	}
	return c.Conn.Close()
}

// CloseWithError .
//
//go:norace
func (c *Conn) CloseWithError(err error) {
	c.SetCloseError(err)
	_ = c.Close()
}

// SetCloseError .
//
//go:norace
func (c *Conn) SetCloseError(err error) {
	c.mux.Lock()
	if c.closeErr == nil {
		c.closeErr = err
	}
	c.mux.Unlock()
}

// CompressionEnabled .
//
//go:norace
func (c *Conn) CompressionEnabled() bool {
	return c.compress
}

//go:norace
func (c *Conn) safeBufferPointer(pbody *[]byte) *[]byte {
	if pbody == nil {
		var b []byte
		pbody = &b
	}
	return pbody
}

//go:norace
func (c *Conn) handleDataFrame(opcode MessageType, fin bool, pbody *[]byte) {
	pbody = c.safeBufferPointer(pbody)

	h := c.dataFrameHandler
	if c.isBlockingMod {
		if c.releasePayload {
			defer c.Engine.BodyAllocator.Free(pbody)
		}
		c.Engine.SyncCall(func() {
			h(c, opcode, fin, pbody)
		})
	} else {
		if !c.Execute(func() {
			if c.releasePayload {
				defer c.Engine.BodyAllocator.Free(pbody)
			}
			h(c, opcode, fin, pbody)
		}) {
			if c.releasePayload {
				defer c.Engine.BodyAllocator.Free(pbody)
			}
		}
	}
}

//go:norace
func (c *Conn) handleMessage(opcode MessageType, pbody *[]byte) {
	pbody = c.safeBufferPointer(pbody)

	if c.isBlockingMod {
		if c.releasePayload {
			defer c.Engine.BodyAllocator.Free(pbody)
		}
		c.Engine.SyncCall(func() {
			c.handleWsMessage(opcode, pbody)
		})
	} else {
		if !c.Execute(func() {
			if c.releasePayload {
				defer c.Engine.BodyAllocator.Free(pbody)
			}
			c.handleWsMessage(opcode, pbody)
		}) {
			if c.releasePayload {
				defer c.Engine.BodyAllocator.Free(pbody)
			}
		}
	}
}

//go:norace
func (c *Conn) handleProtocolMessage(opcode MessageType, pbody *[]byte) {
	c.handleMessage(opcode, pbody)
}

//go:norace
func (c *Conn) handleWsMessage(opcode MessageType, pData *[]byte) {
	const errInvalidUtf8Text = "invalid UTF-8 bytes"

	if c.KeepaliveTime > 0 {
		defer func() { _ = c.SetReadDeadline(time.Now().Add(c.KeepaliveTime)) }()
	}

	dataToString := func() string {
		s := ""
		if pData != nil {
			s = string(*pData)
		}
		return s
	}

	switch opcode {
	case BinaryMessage:
		c.messageHandler(c, opcode, pData)
		return
	case TextMessage:
		if pData != nil && !c.Engine.CheckUtf8(*pData) {
			protoErrorData := make([]byte, 2+len(errInvalidUtf8Text))
			binary.BigEndian.PutUint16(protoErrorData, 1002)
			copy(protoErrorData[2:], errInvalidUtf8Text)
			c.SetCloseError(ErrInvalidUtf8)
			_ = c.WriteMessage(CloseMessage, protoErrorData)
			goto ErrExit
		}
		c.messageHandler(c, opcode, pData)
		return
	case PingMessage:
		c.pingMessageHandler(c, dataToString())
		return
	case PongMessage:
		c.pongMessageHandler(c, dataToString())
		return
	case CloseMessage:
		var code int
		var reason string
		if pData == nil || len(*pData) == 0 {
			code = 1005 // no status
		} else if pData != nil && len(*pData) >= 2 {
			code = int(binary.BigEndian.Uint16((*pData)[:2]))
			if !validCloseCode(code) {
				protoErrorCode := make([]byte, 2)
				binary.BigEndian.PutUint16(protoErrorCode, 1002)
				c.SetCloseError(ErrInvalidCloseCode)
				_ = c.WriteMessage(CloseMessage, protoErrorCode)
				goto ErrExit
			}
			if !c.Engine.CheckUtf8((*pData)[2:]) {
				protoErrorData := make([]byte, 2+len(errInvalidUtf8Text))
				binary.BigEndian.PutUint16(protoErrorData, 1002)
				copy(protoErrorData[2:], errInvalidUtf8Text)
				c.SetCloseError(ErrInvalidUtf8)
				_ = c.WriteMessage(CloseMessage, protoErrorData)
				goto ErrExit
			}
			reason = string((*pData)[2:])
		} else {
			code = 1002 // protocol_error
		}
		if code != 1000 {
			c.SetCloseError(&CloseError{
				Code:   code,
				Reason: reason,
			})
		}
		c.closeMessageHandler(c, code, reason)
	case FragmentMessage:
		logging.Debug("invalid fragment message")
		c.SetCloseError(ErrInvalidFragmentMessage)
	default:
		logging.Debug("invalid message type: %v", opcode)
		c.SetCloseError(fmt.Errorf("websocket: invalid message type: %v", opcode))
	}

ErrExit:
	_ = c.Close()
}

//go:norace
func (c *Conn) nextFrame() (int, MessageType, []byte, bool, bool, bool, error) {
	var (
		opcode                    MessageType
		body                      []byte
		ok, fin, res1, res2, res3 bool
		err                       error
		pdata                     = c.bytesCached
		l                         int64
		headLen                   = int64(2)
		total                     int64
	)
	if pdata != nil {
		l = int64(len(*pdata))
	}
	if l >= 2 {
		opcode = MessageType((*pdata)[0] & 0xF)
		res1 = int8((*pdata)[0]&0x40) != 0
		res2 = int8((*pdata)[0]&0x20) != 0
		res3 = int8((*pdata)[0]&0x10) != 0
		fin = (((*pdata)[0] & 0x80) != 0)
		payloadLen := (*pdata)[1] & 0x7F
		bodyLen := int64(-1)

		switch payloadLen {
		case 126:
			if l >= 4 {
				bodyLen = int64(binary.BigEndian.Uint16((*pdata)[2:4]))
				headLen = 4
			}
		case 127:
			if len(*pdata) >= 10 {
				bodyLen = int64(binary.BigEndian.Uint64((*pdata)[2:10]))
				headLen = 10
			}
		default:
			bodyLen = int64(payloadLen)
		}

		ml := 0
		if c.message != nil {
			ml = len(*c.message)
		}
		if c.isMessageTooLarge(ml + int(bodyLen)) {
			return 0, 0, nil, false, false, false, ErrMessageTooLarge
		}

		if (bodyLen > maxControlFramePayloadSize) &&
			((opcode == PingMessage) || (opcode == PongMessage) || (opcode == CloseMessage)) {
			return 0, 0, nil, false, false, false, ErrControlMessageTooBig
		}

		if bodyLen >= 0 {
			masked := ((*pdata)[1] & 0x80) != 0
			if masked {
				headLen += 4
			}
			total = headLen + bodyLen
			if l >= total {
				body = (*pdata)[headLen:total]
				if masked {
					maskXOR(body, (*pdata)[headLen-4:headLen])
				}

				ok = true
				err = c.validFrame(opcode, fin, res1, res2, res3, c.expectingFragments)
			}
		}
	}

	return int(total), opcode, body, ok, fin, res1, err
}

// Read .
//
//go:norace
func (c *Conn) Parse(data []byte) error {
	if len(data) == 0 {
		return nil
	}

	c.mux.Lock()
	if c.closed {
		c.mux.Unlock()
		return net.ErrClosed
	}

	readLimit := c.Engine.ReadLimit
	if readLimit > 0 && (c.bytesCached != nil && (len(*c.bytesCached)+len(data) > readLimit)) {
		c.mux.Unlock()
		return nbhttp.ErrTooLong
	}

	var allocator = c.Engine.BodyAllocator
	if c.bytesCached == nil {
		c.bytesCached = allocator.Malloc(len(data))
		copy(*c.bytesCached, data)
	} else {
		c.bytesCached = allocator.Append(c.bytesCached, data...)
	}
	c.mux.Unlock()

	var err error
	var body []byte
	var frame *[]byte
	var message *[]byte
	var msgType MessageType
	var protocolMessage *[]byte
	var isProtocolMessage bool
	var opcode MessageType
	var ok, fin, compress bool
	var totalFrameSize int

	releaseBuf := func() {
		if frame != nil {
			allocator.Free(frame)
		}
		if message != nil {
			allocator.Free(message)
		}
		if protocolMessage != nil {
			allocator.Free(protocolMessage)
		}
	}

	for !c.closed {
		func() {
			c.mux.Lock()
			defer c.mux.Unlock()
			if c.closed {
				err = net.ErrClosed
				return
			}
			totalFrameSize, opcode, body, ok, fin, compress, err = c.nextFrame()
			if err != nil {
				return
			}
			if !ok {
				return
			}

			bl := len(body)
			switch opcode {
			case FragmentMessage, TextMessage, BinaryMessage:
				if c.msgType == 0 {
					c.msgType = opcode
					c.compress = compress
				}
				msgType = c.msgType
				if bl > 0 && c.dataFrameHandler != nil {
					frame = allocator.Malloc(bl)
					copy(*frame, body)
					// if compressed, should check utf8 after decompressed the whole message.
					// if c.msgType == TextMessage && len(frame) > 0 && !c.Engine.CheckUtf8(frame) {
					// 	c.Conn.Close()
					// 	err = ErrInvalidUtf8
					// 	return
					// }
				}
				if c.messageHandler != nil {
					if bl > 0 {
						if c.message == nil {
							c.message = allocator.Malloc(len(body))
							copy(*c.message, body)
						} else {
							c.message = allocator.Append(c.message, body...)
						}
					}
					if fin {
						message = c.message
						c.message = nil
						if c.compress {
							var pb *[]byte
							var rc io.ReadCloser
							if c.WebsocketDecompressor != nil {
								rc = c.WebsocketDecompressor(c, io.MultiReader(bytes.NewBuffer(*message), strings.NewReader(flateReaderTail)))
							} else {
								rc = decompressReader(io.MultiReader(bytes.NewBuffer(*message), strings.NewReader(flateReaderTail)))
							}
							pb, err = c.readAll(rc, len(*message)*2)
							allocator.Free(message)
							message = pb
							_ = rc.Close()
							if err != nil {
								releaseBuf()
								return
							}
						}
						c.msgType = 0
						c.compress = false
						c.expectingFragments = false
					} else {
						c.expectingFragments = true
					}
				}
			case PingMessage, PongMessage, CloseMessage:
				isProtocolMessage = true
				if bl > 0 {
					protocolMessage = allocator.Malloc(len(body))
					copy(*protocolMessage, body)
				}
			default:
				err = ErrInvalidFragmentMessage
				return
			}

			l := len(*c.bytesCached)
			if l == totalFrameSize {
				c.Engine.BodyAllocator.Free(c.bytesCached)
				c.bytesCached = nil
			} else {
				copy(*c.bytesCached, (*c.bytesCached)[totalFrameSize:l])
				*c.bytesCached = (*c.bytesCached)[:l-totalFrameSize]
			}
		}()

		if err != nil {
			if errors.Is(err, ErrMessageTooLarge) || errors.Is(err, ErrControlMessageTooBig) {
				_ = c.WriteClose(1009, err.Error())
			}
			return err
		}

		if message != nil {
			c.handleMessage(msgType, message)
			message = nil
		}
		if frame != nil {
			c.handleDataFrame(msgType, fin, frame)
			frame = nil
		}
		if isProtocolMessage {
			c.handleProtocolMessage(opcode, protocolMessage)
			protocolMessage = nil
			isProtocolMessage = false
		}

		// need more data
		if !ok {
			break
		}
	}

	return nil
}

// OnMessage .
//
//go:norace
func (c *Conn) OnMessage(h func(*Conn, MessageType, []byte)) {
	c.messageHandler = func(c *Conn, messageType MessageType, messagePtr *[]byte) {
		if !c.closed && h != nil {
			if messagePtr != nil {
				h(c, messageType, *messagePtr)
			} else {
				h(c, messageType, nil)
			}
		}
	}
}

// OnMessagePtr .
//
//go:norace
func (c *Conn) OnMessagePtr(h func(*Conn, MessageType, *[]byte)) {
	c.messageHandler = func(c *Conn, messageType MessageType, messagePtr *[]byte) {
		if !c.closed && h != nil {
			h(c, messageType, messagePtr)
		}
	}
}

// OnDataFrame .
//
//go:norace
func (c *Conn) OnDataFrame(h func(*Conn, MessageType, bool, []byte)) {
	c.dataFrameHandler = func(c *Conn, messageType MessageType, fin bool, framePtr *[]byte) {
		if !c.closed && h != nil {
			if framePtr != nil {
				h(c, messageType, fin, *framePtr)
			} else {
				h(c, messageType, fin, nil)
			}
		}
	}
}

// OnDataFramePtr .
//
//go:norace
func (c *Conn) OnDataFramePtr(h func(*Conn, MessageType, bool, *[]byte)) {
	c.dataFrameHandler = func(c *Conn, messageType MessageType, fin bool, framePtr *[]byte) {
		if !c.closed && h != nil {
			h(c, messageType, fin, framePtr)
		}
	}
}

// EnableCompression .
//
//go:norace
func (c *Conn) EnableCompression(enable bool) {
	c.enableCompression = enable
}

//go:norace
func (c *Conn) OnClose(h func(*Conn, error)) {
	c.onClose = h
}

// WriteClose .
//
//go:norace
func (c *Conn) WriteClose(code int, reason string) error {
	buf := make([]byte, 2+len(reason))
	binary.BigEndian.PutUint16(buf[:2], uint16(code))
	copy(buf[2:], reason)
	return c.WriteMessage(CloseMessage, buf)
}

// WriteMessage .
//
//go:norace
func (c *Conn) WriteMessage(messageType MessageType, data []byte) error {
	c.mux.Lock()
	defer c.mux.Unlock()

	if c.closed {
		return net.ErrClosed
	}

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
		w := &writeBuffer{
			allocator: c.Engine.BodyAllocator,
		}
		defer func() { _ = w.Close() }()

		var cw io.WriteCloser
		if c.WebsocketCompressor != nil {
			cw = c.WebsocketCompressor(c, w, c.compressionLevel)
		} else {
			cw = compressWriter(w, c.compressionLevel)
		}
		_, err := cw.Write(data)
		if err != nil {
			compress = false
		} else {
			_ = cw.Close()
			if w.pbuf != nil {
				data = *w.pbuf
			}
		}
	}

	if len(data) > 0 {
		sendOpcode := true
		sendCompress := compress
		for len(data) > 0 {
			n := len(data)
			if n > c.Engine.MaxWebsocketFramePayloadSize {
				n = c.Engine.MaxWebsocketFramePayloadSize
			}
			err := c.writeFrame(messageType, sendOpcode, n == len(data), data[:n], sendCompress)
			if err != nil {
				return err
			}
			sendOpcode = false
			sendCompress = false
			data = data[n:]
		}
		return nil
	}

	return c.writeFrame(messageType, true, true, []byte{}, compress)
}

// Keepalive .
//
//go:norace
func (c *Conn) Keepalive(d time.Duration) *time.Timer {
	var fn func()
	var timer *time.Timer
	fn = func() {
		err := c.WriteMessage(PingMessage, []byte{})
		if err != nil {
			return
		}
		timer.Reset(d)
	}
	timer = time.AfterFunc(d, fn)
	return timer
}

// Session returns user session.
//
//go:norace
func (c *Conn) Session() interface{} {
	if c.chSessionInited == nil {
		return c.session
	}
	return c.SessionWithLock()
}

// SessionWithLock returns user session with lock, returns as soon as the session has been seted.
//
//go:norace
func (c *Conn) SessionWithLock() interface{} {
	c.mux.Lock()
	ch := c.chSessionInited
	c.mux.Unlock()
	if ch != nil {
		<-ch
	}
	return c.session
}

// SessionWithContext returns user session, returns as soon as the session has been seted or
// waits until the context is done.
//
//go:norace
func (c *Conn) SessionWithContext(ctx context.Context) interface{} {
	c.mux.Lock()
	ch := c.chSessionInited
	c.mux.Unlock()
	if ch != nil {
		select {
		case <-ch:
		case <-ctx.Done():
		}

	}
	return c.session
}

// SetSession sets user session.
//
//go:norace
func (c *Conn) SetSession(session interface{}) {
	c.mux.Lock()
	c.session = session
	if c.chSessionInited != nil {
		close(c.chSessionInited)
		c.chSessionInited = nil
	}
	c.mux.Unlock()
}

type writeBuffer struct {
	pbuf      *[]byte
	allocator mempool.Allocator
}

// Write .
//
//go:norace
func (w *writeBuffer) Write(p []byte) (n int, err error) {
	if w.pbuf == nil {
		w.pbuf = w.allocator.Malloc(len(p))
		return copy(*w.pbuf, p), nil
	}
	w.pbuf = w.allocator.Append(w.pbuf, p...)
	return len(p), nil
}

// Close .
//
//go:norace
func (w *writeBuffer) Close() error {
	if w.pbuf != nil {
		w.allocator.Free(w.pbuf)
		w.pbuf = nil
	}
	return nil
}

// CloseAndClean .
//
//go:norace
func (c *Conn) CloseAndClean(err error) {
	// c.WriteClose(1000, "normal close")
	c.mux.Lock()
	if c.closed {
		c.mux.Unlock()
		return
	}

	c.closed = true

	if c.chSessionInited != nil {
		close(c.chSessionInited)
		c.chSessionInited = nil
	}

	for i, b := range c.sendQueue {
		if b != nil {
			c.Engine.BodyAllocator.Free(b)
			c.sendQueue[i] = nil
		}
	}

	if c.closeErr == nil {
		c.closeErr = err
	}

	if c.Conn != nil {
		_ = c.Conn.Close()
	}

	if c.bytesCached != nil {
		c.Engine.BodyAllocator.Free(c.bytesCached)
		c.bytesCached = nil
	}
	if c.message != nil {
		c.Engine.BodyAllocator.Free(c.message)
		c.message = nil
	}

	c.mux.Unlock()

	if c.onClose != nil {
		c.onClose(c, c.closeErr)
	}
}

// WriteFrame .
//
//go:norace
func (c *Conn) WriteFrame(messageType MessageType, sendOpcode, fin bool, data []byte) error {
	c.mux.Lock()
	defer c.mux.Unlock()

	if c.closed {
		return net.ErrClosed
	}

	return c.writeFrame(messageType, sendOpcode, fin, data, false)
}

//go:norace
func (c *Conn) writeFrame(messageType MessageType, sendOpcode, fin bool, data []byte, compress bool) error {
	var (
		pbuf    *[]byte
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
		pbuf = c.Engine.BodyAllocator.Malloc(len(data) + headLen)
		(*pbuf)[0] = 0
		(*pbuf)[1] = (byte1 | byte(bodyLen))
	} else if bodyLen <= 65535 {
		headLen = 4 + maskLen
		pbuf = c.Engine.BodyAllocator.Malloc(len(data) + headLen)
		(*pbuf)[0] = 0
		(*pbuf)[1] = (byte1 | 126)
		binary.BigEndian.PutUint16((*pbuf)[2:4], uint16(bodyLen))
	} else {
		headLen = 10 + maskLen
		pbuf = c.Engine.BodyAllocator.Malloc(len(data) + headLen)
		(*pbuf)[0] = 0
		(*pbuf)[1] = (byte1 | 127)
		binary.BigEndian.PutUint64((*pbuf)[2:10], uint64(bodyLen))
	}

	if c.isClient {
		u32 := rand.Uint32()
		binary.LittleEndian.PutUint32((*pbuf)[headLen-4:headLen], u32)
		copy((*pbuf)[headLen:], data)
		maskXOR((*pbuf)[headLen:], (*pbuf)[headLen-4:headLen])
	} else {
		copy((*pbuf)[headLen:], data)
	}

	// opcode
	if sendOpcode {
		(*pbuf)[0] = byte(messageType)
	} else {
		(*pbuf)[0] = 0
	}

	if compress {
		(*pbuf)[0] |= 0x40
	}

	// fin
	if fin {
		(*pbuf)[0] |= byte(0x80)
	}

	if c.sendQueue != nil {
		if c.sendQueueSize > 0 && len(c.sendQueue) >= int(c.sendQueueSize) {
			c.Engine.BodyAllocator.Free(pbuf)
			return ErrMessageSendQuqueIsFull
		}
		c.sendQueue = append(c.sendQueue, pbuf)
		isHead := (len(c.sendQueue) == 1)

		if isHead {
			c.sendQueue[0] = nil
			go func() {
				i := 0
				for {
					_, err := c.Conn.Write(*pbuf)
					c.Engine.BodyAllocator.Free(pbuf)
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

					pbuf = c.sendQueue[i]
					c.sendQueue[i] = nil

					c.mux.Unlock()

					if pbuf == nil {
						return
					}
				}
			}()
		}
		return nil
	}

	_, err := c.Conn.Write(*pbuf)
	c.Engine.BodyAllocator.Free(pbuf)

	return err
}

// Write overwrites nbio.Conn.Write.
//
//go:norace
func (c *Conn) Write(data []byte) (int, error) {
	return -1, ErrInvalidWriteCalling
}

// EnableWriteCompression .
//
//go:norace
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
//
//go:norace
func (c *Conn) Subprotocol() string {
	return c.subprotocol
}

//go:norace
func NewClientConn(opt *Options, c net.Conn, subprotocol string, remoteCompressionEnabled bool, asyncWrite bool) *Conn {
	return newConn(opt, c, subprotocol, remoteCompressionEnabled, asyncWrite, true)
}

//go:norace
func NewServerConn(u *Upgrader, c net.Conn, subprotocol string, remoteCompressionEnabled bool, asyncWrite bool) *Conn {
	return newConn(u, c, subprotocol, remoteCompressionEnabled, asyncWrite, false)
}

//go:norace
func newConn(u *Upgrader, c net.Conn, subprotocol string, remoteCompressionEnabled bool, asyncWrite bool, isClient bool) *Conn {
	wsc := &Conn{
		commonFields:             &u.commonFields,
		Engine:                   u.Engine,
		Conn:                     c,
		subprotocol:              subprotocol,
		enableCompression:        u.enableCompression,
		remoteCompressionEnabled: remoteCompressionEnabled,
		compressionLevel:         u.compressionLevel,
		onClose:                  u.onClose,
		isClient:                 isClient,
	}
	wsc.EnableWriteCompression(remoteCompressionEnabled)
	if asyncWrite {
		wsc.sendQueue = make([]*[]byte, u.BlockingModSendQueueInitSize)[:0]
		wsc.sendQueueSize = u.BlockingModSendQueueMaxSize
		if wsc.BlockingModAsyncCloseDelay <= 0 {
			wsc.BlockingModAsyncCloseDelay = DefaultBlockingModAsyncCloseDelay
		}
	}
	return wsc
}

// HandleRead .
//
//go:norace
func (c *Conn) HandleRead(bufSize int) {
	if c.isReadingByParser {
		return
	}
	c.mux.Lock()
	reading := c.isInReadingLoop
	c.isInReadingLoop = true
	c.mux.Unlock()
	if reading {
		return
	}

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
		n, err = c.Read(buf)
		if err != nil {
			break
		}
		err = c.Parse(buf[:n])
		if err != nil {
			break
		}
	}
}

// return false if length is ok.
//
//go:norace
func (c *Conn) isMessageTooLarge(len int) bool {
	// <=0 means unlimitted size
	if c.MessageLengthLimit <= 0 {
		return false
	}
	return len > c.MessageLengthLimit
}

//go:norace
func (c *Conn) validFrame(opcode MessageType, fin, res1, res2, res3, expectingFragments bool) error {
	if res1 && !c.enableCompression {
		return ErrReserveBitSet
	}
	if res2 || res3 {
		return ErrReserveBitSet
	}
	if opcode > BinaryMessage && opcode < CloseMessage {
		return fmt.Errorf("%w: opcode=%d", ErrReservedMessageType, opcode)
	}
	if !fin && (opcode != FragmentMessage && opcode != TextMessage && opcode != BinaryMessage) {
		return fmt.Errorf("%w: opcode=%d", ErrControlMessageFragmented, opcode)
	}
	if expectingFragments && (opcode == TextMessage || opcode == BinaryMessage) {
		return ErrFragmentsShouldNotHaveBinaryOrTextMessage
	}
	return nil
}

//go:norace
func (c *Conn) readAll(r io.Reader, size int) (*[]byte, error) {
	const maxAppendSize = 1024 * 1024 * 4
	if c.MessageLengthLimit > 0 && size > c.MessageLengthLimit {
		size = c.MessageLengthLimit
	}
	pbuf := c.Engine.BodyAllocator.Malloc(size)
	*pbuf = (*pbuf)[0:0]
	for {
		n, err := r.Read((*pbuf)[len(*pbuf):cap(*pbuf)])
		if n > 0 {
			*pbuf = (*pbuf)[:len(*pbuf)+n]
		}
		if err != nil {
			if err == io.EOF {
				err = nil
			}
			return pbuf, err
		}
		if len(*pbuf) == cap(*pbuf) {
			l := len(*pbuf)
			// can not extend more bytes.
			if c.isMessageTooLarge(l + 1) {
				return nil, ErrMessageTooLarge
			}
			al := l
			if al > maxAppendSize {
				al = maxAppendSize
			}
			// extend to the limit size at most.
			if (c.MessageLengthLimit > 0) && (l+al > c.MessageLengthLimit) {
				al = c.MessageLengthLimit - l
			}
			pbuf = c.Engine.BodyAllocator.Append(pbuf, make([]byte, al)...)
			*pbuf = (*pbuf)[:l]
		}
	}
}

//go:norace
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

//go:norace
func maskXOR(b, key []byte) {
	key64 := uint64(binary.LittleEndian.Uint32(key))
	key64 |= (key64 << 32)

	for len(b) >= 64 {
		v := binary.LittleEndian.Uint64(b)
		binary.LittleEndian.PutUint64(b, v^key64)
		v = binary.LittleEndian.Uint64(b[8:16])
		binary.LittleEndian.PutUint64(b[8:16], v^key64)
		v = binary.LittleEndian.Uint64(b[16:24])
		binary.LittleEndian.PutUint64(b[16:24], v^key64)
		v = binary.LittleEndian.Uint64(b[24:32])
		binary.LittleEndian.PutUint64(b[24:32], v^key64)
		v = binary.LittleEndian.Uint64(b[32:40])
		binary.LittleEndian.PutUint64(b[32:40], v^key64)
		v = binary.LittleEndian.Uint64(b[40:48])
		binary.LittleEndian.PutUint64(b[40:48], v^key64)
		v = binary.LittleEndian.Uint64(b[48:56])
		binary.LittleEndian.PutUint64(b[48:56], v^key64)
		v = binary.LittleEndian.Uint64(b[56:64])
		binary.LittleEndian.PutUint64(b[56:64], v^key64)
		b = b[64:]
	}

	for len(b) >= 8 {
		v := binary.LittleEndian.Uint64(b[:8])
		binary.LittleEndian.PutUint64(b[:8], v^key64)
		b = b[8:]
	}

	for i := 0; i < len(b); i++ {
		idx := i & 3
		b[i] ^= key[idx]
	}
}
