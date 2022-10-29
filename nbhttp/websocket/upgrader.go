package websocket

import (
	"bytes"
	"crypto/rand"
	"crypto/sha1"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/lesismal/llib/std/crypto/tls"
	"github.com/lesismal/nbio"
	"github.com/lesismal/nbio/logging"
	"github.com/lesismal/nbio/mempool"
	"github.com/lesismal/nbio/nbhttp"
)

var (
	// DefaultBlockingReadBufferSize .
	DefaultBlockingReadBufferSize = 1024 * 4

	// DefaultBlockingModAsyncWrite represents whether create a goroutine to handle writing:
	// true : create a goroutine to recv buffers and write to conn, default is true;
	// false: write buffer to the conn directely.
	DefaultBlockingModAsyncWrite = false

	// DefaultEngine will be set to a Upgrader.Engine to handle details such as buffers.
	DefaultEngine = nbhttp.NewEngine(nbhttp.Config{
		ReleaseWebsocketPayload: true,
	})
)

// Upgrader .
type Upgrader = WebsocketReader

type WebsocketReader struct {
	ReadLimit int64
	// MessageLengthLimit is the maximum length of websocket message. 0 for unlimited.
	MessageLengthLimit int64
	HandshakeTimeout   time.Duration
	KeepaliveTime      time.Duration

	compressionLevel int
	Subprotocols     []string

	CheckOrigin func(r *http.Request) bool

	Engine *nbhttp.Engine

	BlockingModReadBufferSize int
	BlockingModAsyncWrite     bool
	isBlockingMod             bool
	enableCompression         bool
	enableWriteCompression    bool
	expectingFragments        bool
	compress                  bool
	opcode                    MessageType
	buffer                    []byte
	message                   []byte

	conn *Conn

	pingMessageHandler  func(c *Conn, appData string)
	pongMessageHandler  func(c *Conn, appData string)
	closeMessageHandler func(c *Conn, code int, text string)

	openHandler      func(*Conn)
	messageHandler   func(c *Conn, messageType MessageType, data []byte)
	dataFrameHandler func(c *Conn, messageType MessageType, fin bool, data []byte)
	onClose          func(c *Conn, err error)
}

// CompressionEnabled .
func (wr *WebsocketReader) CompressionEnabled() bool {
	return wr.compress
}

// NewUpgrader .
func NewUpgrader() *Upgrader {
	wr := &Upgrader{
		Engine: DefaultEngine,
		// BlockingModReadBufferSize: DefaultBlockingReadBufferSize,
		// BlockingModAsyncWrite:     false,
	}
	wr.pingMessageHandler = func(c *Conn, data string) {
		if len(data) > 125 {
			c.Close()
			return
		}
		err := c.WriteMessage(PongMessage, []byte(data))
		if err != nil {
			logging.Debug("failed to send pong %v", err)
			c.Close()
			return
		}
	}
	wr.pongMessageHandler = func(*Conn, string) {}
	wr.closeMessageHandler = func(c *Conn, code int, text string) {
		if len(text)+2 > maxControlFramePayloadSize {
			return //ErrInvalidControlFrame
		}
		buf := mempool.Malloc(len(text) + 2)
		binary.BigEndian.PutUint16(buf[:2], uint16(code))
		copy(buf[2:], text)
		c.WriteMessage(CloseMessage, buf)
		mempool.Free(buf)
	}
	return wr
}

// SetCloseHandler .
func (wr *WebsocketReader) SetCloseHandler(h func(*Conn, int, string)) {
	if h != nil {
		wr.closeMessageHandler = h
	}
}

// SetPingHandler .
func (wr *WebsocketReader) SetPingHandler(h func(*Conn, string)) {
	if h != nil {
		wr.pingMessageHandler = h
	}
}

// SetPongHandler .
func (wr *WebsocketReader) SetPongHandler(h func(*Conn, string)) {
	if h != nil {
		wr.pongMessageHandler = h
	}
}

// OnOpen .
func (wr *WebsocketReader) OnOpen(h func(*Conn)) {
	wr.openHandler = h
}

