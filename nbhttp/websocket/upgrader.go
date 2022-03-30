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

// Hijacker .
type Hijacker interface {
	Hijack() (net.Conn, error)
}

// Upgrader .
type Upgrader struct {
	ReadLimit int64
	// MessageLengthLimit is the maximum length of websocket message. 0 for unlimited.
	MessageLengthLimit int64
	HandshakeTimeout   time.Duration

	enableCompression      bool
	enableWriteCompression bool
	compressionLevel       int
	Subprotocols           []string

	CheckOrigin func(r *http.Request) bool

	pingMessageHandler  func(c *Conn, appData string)
	pongMessageHandler  func(c *Conn, appData string)
	closeMessageHandler func(c *Conn, code int, text string)

	openHandler      func(*Conn)
	messageHandler   func(c *Conn, messageType MessageType, data []byte)
	dataFrameHandler func(c *Conn, messageType MessageType, fin bool, data []byte)
	onClose          func(c *Conn, err error)
}
type connState struct {
	common             *Upgrader
	conn               *Conn
	expectingFragments bool
	compress           bool
	opcode             MessageType
	buffer             []byte
	message            []byte
	Engine             *nbhttp.Engine
}

// CompressionEnabled .
func (u *connState) CompressionEnabled() bool {
	return u.compress
}

