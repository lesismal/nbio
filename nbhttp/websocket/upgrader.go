package websocket

import (
	"bytes"
	"crypto/sha1"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/lesismal/llib/std/crypto/tls"
	"github.com/lesismal/nbio"
	"github.com/lesismal/nbio/mempool"
	"github.com/lesismal/nbio/nbhttp"
)

// Hijacker .
type Hijacker interface {
	Hijack() (net.Conn, error)
}

var (
	emptyUpgrader = upgrader{}

	upgraderPool = sync.Pool{
		New: func() interface{} {
			return &upgrader{}
		},
	}
)

// upgrader .
type upgrader struct {
	conn *Conn

	ReadLimit        int64
	HandshakeTimeout time.Duration

	EnableCompression bool
	Subprotocols      []string

	CheckOrigin func(r *http.Request) bool

	expectingFragments bool
	compress           bool
	opcode             MessageType
	buffer             []byte
	message            []byte

	Server *nbhttp.Server
}

func NewUpgrader() *upgrader {
	return upgraderPool.Get().(*upgrader)
}

// Upgrade .
func (u *upgrader) Upgrade(w http.ResponseWriter, r *http.Request, responseHeader http.Header) (net.Conn, error) {
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
	if u.EnableCompression {
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
		tlsConn, ok := conn.(*tls.Conn)
		if !ok {
			return nil, u.returnError(w, r, http.StatusInternalServerError, err)
		}
		nbc, ok = tlsConn.Conn().(*nbio.Conn)
		if !ok {
			return nil, u.returnError(w, r, http.StatusInternalServerError, err)
		}
	}

	parser, ok := nbc.Session().(*nbhttp.Parser)
	if !ok {
		return nil, u.returnError(w, r, http.StatusInternalServerError, err)
	}

	parser.Upgrader = u

	buf := mempool.Malloc(1024)[0:0]
	buf = append(buf, "HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Accept: "...)
	buf = append(buf, acceptKeyBytes(challengeKey)...)
	buf = append(buf, "\r\n"...)
	if subprotocol != "" {
		buf = append(buf, "Sec-WebSocket-Protocol: "...)
		buf = append(buf, subprotocol...)
		buf = append(buf, "\r\n"...)
	}
	if compress {
		buf = append(buf, "Sec-WebSocket-Extensions: permessage-deflate; server_no_context_takeover; client_no_context_takeover\r\n"...)
	}
	for k, vs := range responseHeader {
		if k == "Sec-Websocket-Protocol" {
			continue
		}
		for _, v := range vs {
			buf = append(buf, k...)
			buf = append(buf, ": "...)
			for i := 0; i < len(v); i++ {
				b := v[i]
				if b <= 31 {
					// prevent response splitting.
					b = ' '
				}
				buf = append(buf, b)
			}
			buf = append(buf, "\r\n"...)
		}
	}
	buf = append(buf, "\r\n"...)

	if u.HandshakeTimeout > 0 {
		conn.SetWriteDeadline(time.Now().Add(u.HandshakeTimeout))
	}

	if _, err = conn.Write(buf); err != nil {
		conn.Close()
		return nil, err
	}

	u.conn = newConn(conn, nbc.Hash(), false, subprotocol, compress)
	u.Server = parser.Server
	u.conn.Server = parser.Server
	return u.conn, nil
}