// OnMessage .
func (wr *WebsocketReader) OnMessage(h func(*Conn, MessageType, []byte)) {
	if h != nil {
		wr.messageHandler = func(c *Conn, messageType MessageType, data []byte) {
			if c.Engine.ReleaseWebsocketPayload && len(data) > 0 {
				defer c.Engine.BodyAllocator.Free(data)
			}
			h(c, messageType, data)
		}
	}
}

// OnDataFrame .
func (wr *WebsocketReader) OnDataFrame(h func(*Conn, MessageType, bool, []byte)) {
	if h != nil {
		wr.dataFrameHandler = func(c *Conn, messageType MessageType, fin bool, data []byte) {
			if c.Engine.ReleaseWebsocketPayload {
				defer c.Engine.BodyAllocator.Free(data)
			}
			h(c, messageType, fin, data)
		}
	}
}

// OnClose .
func (wr *WebsocketReader) OnClose(h func(*Conn, error)) {
	wr.onClose = h
}

// EnableCompression .
func (wr *WebsocketReader) EnableCompression(enable bool) {
	wr.enableCompression = enable
}

// EnableWriteCompression .
func (wr *WebsocketReader) EnableWriteCompression(enable bool) {
	wr.enableWriteCompression = enable
}

// SetCompressionLevel .
func (wr *WebsocketReader) SetCompressionLevel(level int) error {
	wr.compressionLevel = level
	return nil
}