// NewUpgrader .
func NewUpgrader() *Upgrader {
	u := &Upgrader{}
	u.pingMessageHandler = func(c *Conn, data string) {
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
	u.pongMessageHandler = func(*Conn, string) {}
	u.closeMessageHandler = func(c *Conn, code int, text string) {
		if len(text)+2 > maxControlFramePayloadSize {
			return //ErrInvalidControlFrame
		}
		buf := mempool.Malloc(len(text) + 2)
		binary.BigEndian.PutUint16(buf[:2], uint16(code))
		copy(buf[2:], text)
		c.WriteMessage(CloseMessage, buf)
		mempool.Free(buf)
	}
	return u
}

// SetCloseHandler .
func (u *Upgrader) SetCloseHandler(h func(*Conn, int, string)) {
	if h != nil {
		u.closeMessageHandler = h
	}
}

// SetPingHandler .
func (u *Upgrader) SetPingHandler(h func(*Conn, string)) {
	if h != nil {
		u.pingMessageHandler = h
	}
}

// SetPongHandler .
func (u *Upgrader) SetPongHandler(h func(*Conn, string)) {
	if h != nil {
		u.pongMessageHandler = h
	}
}

// OnOpen .
func (u *Upgrader) OnOpen(h func(*Conn)) {
	u.openHandler = h
}

// OnMessage .
func (u *Upgrader) OnMessage(h func(*Conn, MessageType, []byte)) {
	if h != nil {
		u.messageHandler = func(c *Conn, messageType MessageType, data []byte) {
			if c.Engine.ReleaseWebsocketPayload {
				if len(data) > 0 {
					defer c.Engine.BodyAllocator.Free(data)
				}
			}
			h(c, messageType, data)
		}
	}
}

// OnDataFrame .
func (u *Upgrader) OnDataFrame(h func(*Conn, MessageType, bool, []byte)) {
	if h != nil {
		u.dataFrameHandler = func(c *Conn, messageType MessageType, fin bool, data []byte) {
			if c.Engine.ReleaseWebsocketPayload {
				defer c.Engine.BodyAllocator.Free(data)
			}
			h(c, messageType, fin, data)
		}
	}
}

// OnClose .
func (u *Upgrader) OnClose(h func(*Conn, error)) {
	u.onClose = h
}

// EnableCompression .
func (u *Upgrader) EnableCompression(enable bool) {
	u.enableCompression = enable
}

// EnableWriteCompression .
func (u *Upgrader) EnableWriteCompression(enable bool) {
	u.enableWriteCompression = enable
}

// SetCompressionLevel .
func (u *Upgrader) SetCompressionLevel(level int) error {
	u.compressionLevel = level
	return nil
}

// Upgrade .
func (u *Upgrader) Upgrade(w http.ResponseWriter, r *http.Request, responseHeader http.Header) (net.Conn, error) {
	if !headerContains(r.Header, "Connection", "upgrade") {
		return nil, u.returnError(w, r, http.StatusBadRequest, ErrUpgradeTokenNotFound)
	}

	if !headerContains(r.Header, "Upgrade", "websocket") {
		return nil, u.returnError(w, r, http.StatusBadRequest, ErrUpgradeTokenNotFound)
	}

	if r.Method != "GET" {
		return nil, u.returnError(w, r, http.StatusMethodNotAllowed, ErrUpgradeMethodIsGet)
	}

	if !headerContains(r.Header, "Sec-Websocket-Version", "13") {
		return nil, u.returnError(w, r, http.StatusBadRequest, ErrUpgradeInvalidWebsocketVersion)
	}

	if _, ok := responseHeader["Sec-Websocket-Extensions"]; ok {
		return nil, u.returnError(w, r, http.StatusInternalServerError, ErrUpgradeUnsupportedExtensions)
	}

	checkOrigin := u.CheckOrigin
	if checkOrigin == nil {
		checkOrigin = checkSameOrigin
	}
	if !checkOrigin(r) {
		return nil, u.returnError(w, r, http.StatusForbidden, ErrUpgradeOriginNotAllowed)
	}

	challengeKey := r.Header.Get("Sec-Websocket-Key")
	if challengeKey == "" {
		return nil, u.returnError(w, r, http.StatusBadRequest, ErrUpgradeMissingWebsocketKey)
	}

	subprotocol := u.selectSubprotocol(r, responseHeader)

	// Negotiate PMCE
	var compress bool
	if u.enableCompression {
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
		return nil, u.returnError(w, r, http.StatusInternalServerError, ErrUpgradeNotHijacker)
	}
	conn, _, err := h.Hijack()
	if err != nil {
		return nil, u.returnError(w, r, http.StatusInternalServerError, err)
	}

	nbc, ok := conn.(*nbio.Conn)
	if !ok {
		tlsConn, tlsOk := conn.(*tls.Conn)
		if !tlsOk {
			return nil, u.returnError(w, r, http.StatusInternalServerError, err)
		}
		nbc, tlsOk = tlsConn.Conn().(*nbio.Conn)
		if !tlsOk {
			return nil, u.returnError(w, r, http.StatusInternalServerError, err)
		}
	}

	parser, ok := nbc.Session().(*nbhttp.Parser)
	if !ok {
		return nil, u.returnError(w, r, http.StatusInternalServerError, err)
	}

	state := &connState{common: u}
	parser.ConnState = state

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

	if u.HandshakeTimeout > 0 {
		conn.SetWriteDeadline(time.Now().Add(u.HandshakeTimeout))
	}

	state.conn = newConn(u, conn, subprotocol, compress)
	state.Engine = parser.Engine
	state.conn.Engine = parser.Engine

	if u.openHandler != nil {
		u.openHandler(state.conn)
	}

	_, err = conn.Write(buf)
	mempool.Free(buf)
	if err != nil {
		conn.Close()
		return nil, err
	}

	state.conn.OnClose(u.onClose)

	return state.conn, nil
}

func (u *connState) validFrame(opcode MessageType, fin, res1, res2, res3, expectingFragments bool) error {
	if res1 && !u.common.enableCompression {
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
func (u *connState) isMessageTooLarge(len int) bool {
	if u.common.MessageLengthLimit == 0 {
		// 0 means unlimitted size
		return false
	}
	return len > int(u.common.MessageLengthLimit)
}

// Read .
func (u *connState) Read(p *nbhttp.Parser, data []byte) error {
	oldLen := len(u.buffer)
	if u.common.ReadLimit > 0 && (int64(oldLen+len(data)) > u.common.ReadLimit || int64(oldLen+len(u.message)) > u.common.ReadLimit) {
		return nbhttp.ErrTooLong
	}

	var oldBuffer []byte
	if oldLen == 0 {
		u.buffer = data
	} else {
		u.buffer = mempool.Append(u.buffer, data...)
		oldBuffer = u.buffer
	}

	var err error
	for i := 0; true; i++ {
		opcode, body, ok, fin, res1, res2, res3 := u.nextFrame()
		if !ok {
			break
		}
		if err = u.validFrame(opcode, fin, res1, res2, res3, u.expectingFragments); err != nil {
			break
		}
		if opcode == FragmentMessage || opcode == TextMessage || opcode == BinaryMessage {
			if u.opcode == 0 {
				u.opcode = opcode
				u.compress = res1
			}
			bl := len(body)
			if u.common.dataFrameHandler != nil {
				var frame []byte
				if bl > 0 {
					if u.isMessageTooLarge(bl) {
						err = ErrMessageTooLarge
						break
					}
					frame = u.Engine.BodyAllocator.Malloc(bl)
					copy(frame, body)
				}
				if u.opcode == TextMessage && len(frame) > 0 && !u.Engine.CheckUtf8(frame) {
					u.conn.Close()
				} else {
					u.common.handleDataFrame(p, u.conn, u.opcode, fin, frame)
				}
			}
			if bl > 0 && u.common.messageHandler != nil {
				if u.message == nil {
					if u.isMessageTooLarge(len(body)) {
						err = ErrMessageTooLarge
						break
					}
					u.message = u.Engine.BodyAllocator.Malloc(len(body))
					copy(u.message, body)
				} else {
					if u.isMessageTooLarge(len(u.message) + len(body)) {
						err = ErrMessageTooLarge
						break
					}
					u.message = u.Engine.BodyAllocator.Append(u.message, body...)
				}
			}
			if fin {
				if u.common.messageHandler != nil {
					if u.compress {
						var b []byte
						rc := decompressReader(io.MultiReader(bytes.NewBuffer(u.message), strings.NewReader(flateReaderTail)))
						b, err = u.readAll(rc, len(u.message)*2)
						u.Engine.BodyAllocator.Free(u.message)
						u.message = b
						rc.Close()
						if err != nil {
							break
						}
					}
					u.handleMessage(p, u.opcode, u.message)
				}
				u.compress = false
				u.expectingFragments = false
				u.message = nil
				u.opcode = 0
			} else {
				u.expectingFragments = true
			}
		} else {
			var frame []byte
			if len(body) > 0 {
				if u.isMessageTooLarge(len(body)) {
					err = ErrMessageTooLarge
					break
				}
				frame = u.Engine.BodyAllocator.Malloc(len(body))
				copy(frame, body)
			}
			u.handleProtocolMessage(p, opcode, frame)
		}

		if len(u.buffer) == 0 {
			break
		}
	}

	if oldLen == 0 {
		if len(u.buffer) > 0 {
			tmp := u.buffer
			u.buffer = mempool.Malloc(len(tmp))
			copy(u.buffer, tmp)
		} else {
			u.buffer = nil
		}
	} else {
		if len(u.buffer) == 0 {
			mempool.Free(oldBuffer)
			u.buffer = nil
		} else if len(u.buffer) < len(oldBuffer) {
			tmp := mempool.Malloc(len(u.buffer))
			copy(tmp, u.buffer)
			u.buffer = tmp
			mempool.Free(oldBuffer)
		}
	}

	return err
}

// Close .
func (u *connState) Close(p *nbhttp.Parser, err error) {
	if u.conn != nil {
		u.conn.onClose(u.conn, err)
		u.conn.Close()
	}
	if u.buffer != nil {
		mempool.Free(u.buffer)
		u.buffer = nil
	}
	if u.message != nil {
		mempool.Free(u.message)
		u.message = nil
	}
}

func (u *Upgrader) handleDataFrame(p *nbhttp.Parser, c *Conn, opcode MessageType, fin bool, data []byte) {
	h := u.dataFrameHandler
	p.Execute(func() {
		h(c, opcode, fin, data)
	})
}

func (u *connState) handleMessage(p *nbhttp.Parser, opcode MessageType, body []byte) {
	if u.opcode == TextMessage && !u.Engine.CheckUtf8(u.message) {
		u.conn.Close()
		return
	}

	p.Execute(func() {
		u.common.handleWsMessage(u.conn, opcode, body)
	})

}

func (u *connState) handleProtocolMessage(p *nbhttp.Parser, opcode MessageType, body []byte) {
	p.Execute(func() {
		u.common.handleWsMessage(u.conn, opcode, body)
		if len(body) > 0 && u.Engine.ReleaseWebsocketPayload {
			u.Engine.BodyAllocator.Free(body)
		}
	})
}

func (u *Upgrader) handleWsMessage(c *Conn, opcode MessageType, data []byte) {
	switch opcode {
	case TextMessage, BinaryMessage:
		u.messageHandler(c, opcode, data)
	case CloseMessage:
		if len(data) >= 2 {
			code := int(binary.BigEndian.Uint16(data[:2]))
			if !validCloseCode(code) || !c.Engine.CheckUtf8(data[2:]) {
				protoErrorCode := make([]byte, 2)
				binary.BigEndian.PutUint16(protoErrorCode, 1002)
				c.WriteMessage(CloseMessage, protoErrorCode)
			} else {
				u.closeMessageHandler(c, code, string(data[2:]))
			}
		} else {
			c.WriteMessage(CloseMessage, nil)
		}
		// close immediately, no need to wait for data flushed on a blocked conn
		c.Close()
	case PingMessage:
		u.pingMessageHandler(c, string(data))
	case PongMessage:
		u.pongMessageHandler(c, string(data))
	case FragmentMessage:
		logging.Debug("invalid fragment message")
		c.Close()
	default:
		c.Close()
	}
}

func (u *connState) nextFrame() (opcode MessageType, body []byte, ok, fin, res1, res2, res3 bool) {
	l := int64(len(u.buffer))
	headLen := int64(2)
	if l >= 2 {
		opcode = MessageType(u.buffer[0] & 0xF)
		res1 = int8(u.buffer[0]&0x40) != 0
		res2 = int8(u.buffer[0]&0x20) != 0
		res3 = int8(u.buffer[0]&0x10) != 0
		fin = ((u.buffer[0] & 0x80) != 0)
		payloadLen := u.buffer[1] & 0x7F
		bodyLen := int64(-1)

		switch payloadLen {
		case 126:
			if l >= 4 {
				bodyLen = int64(binary.BigEndian.Uint16(u.buffer[2:4]))
				headLen = 4
			}
		case 127:
			if len(u.buffer) >= 10 {
				bodyLen = int64(binary.BigEndian.Uint64(u.buffer[2:10]))
				headLen = 10
			}
		default:
			bodyLen = int64(payloadLen)
		}
		if bodyLen >= 0 {
			masked := (u.buffer[1] & 0x80) != 0
			if masked {
				headLen += 4
			}
			total := headLen + bodyLen
			if l >= total {
				body = u.buffer[headLen:total]
				if masked {
					maskKey := u.buffer[headLen-4 : headLen]
					for i := 0; i < len(body); i++ {
						body[i] ^= maskKey[i%4]
					}
				}

				ok = true
				u.buffer = u.buffer[total:l]
			}
		}
	}

	return opcode, body, ok, fin, res1, res2, res3
}

func (u *Upgrader) returnError(w http.ResponseWriter, _ *http.Request, status int, err error) error {
	w.Header().Set("Sec-Websocket-Version", "13")
	http.Error(w, http.StatusText(status), status)
	return err
}

func (u *Upgrader) selectSubprotocol(r *http.Request, responseHeader http.Header) string {
	if u.Subprotocols != nil {
		clientProtocols := subprotocols(r)
		for _, serverProtocol := range u.Subprotocols {
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
	for _, s := range values {
		for {
			t, s = nextToken(skipSpace(s))
			if t == "" {
				continue
			}
			s = skipSpace(s)
			if s != "" && s[0] != ',' {
				continue
			}
			if equalASCIIFold(t, value) {
				return true
			}
			if s == "" {
				continue
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

func (u *connState) readAll(r io.Reader, size int) ([]byte, error) {
	const maxAppendSize = 1024 * 1024 * 4
	buf := u.Engine.BodyAllocator.Malloc(size)[0:0]
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
			buf = u.Engine.BodyAllocator.Append(buf, make([]byte, al)...)[:l]
		}
	}
}