func (u *upgrader) validFrame(opcode MessageType, fin, res1, res2, res3, expectingFragments bool) error {
	if res1 && !u.EnableCompression {
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

// Read .
func (u *upgrader) Read(p *nbhttp.Parser, data []byte) error {
	bufLen := len(u.buffer)
	if u.ReadLimit > 0 && (int64(bufLen+len(data)) > u.ReadLimit || int64(bufLen+len(u.message)) > u.ReadLimit) {
		return nbhttp.ErrTooLong
	}

	var oldBuffer []byte
	if bufLen == 0 {
		u.buffer = data
	} else {
		u.buffer = append(u.buffer, data...)
		oldBuffer = u.buffer
	}

	for i := 0; true; i++ {
		opcode, body, ok, fin, res1, res2, res3 := u.nextFrame()
		if err := u.validFrame(opcode, fin, res1, res2, res3, u.expectingFragments); err != nil {
			return err
		}
		if !ok {
			break
		}
		if opcode == FragmentMessage || opcode == TextMessage || opcode == BinaryMessage {
			if u.opcode == 0 {
				u.opcode = opcode
				u.compress = res1
			}
			bl := len(body)
			if u.conn.dataFrameHandler != nil {
				messageBackup := u.message
				u.message = nil
				if bl > 0 {
					u.message = mempool.Malloc(bl)
					copy(u.message, body)
				}
				if u.opcode == TextMessage && len(u.message) > 0 && !u.Server.CheckUtf8(u.message) {
					u.conn.Close()
				} else {
					u.conn.dataFrameHandler(u.conn, u.opcode, fin, u.message)
				}
				u.message = messageBackup
			}
			if u.conn.messageHandler != nil {
				if bl > 0 {
					if u.message == nil {
						u.message = mempool.Malloc(len(body))
						copy(u.message, body)
					} else {
						u.message = append(u.message, body...)
					}
				}
				if fin {
					if u.compress {
						rc := decompressReader(io.MultiReader(bytes.NewBuffer(u.message), strings.NewReader(flateReaderTail)))
						b, err := readAll(rc, len(u.message)*2)
						mempool.Free(u.message)
						u.message = b
						rc.Close()
						if err != nil {
							return err
						}
					}
					u.handleMessage()
					u.expectingFragments = false
					u.compress = false
				} else {
					u.expectingFragments = true
				}
			}
		} else {
			opcodeBackup := u.opcode
			u.opcode = opcode
			messageBackup := u.message
			u.message = body
			u.handleMessage()
			u.message = messageBackup
			u.opcode = opcodeBackup
		}

		if len(u.buffer) == 0 {
			break
		}
	}

	if bufLen == 0 {
		if len(u.buffer) > 0 {
			tmp := u.buffer
			u.buffer = mempool.Malloc(len(tmp))
			copy(u.buffer, tmp)
		}
	} else {
		if len(u.buffer) < len(oldBuffer) {
			tmp := u.buffer
			u.buffer = mempool.Malloc(len(tmp))
			copy(u.buffer, tmp)
			mempool.Free(oldBuffer)
		}
	}

	return nil
}

// Close .
func (u *upgrader) Close(p *nbhttp.Parser, err error) {
	if u.conn != nil {
		// u.conn.Close()
		u.conn.onClose(u.conn, err)
	}
	if len(u.buffer) > 0 {
		mempool.Free(u.buffer)
	}
	if len(u.message) > 0 {
		mempool.Free(u.message)
	}
	*u = emptyUpgrader
	upgraderPool.Put(u)
}

func (u *upgrader) handleMessage() {
	if u.opcode == TextMessage && !u.Server.CheckUtf8(u.message) {
		u.conn.Close()
		return
	}
	u.conn.handleMessage(u.opcode, u.message)
	u.message = nil
	u.opcode = 0
}

func (u *upgrader) nextFrame() (opcode MessageType, body []byte, ok, fin, res1, res2, res3 bool) {
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
					mask := u.buffer[headLen-4 : headLen]
					for i := 0; i < len(body); i++ {
						body[i] ^= mask[i%4]
					}
				}

				ok = true
				u.buffer = u.buffer[total:l]
			}
		}
	}

	return opcode, body, ok, fin, res1, res2, res3
}

func (u *upgrader) returnError(w http.ResponseWriter, r *http.Request, status int, err error) error {
	w.Header().Set("Sec-Websocket-Version", "13")
	http.Error(w, http.StatusText(status), status)
	return err
}

func (u *upgrader) selectSubprotocol(r *http.Request, responseHeader http.Header) string {
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

func acceptKeyBytes(challengeKey string) []byte {
	h := sha1.New()
	h.Write([]byte(challengeKey))
	h.Write(keyGUID)
	sum := h.Sum(nil)
	buf := make([]byte, base64.StdEncoding.EncodedLen(len(sum)))
	base64.StdEncoding.Encode(buf, sum)
	return buf
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
	t := ""
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

func readAll(r io.Reader, size int) ([]byte, error) {
	const maxAppendSize = 1024 * 1024 * 4
	buf := mempool.Malloc(size)[0:0]
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
			tail := mempool.Malloc(al)
			buf = append(buf, tail...)[:l]
			mempool.Free(tail)
		}
	}
}