// Upgrade .
func (wr *WebsocketReader) Upgrade(w http.ResponseWriter, r *http.Request, responseHeader http.Header) (net.Conn, error) {
	if !headerContains(r.Header, "Connection", "upgrade") {
		return nil, wr.returnError(w, r, http.StatusBadRequest, ErrUpgradeTokenNotFound)
	}

	if !headerContains(r.Header, "Upgrade", "websocket") {
		return nil, wr.returnError(w, r, http.StatusBadRequest, ErrUpgradeTokenNotFound)
	}

	if r.Method != "GET" {
		return nil, wr.returnError(w, r, http.StatusMethodNotAllowed, ErrUpgradeMethodIsGet)
	}

	if !headerContains(r.Header, "Sec-Websocket-Version", "13") {
		return nil, wr.returnError(w, r, http.StatusBadRequest, ErrUpgradeInvalidWebsocketVersion)
	}

	if _, ok := responseHeader["Sec-Websocket-Extensions"]; ok {
		return nil, wr.returnError(w, r, http.StatusInternalServerError, ErrUpgradeUnsupportedExtensions)
	}

	checkOrigin := wr.CheckOrigin
	if checkOrigin == nil {
		checkOrigin = checkSameOrigin
	}
	if !checkOrigin(r) {
		return nil, wr.returnError(w, r, http.StatusForbidden, ErrUpgradeOriginNotAllowed)
	}

	challengeKey := r.Header.Get("Sec-Websocket-Key")
	if challengeKey == "" {
		return nil, wr.returnError(w, r, http.StatusBadRequest, ErrUpgradeMissingWebsocketKey)
	}

	subprotocol := wr.selectSubprotocol(r, responseHeader)

	// Negotiate PMCE
	var compress bool
	if wr.enableCompression {
		for _, ext := range parseExtensions(r.Header) {
			if ext[""] != "permessage-deflate" {
				continue
			}
			compress = true
			break
		}
	}

	h, ok := w.(http.Hijacker)
	if !ok {
		return nil, wr.returnError(w, r, http.StatusInternalServerError, ErrUpgradeNotHijacker)
	}
	conn, _, err := h.Hijack()
	if err != nil {
		return nil, wr.returnError(w, r, http.StatusInternalServerError, err)
	}

	var parser *nbhttp.Parser
	switch vt := conn.(type) {
	case *nbio.Conn:
		parser, ok = vt.Session().(*nbhttp.Parser)
		if !ok {
			return nil, wr.returnError(w, r, http.StatusInternalServerError, err)
		}
		parser.Reader = wr
		wr.conn = newConn(wr, conn, subprotocol, compress)
		wr.conn.Engine = parser.Engine
		wr.Engine = parser.Engine
	case *tls.Conn:
		nbc, ok := vt.Conn().(*nbio.Conn)
		if !ok {
			parser, ok = vt.Session().(*nbhttp.Parser)
			if !ok {
				return nil, wr.returnError(w, r, http.StatusInternalServerError, err)
			}
			parser.Reader = wr
			wr.conn = newConn(wr, conn, subprotocol, compress)
			wr.conn.Engine = wr.Engine
			wr.isBlockingMod = true
			if wr.BlockingModAsyncWrite {
				wr.conn.chAsyncWrite = make(chan []byte, 4096)
			}

		} else {
			parser, ok = nbc.Session().(*nbhttp.Parser)
			if !ok {
				return nil, wr.returnError(w, r, http.StatusInternalServerError, err)
			}
			parser.Reader = wr
			wr.conn = newConn(wr, conn, subprotocol, compress)
			wr.conn.Engine = parser.Engine
			wr.Engine = parser.Engine
		}
	default:
		nbResonse, ok := w.(*nbhttp.Response)
		if ok {
			parser = nbResonse.Parser
			parser.Reader = wr
		}
		wr.conn = newConn(wr, conn, subprotocol, compress)
		wr.conn.Engine = wr.Engine
		wr.isBlockingMod = true
		if wr.BlockingModAsyncWrite {
			wr.conn.chAsyncWrite = make(chan []byte, 4096)
		}
	}

	buf := mempool.Malloc(1024)[0:0]
	buf = mempool.AppendString(buf, "HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Accept: ")
	buf = mempool.Append(buf, acceptKeyBytes(challengeKey)...)
	buf = mempool.AppendString(buf, "\r\n")
	if subprotocol != "" {
		buf = mempool.AppendString(buf, "Sec-WebSocket-Protocol: ")
		buf = mempool.AppendString(buf, subprotocol)
		buf = mempool.AppendString(buf, "\r\n")
	}
	if compress {
		buf = mempool.AppendString(buf, "Sec-WebSocket-Extensions: permessage-deflate; server_no_context_takeover; client_no_context_takeover\r\n")
	}
	for k, vs := range responseHeader {
		if k == "Sec-Websocket-Protocol" {
			continue
		}
		for _, v := range vs {
			buf = mempool.AppendString(buf, k)
			buf = mempool.AppendString(buf, ": ")
			for i := 0; i < len(v); i++ {
				b := v[i]
				if b <= 31 {
					// prevent response splitting.
					b = ' '
				}
				buf = mempool.Append(buf, b)
			}
			buf = mempool.AppendString(buf, "\r\n")
		}
	}
	buf = mempool.AppendString(buf, "\r\n")

	if wr.HandshakeTimeout > 0 {
		conn.SetWriteDeadline(time.Now().Add(wr.HandshakeTimeout))
	}

	_, err = conn.Write(buf)
	mempool.Free(buf)
	if err != nil {
		conn.Close()
		return nil, err
	}

	if wr.KeepaliveTime <= 0 {
		conn.SetReadDeadline(time.Now().Add(nbhttp.DefaultKeepaliveTime))
	} else {
		conn.SetReadDeadline(time.Now().Add(wr.KeepaliveTime))
	}

	if wr.openHandler != nil {
		wr.openHandler(wr.conn)
	}

	wr.conn.OnClose(wr.onClose)

	if wr.isBlockingMod {
		if wr.BlockingModAsyncWrite {
			go wr.blockingModWriteLoop()
		}
		if parser == nil {
			go wr.blockingModReadLoop()
		}
	}

	return wr.conn, nil
}

func (wr *WebsocketReader) blockingModReadLoop() {
	conn := wr.conn
	bufSize := wr.BlockingModReadBufferSize
	if bufSize <= 0 {
		bufSize = DefaultBlockingReadBufferSize
	}
	buf := make([]byte, bufSize)

	defer func() {
		conn.Close()
		if wr.BlockingModAsyncWrite && conn.chAsyncWrite != nil {
			close(conn.chAsyncWrite)
		}
	}()

	for {
		n, err := conn.Read(buf)
		if err != nil {
			conn.onClose(conn, err)
			break
		}
		err = wr.Read(nil, buf[:n])
		if err != nil {
			conn.onClose(conn, err)
			break
		}
	}
}

func (wr *WebsocketReader) blockingModWriteLoop() {
	conn := wr.conn
	defer conn.Close()

	for data := range conn.chAsyncWrite {
		_, err := conn.Conn.Write(data)
		mempool.Free(data)
		if err != nil {
			break
		}
	}
}

