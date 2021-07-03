package websocket

import (
	"crypto/sha1"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
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

// Upgrader .
type Upgrader struct {
	conn *Conn

	ReadLimit        int64
	HandshakeTimeout time.Duration

	Subprotocols []string

	CheckOrigin func(r *http.Request) bool

	expectingFragments bool
	opcode             MessageType
	buffer             []byte
	message            []byte

	Server *nbhttp.Server
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

	h, ok := w.(nbhttp.Hijacker)
	if !ok {
		return nil, u.returnError(w, r, http.StatusInternalServerError, ErrUpgradeNotHijacker)
	}
	conn, err := h.Hijack()
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

	w.WriteHeader(http.StatusSwitchingProtocols)
	w.Header().Add("Upgrade", "websocket")
	w.Header().Add("Connection", "Upgrade")
	w.Header().Add("Sec-WebSocket-Accept", string(acceptKey(challengeKey)))
	if subprotocol != "" {
		w.Header().Add("Sec-WebSocket-Protocol", subprotocol)
	}

	for k, vv := range responseHeader {
		if k != "Sec-Websocket-Protocol" {
			for _, v := range vv {
				w.Header().Add(k, v)
			}
		}
	}

	if u.HandshakeTimeout > 0 {
		conn.SetWriteDeadline(time.Now().Add(u.HandshakeTimeout))
	}

	u.conn = newConn(conn, nbc.Hash(), false, subprotocol)
	u.Server = parser.Server
	u.conn.Server = parser.Server
	return u.conn, nil
}

func validFrame(opcode MessageType, fin, res1, res2, res3, expectingFragments bool) error {
	if res1 || res2 || res3 {
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
func (u *Upgrader) Read(p *nbhttp.Parser, data []byte) error {
	bufLen := len(u.buffer)
	if u.ReadLimit > 0 && (int64(bufLen+len(data)) > u.ReadLimit || int64(bufLen+len(u.message)) > u.ReadLimit) {
		return nbhttp.ErrTooLong
	}

	if bufLen == 0 {
		u.buffer = data
	} else {
		u.buffer = append(u.buffer, data...)
	}

	for i := 0; true; i++ {
		opcode, body, ok, fin, res1, res2, res3 := u.nextFrame()
		if err := validFrame(opcode, fin, res1, res2, res3, u.expectingFragments); err != nil {
			return err
		}
		if !ok {
			break
		}
		bl := len(body)
		if opcode == FragmentMessage || opcode == TextMessage || opcode == BinaryMessage {
			if u.opcode == 0 {
				u.opcode = opcode
			}
			//<<<<<<< HEAD
			//			if u.conn.dataFrameHandler != nil {
			//				u.message = nil
			//				if bl > 0 {
			//					u.message = u.Server.Malloc(bl)
			//					copy(u.message, body)
			//				}
			//				if u.opcode == TextMessage && len(u.message) > 0 && !u.Server.CheckUtf8(u.message) {
			//					u.conn.Close()
			//				} else {
			//					u.conn.dataFrameHandler(u.conn, u.opcode, fin, u.message)
			//				}
			//				u.message = nil
			//			} else {
			//				if bl > 0 {
			//					ml := len(u.message)
			//					if ml == 0 {
			//						u.message = u.Server.Malloc(bl)
			//					} else {
			//						rl := ml + len(body)
			//						u.message = u.Server.Realloc(u.message, rl)
			//					}
			//					copy(u.message[ml:], body)
			//=======
			if bl > 0 {
				if u.message == nil {
					u.message = mempool.Malloc(len(body))
					copy(u.message, body)
				} else {
					u.message = append(u.message, body...)
					// >>>>>>> master
				}
			}
			if fin {
				u.handleMessage()
				u.expectingFragments = false
			} else {
				u.expectingFragments = true
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
		tmp := u.buffer
		u.buffer = mempool.Malloc(len(tmp))
		copy(u.buffer, tmp)
	}

	return nil
}

// Close .
func (u *Upgrader) Close(p *nbhttp.Parser, err error) {
	if u.conn != nil {
		u.conn.onClose(u.conn, err)
	}
}

func (u *Upgrader) handleMessage() {
	if u.opcode == TextMessage && !u.Server.CheckUtf8(u.message) {
		u.conn.Close()
		return
	}
	u.conn.handleMessage(u.opcode, u.message)
	u.message = nil
	u.opcode = 0
}

func (u *Upgrader) nextFrame() (opcode MessageType, body []byte, ok, fin, res1, res2, res3 bool) {
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

func (u *Upgrader) returnError(w http.ResponseWriter, r *http.Request, status int, err error) error {
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

// NewUpgrader .
func NewUpgrader(isTLS bool) *Upgrader {
	return &Upgrader{
		expectingFragments: false,
	}
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

func acceptKey(challengeKey string) string {
	h := sha1.New()
	h.Write([]byte(challengeKey))
	h.Write(keyGUID)
	return base64.StdEncoding.EncodeToString(h.Sum(nil))
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
	value = strings.ToLower(value)
	for _, v := range header[name] {
		if strings.ToLower(v) == value {
			return true
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