func (wr *WebsocketReader) validFrame(opcode MessageType, fin, res1, res2, res3, expectingFragments bool) error {
	if res1 && !wr.enableCompression {
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

// return false if length is ok.
func (wr *WebsocketReader) isMessageTooLarge(len int) bool {
	if wr.MessageLengthLimit == 0 {
		// 0 means unlimitted size
		return false
	}
	return len > int(wr.MessageLengthLimit)
}

// Read .
func (wr *WebsocketReader) Read(p *nbhttp.Parser, data []byte) error {
	oldLen := len(wr.buffer)
	if wr.ReadLimit > 0 && (int64(oldLen+len(data)) > wr.ReadLimit || int64(oldLen+len(wr.message)) > wr.ReadLimit) {
		return nbhttp.ErrTooLong
	}

	var oldBuffer []byte
	if oldLen == 0 {
		wr.buffer = data
	} else {
		wr.buffer = mempool.Append(wr.buffer, data...)
		oldBuffer = wr.buffer
	}

	var err error
	for i := 0; true; i++ {
		opcode, body, ok, fin, res1, res2, res3 := wr.nextFrame()
		if !ok {
			break
		}
		if err = wr.validFrame(opcode, fin, res1, res2, res3, wr.expectingFragments); err != nil {
			break
		}
		if opcode == FragmentMessage || opcode == TextMessage || opcode == BinaryMessage {
			if wr.opcode == 0 {
				wr.opcode = opcode
				wr.compress = res1
			}
			bl := len(body)
			if wr.dataFrameHandler != nil {
				var frame []byte
				if bl > 0 {
					if wr.isMessageTooLarge(bl) {
						err = ErrMessageTooLarge
						break
					}
					frame = wr.Engine.BodyAllocator.Malloc(bl)
					copy(frame, body)
				}
				if wr.opcode == TextMessage && len(frame) > 0 && !wr.Engine.CheckUtf8(frame) {
					wr.conn.Close()
				} else {
					wr.handleDataFrame(p, wr.conn, wr.opcode, fin, frame)
				}
			}
			if bl > 0 && wr.messageHandler != nil {
				if wr.message == nil {
					if wr.isMessageTooLarge(len(body)) {
						err = ErrMessageTooLarge
						break
					}
					wr.message = wr.Engine.BodyAllocator.Malloc(len(body))
					copy(wr.message, body)
				} else {
					if wr.isMessageTooLarge(len(wr.message) + len(body)) {
						err = ErrMessageTooLarge
						break
					}
					wr.message = wr.Engine.BodyAllocator.Append(wr.message, body...)
				}
			}
			if fin {
				if wr.messageHandler != nil {
					if wr.compress {
						var b []byte
						rc := decompressReader(io.MultiReader(bytes.NewBuffer(wr.message), strings.NewReader(flateReaderTail)))
						b, err = wr.readAll(rc, len(wr.message)*2)
						wr.Engine.BodyAllocator.Free(wr.message)
						wr.message = b
						rc.Close()
						if err != nil {
							break
						}
					}
					wr.handleMessage(p, wr.opcode, wr.message)
				}
				wr.compress = false
				wr.expectingFragments = false
				wr.message = nil
				wr.opcode = 0
			} else {
				wr.expectingFragments = true
			}
		} else {
			var frame []byte
			if len(body) > 0 {
				if wr.isMessageTooLarge(len(body)) {
					err = ErrMessageTooLarge
					break
				}
				frame = wr.Engine.BodyAllocator.Malloc(len(body))
				copy(frame, body)
			}
			wr.handleProtocolMessage(p, opcode, frame)
		}

		if len(wr.buffer) == 0 {
			break
		}
	}

	if oldLen == 0 {
		if len(wr.buffer) > 0 {
			tmp := wr.buffer
			wr.buffer = mempool.Malloc(len(tmp))
			copy(wr.buffer, tmp)
		} else {
			wr.buffer = nil
		}
	} else {
		if len(wr.buffer) == 0 {
			mempool.Free(oldBuffer)
			wr.buffer = nil
		} else if len(wr.buffer) < len(oldBuffer) {
			tmp := mempool.Malloc(len(wr.buffer))
			copy(tmp, wr.buffer)
			wr.buffer = tmp
			mempool.Free(oldBuffer)
		}
	}

	return err
}

// Close .
func (wr *WebsocketReader) Close(p *nbhttp.Parser, err error) {
	if wr.conn != nil {
		wr.conn.onClose(wr.conn, err)
		wr.conn.Close()
	}
	if wr.buffer != nil {
		mempool.Free(wr.buffer)
		wr.buffer = nil
	}
	if wr.message != nil {
		mempool.Free(wr.message)
		wr.message = nil
	}
}

func (wr *WebsocketReader) handleDataFrame(p *nbhttp.Parser, c *Conn, opcode MessageType, fin bool, data []byte) {
	h := wr.dataFrameHandler
	if wr.isBlockingMod {
		h(c, opcode, fin, data)
	} else {
		p.Execute(func() {
			h(c, opcode, fin, data)
		})
	}
}

func (wr *WebsocketReader) handleMessage(p *nbhttp.Parser, opcode MessageType, body []byte) {
	if wr.isBlockingMod {
		wr.handleWsMessage(wr.conn, opcode, body)
	} else {
		if !p.Execute(func() {
			wr.handleWsMessage(wr.conn, opcode, body)
		}) {
			if len(body) > 0 {
				wr.Engine.BodyAllocator.Free(body)
			}
		}
	}
}

func (wr *WebsocketReader) handleProtocolMessage(p *nbhttp.Parser, opcode MessageType, body []byte) {
	if wr.isBlockingMod {
		wr.handleWsMessage(wr.conn, opcode, body)
		if len(body) > 0 && wr.Engine.ReleaseWebsocketPayload {
			wr.Engine.BodyAllocator.Free(body)
		}
	} else {
		if !p.Execute(func() {
			wr.handleWsMessage(wr.conn, opcode, body)
			if len(body) > 0 && wr.Engine.ReleaseWebsocketPayload {
				wr.Engine.BodyAllocator.Free(body)
			}
		}) {
			if len(body) > 0 {
				wr.Engine.BodyAllocator.Free(body)
			}
		}
	}
}

func (wr *WebsocketReader) handleWsMessage(c *Conn, opcode MessageType, data []byte) {
	if wr.KeepaliveTime > 0 {
		defer c.SetReadDeadline(time.Now().Add(wr.KeepaliveTime))
	}
	switch opcode {
	case BinaryMessage:
		wr.messageHandler(c, opcode, data)
	case TextMessage:
		if !c.Engine.CheckUtf8(data) {
			const errText = "Invalid UTF-8 bytes"
			protoErrorData := make([]byte, 2+len(errText))
			binary.BigEndian.PutUint16(protoErrorData, 1002)
			copy(protoErrorData[2:], errText)
			c.WriteMessage(CloseMessage, protoErrorData)
			return
		}
		wr.messageHandler(c, opcode, data)
	case CloseMessage:
		if len(data) >= 2 {
			code := int(binary.BigEndian.Uint16(data[:2]))
			if !validCloseCode(code) || !c.Engine.CheckUtf8(data[2:]) {
				protoErrorCode := make([]byte, 2)
				binary.BigEndian.PutUint16(protoErrorCode, 1002)
				c.WriteMessage(CloseMessage, protoErrorCode)
			} else {
				wr.closeMessageHandler(c, code, string(data[2:]))
			}
		} else {
			c.WriteMessage(CloseMessage, nil)
		}
		// close immediately, no need to wait for data flushed on a blocked conn
		c.Close()
	case PingMessage:
		wr.pingMessageHandler(c, string(data))
	case PongMessage:
		wr.pongMessageHandler(c, string(data))
	case FragmentMessage:
		logging.Debug("invalid fragment message")
		c.Close()
	default:
		c.Close()
	}
}

func (wr *WebsocketReader) nextFrame() (opcode MessageType, body []byte, ok, fin, res1, res2, res3 bool) {
	l := int64(len(wr.buffer))
	headLen := int64(2)
	if l >= 2 {
		opcode = MessageType(wr.buffer[0] & 0xF)
		res1 = int8(wr.buffer[0]&0x40) != 0
		res2 = int8(wr.buffer[0]&0x20) != 0
		res3 = int8(wr.buffer[0]&0x10) != 0
		fin = ((wr.buffer[0] & 0x80) != 0)
		payloadLen := wr.buffer[1] & 0x7F
		bodyLen := int64(-1)

		switch payloadLen {
		case 126:
			if l >= 4 {
				bodyLen = int64(binary.BigEndian.Uint16(wr.buffer[2:4]))
				headLen = 4
			}
		case 127:
			if len(wr.buffer) >= 10 {
				bodyLen = int64(binary.BigEndian.Uint64(wr.buffer[2:10]))
				headLen = 10
			}
		default:
			bodyLen = int64(payloadLen)
		}
		if bodyLen >= 0 {
			masked := (wr.buffer[1] & 0x80) != 0
			if masked {
				headLen += 4
			}
			total := headLen + bodyLen
			if l >= total {
				body = wr.buffer[headLen:total]
				if masked {
					maskKey := wr.buffer[headLen-4 : headLen]
					for i := 0; i < len(body); i++ {
						body[i] ^= maskKey[i%4]
					}
				}

				ok = true
				wr.buffer = wr.buffer[total:l]
			}
		}
	}

	return opcode, body, ok, fin, res1, res2, res3
}

func (wr *WebsocketReader) returnError(w http.ResponseWriter, _ *http.Request, status int, err error) error {
	w.Header().Set("Sec-Websocket-Version", "13")
	http.Error(w, http.StatusText(status), status)
	return err
}

func (wr *WebsocketReader) selectSubprotocol(r *http.Request, responseHeader http.Header) string {
	if wr.Subprotocols != nil {
		clientProtocols := subprotocols(r)
		for _, serverProtocol := range wr.Subprotocols {
			for _, clientProtocol := range clientProtocols {
				if clientProtocol == serverProtocol {
					return clientProtocol
				}
			}
		}
	} else if responseHeader != nil {
		return responseHeader.Get("Sec-Websocket-Protocol")
	}
	return ""
}

func subprotocols(r *http.Request) []string {
	h := strings.TrimSpace(r.Header.Get("Sec-Websocket-Protocol"))
	if h == "" {
		return nil
	}
	protocols := strings.Split(h, ",")
	for i := range protocols {
		protocols[i] = strings.TrimSpace(protocols[i])
	}
	return protocols
}

var keyGUID = []byte("258EAFA5-E914-47DA-95CA-C5AB0DC85B11")

func acceptKeyString(challengeKey string) string {
	h := sha1.New() //nolint:gosec // per websocket protocol spec
	h.Write([]byte(challengeKey))
	h.Write(keyGUID)
	return base64.StdEncoding.EncodeToString(h.Sum(nil))
}

func acceptKeyBytes(challengeKey string) []byte {
	h := sha1.New() //nolint:gosec // per websocket protocol spec
	h.Write([]byte(challengeKey))
	h.Write(keyGUID)
	sum := h.Sum(nil)
	buf := make([]byte, base64.StdEncoding.EncodedLen(len(sum)))
	base64.StdEncoding.Encode(buf, sum)
	return buf
}

func challengeKey() (string, error) {
	p := make([]byte, 16)
	if _, err := io.ReadFull(rand.Reader, p); err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(p), nil
}

func checkSameOrigin(r *http.Request) bool {
	origin := r.Header["Origin"]
	if len(origin) == 0 {
		return true
	}
	u, err := url.Parse(origin[0])
	if err != nil {
		return false
	}
	return equalASCIIFold(u.Host, r.Host)
}

func headerContains(header http.Header, name string, value string) bool {
	var t string
	values := header[name]
headers:
	for _, s := range values {
		for {
			t, s = nextToken(skipSpace(s))
			if t == "" {
				continue headers
			}
			s = skipSpace(s)
			if s != "" && s[0] != ',' {
				continue headers
			}
			if equalASCIIFold(t, value) {
				return true
			}
			if s == "" {
				continue headers
			}
			s = s[1:]
		}
	}
	return false
}

func equalASCIIFold(s, t string) bool {
	for s != "" && t != "" {
		sr, size := utf8.DecodeRuneInString(s)
		s = s[size:]
		tr, size := utf8.DecodeRuneInString(t)
		t = t[size:]
		if sr == tr {
			continue
		}
		if 'A' <= sr && sr <= 'Z' {
			sr = sr + 'a' - 'A'
		}
		if 'A' <= tr && tr <= 'Z' {
			tr = tr + 'a' - 'A'
		}
		if sr != tr {
			return false
		}
	}
	return s == t
}

func parseExtensions(header http.Header) []map[string]string {
	var result []map[string]string
headers:
	for _, s := range header["Sec-Websocket-Extensions"] {
		for {
			var t string
			t, s = nextToken(skipSpace(s))
			if t == "" {
				continue headers
			}
			ext := map[string]string{"": t}
			for {
				s = skipSpace(s)
				if !strings.HasPrefix(s, ";") {
					break
				}
				var k string
				k, s = nextToken(skipSpace(s[1:]))
				if k == "" {
					continue headers
				}
				s = skipSpace(s)
				var v string
				if strings.HasPrefix(s, "=") {
					v, s = nextTokenOrQuoted(skipSpace(s[1:]))
					s = skipSpace(s)
				}
				if s != "" && s[0] != ',' && s[0] != ';' {
					continue headers
				}
				ext[k] = v
			}
			if s != "" && s[0] != ',' {
				continue headers
			}
			result = append(result, ext)
			if s == "" {
				continue headers
			}
			s = s[1:]
		}
	}
	return result
}

var isTokenOctet = [256]bool{
	'!':  true,
	'#':  true,
	'$':  true,
	'%':  true,
	'&':  true,
	'\'': true,
	'*':  true,
	'+':  true,
	'-':  true,
	'.':  true,
	'0':  true,
	'1':  true,
	'2':  true,
	'3':  true,
	'4':  true,
	'5':  true,
	'6':  true,
	'7':  true,
	'8':  true,
	'9':  true,
	'A':  true,
	'B':  true,
	'C':  true,
	'D':  true,
	'E':  true,
	'F':  true,
	'G':  true,
	'H':  true,
	'I':  true,
	'J':  true,
	'K':  true,
	'L':  true,
	'M':  true,
	'N':  true,
	'O':  true,
	'P':  true,
	'Q':  true,
	'R':  true,
	'S':  true,
	'T':  true,
	'U':  true,
	'W':  true,
	'V':  true,
	'X':  true,
	'Y':  true,
	'Z':  true,
	'^':  true,
	'_':  true,
	'`':  true,
	'a':  true,
	'b':  true,
	'c':  true,
	'd':  true,
	'e':  true,
	'f':  true,
	'g':  true,
	'h':  true,
	'i':  true,
	'j':  true,
	'k':  true,
	'l':  true,
	'm':  true,
	'n':  true,
	'o':  true,
	'p':  true,
	'q':  true,
	'r':  true,
	's':  true,
	't':  true,
	'u':  true,
	'v':  true,
	'w':  true,
	'x':  true,
	'y':  true,
	'z':  true,
	'|':  true,
	'~':  true,
}

func skipSpace(s string) (rest string) {
	i := 0
	for ; i < len(s); i++ {
		if b := s[i]; b != ' ' && b != '\t' {
			break
		}
	}
	return s[i:]
}

func nextToken(s string) (token, rest string) {
	i := 0
	for ; i < len(s); i++ {
		if !isTokenOctet[s[i]] {
			break
		}
	}
	return s[:i], s[i:]
}

func nextTokenOrQuoted(s string) (value string, rest string) {
	if !strings.HasPrefix(s, "\"") {
		return nextToken(s)
	}
	s = s[1:]
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '"':
			return s[:i], s[i+1:]
		case '\\':
			p := make([]byte, len(s)-1)
			j := copy(p, s[:i])
			escape := true
			for i = i + 1; i < len(s); i++ {
				b := s[i]
				switch {
				case escape:
					escape = false
					p[j] = b
					j++
				case b == '\\':
					escape = true
				case b == '"':
					return string(p[:j]), s[i+1:]
				default:
					p[j] = b
					j++
				}
			}
			return "", ""
		}
	}
	return "", ""
}

func (wr *WebsocketReader) readAll(r io.Reader, size int) ([]byte, error) {
	const maxAppendSize = 1024 * 1024 * 4
	buf := wr.Engine.BodyAllocator.Malloc(size)[0:0]
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
			buf = wr.Engine.BodyAllocator.Append(buf, make([]byte, al)...)[:l]
		}
	}
}
