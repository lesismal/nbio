package websocket

import (
	"crypto/rand"
	"crypto/sha1"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"runtime"
	"strings"
	"time"
	"unicode/utf8"
	"unsafe"

	"github.com/lesismal/llib/std/crypto/tls"
	"github.com/lesismal/nbio"
	"github.com/lesismal/nbio/logging"
	"github.com/lesismal/nbio/mempool"
	"github.com/lesismal/nbio/nbhttp"
)

var (
	// DefaultBlockingReadBufferSize .
	DefaultBlockingReadBufferSize = 1024 * 4

	// DefaultBlockingModAsyncWrite .
	DefaultBlockingModAsyncWrite = true

	DefaultBlockingModSendQueueInitSize = 4

	DefaultBlockingModSendQueueMaxSize = 0

	DefaultBlockingModAsyncCloseDelay = time.Second / 10

	// DefaultEngine will be set to a Upgrader.Engine to handle details such as buffers.
	DefaultEngine = nbhttp.NewEngine(nbhttp.Config{
		ReleaseWebsocketPayload: true,
	})
)

type commonFields struct {
	Engine                     *nbhttp.Engine
	KeepaliveTime              time.Duration
	MessageLengthLimit         int
	BlockingModAsyncCloseDelay time.Duration

	enableCompression bool
	compressionLevel  int

	pingMessageHandler  func(c *Conn, appData string)
	pongMessageHandler  func(c *Conn, appData string)
	closeMessageHandler func(c *Conn, code int, text string)

	openHandler      func(*Conn)
	messageHandler   func(c *Conn, messageType MessageType, data []byte)
	dataFrameHandler func(c *Conn, messageType MessageType, fin bool, data []byte)
	onClose          func(c *Conn, err error)
}

// Upgrader .
type Upgrader struct {
	commonFields

	// Subprotocols .
	Subprotocols []string

	// CheckOrigin .
	CheckOrigin func(r *http.Request) bool

	// HandshakeTimeout represents the timeout duration during websocket handshake.
	HandshakeTimeout time.Duration

	// BlockingModReadBufferSize represents the read buffer size of a Conn if it's in blocking mod.
	BlockingModReadBufferSize int

	// BlockingModAsyncWrite represents whether use a goroutine to handle writing:
	// true: use dynamic goroutine to handle writing.
	// false: write buffer to the conn directely.
	BlockingModAsyncWrite bool

	// BlockingModSendQueueInitSize represents the init size of a Conn's send queue,
	// only takes effect when `BlockingModAsyncWrite` is true.
	BlockingModSendQueueInitSize int

	// BlockingModSendQueueInitSize represents the max size of a Conn's send queue,
	// only takes effect when `BlockingModAsyncWrite` is true.
	BlockingModSendQueueMaxSize int
}

// NewUpgrader .
func NewUpgrader() *Upgrader {
	u := &Upgrader{
		commonFields: commonFields{
			Engine:                     DefaultEngine,
			compressionLevel:           defaultCompressionLevel,
			BlockingModAsyncCloseDelay: DefaultBlockingModAsyncCloseDelay,
		},
		BlockingModReadBufferSize:    DefaultBlockingReadBufferSize,
		BlockingModAsyncWrite:        DefaultBlockingModAsyncWrite,
		BlockingModSendQueueInitSize: DefaultBlockingModSendQueueInitSize,
		BlockingModSendQueueMaxSize:  DefaultBlockingModSendQueueMaxSize,
	}
	u.pingMessageHandler = func(c *Conn, data string) {
		err := c.WriteMessage(PongMessage, []byte(data))
		if err != nil {
			logging.Debug("failed to send pong %v", err)
			c.Close()
			return
		}
	}
	u.pongMessageHandler = func(*Conn, string) {}
	u.closeMessageHandler = func(c *Conn, code int, text string) {
		if code == 1005 {
			c.WriteMessage(CloseMessage, nil)
			return
		}
		buf := mempool.Malloc(len(text) + 2)
		binary.BigEndian.PutUint16(buf[:2], uint16(code))
		copy(buf[2:], text)
		c.WriteMessage(CloseMessage, buf)
		mempool.Free(buf)
	}

	return u
}

// EnableCompression .
func (u *Upgrader) EnableCompression(enable bool) {
	u.enableCompression = enable
}

// SetCompressionLevel .
func (u *Upgrader) SetCompressionLevel(level int) error {
	if !isValidCompressionLevel(level) {
		return errors.New("websocket: invalid compression level")
	}
	u.compressionLevel = level
	return nil
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
			if c.Engine.ReleaseWebsocketPayload && len(data) > 0 {
				defer c.Engine.BodyAllocator.Free(data)
			}
			if !c.closed {
				h(c, messageType, data)
			}
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

// Upgrade .
func (u *Upgrader) Upgrade(w http.ResponseWriter, r *http.Request, responseHeader http.Header, args ...interface{}) (*Conn, error) {
	challengeKey, subprotocol, compress, err := u.commCheck(w, r, responseHeader)
	if err != nil {
		return nil, err
	}

	h, ok := w.(http.Hijacker)
	if !ok {
		return nil, u.returnError(w, r, http.StatusInternalServerError, ErrUpgradeNotHijacker)
	}
	conn, _, err := h.Hijack()
	if err != nil {
		return nil, u.returnError(w, r, http.StatusInternalServerError, err)
	}

	var wsc *Conn
	var nbc *nbio.Conn
	var engine = u.Engine
	var parser *nbhttp.Parser
	var transferConn bool
	if len(args) > 0 {
		var b bool
		b, ok = args[0].(bool)
		transferConn = ok && b
	}

	getParser := func() {
		var nbResonse *nbhttp.Response
		nbResonse, ok = w.(*nbhttp.Response)
		if ok {
			parser = nbResonse.Parser
			parser.Reader = wsc
		}
	}

	switch vt := conn.(type) {
	case *nbio.Conn:
		// Scenario 1: *nbio.Conn, handled by nbhttp.Engine.
		parser, ok = vt.Session().(*nbhttp.Parser)
		if !ok {
			return nil, u.returnError(w, r, http.StatusInternalServerError, err)
		}
		wsc = NewConn(u, conn, subprotocol, compress, false)
		wsc.Engine = parser.Engine
		parser.Reader = wsc
	case *tls.Conn:
		// Scenario 2: llib's *tls.Conn.
		nbc, ok = vt.Conn().(*nbio.Conn)
		if !ok {
			// 2.1 The conn may be from std's http.Server.Serve(llib's tls.Listener),
			//     or from nbhttp.Engine's IOModBlocking/Mixed(blocking part).
			if transferConn {
				// 2.1.1 Transfer the conn to poller.
				nbc, err = nbio.NBConn(vt.Conn())
				if err != nil {
					return nil, u.returnError(w, r, http.StatusInternalServerError, err)
				}
				vt.ResetRawInput()
				parser = &nbhttp.Parser{Execute: nbc.Execute}
				if engine.EpollMod == nbio.EPOLLET && engine.EPOLLONESHOT == nbio.EPOLLONESHOT {
					parser.Execute = nbhttp.SyncExecutor
				}
				wsc = NewConn(u, vt, subprotocol, compress, false)
				nbc.SetSession(wsc)
				nbc.OnData(func(c *nbio.Conn, data []byte) {
					defer func() {
						if err := recover(); err != nil {
							const size = 64 << 10
							buf := make([]byte, size)
							buf = buf[:runtime.Stack(buf, false)]
							logging.Error("execute failed: %v\n%v\n", err, *(*string)(unsafe.Pointer(&buf)))
						}
					}()
					defer vt.ResetOrFreeBuffer()

					var nread int
					var readed = data
					var buffer = data
					var errRead error
					for {
						_, nread, errRead = vt.AppendAndRead(readed, buffer)
						readed = nil
						if errRead != nil {
							c.CloseWithError(err)
							return
						}
						if nread > 0 {
							errRead = wsc.Read(parser, buffer[:nread])
							if err != nil {
								logging.Debug("websocket Conn Read failed: %v", errRead)
								c.CloseWithError(errRead)
								return
							}
						}
						if nread == 0 {
							return
						}
					}
				})
				nonblock := true
				vt.ResetConn(nbc, nonblock)
				err = engine.AddTransferredConn(nbc)
				if err != nil {
					return nil, u.returnError(w, r, http.StatusInternalServerError, err)
				}
			} else {
				// 2.1.2 Don't transfer the conn to poller.
				wsc = NewConn(u, conn, subprotocol, compress, u.BlockingModAsyncWrite)
				wsc.isBlockingMod = true
				getParser()
			}
		} else {
			// 2.2 The conn is from nbio poller.
			parser, ok = nbc.Session().(*nbhttp.Parser)
			if !ok {
				return nil, u.returnError(w, r, http.StatusInternalServerError, err)
			}
			wsc = NewConn(u, conn, subprotocol, compress, false)
			wsc.Engine = parser.Engine
			parser.Reader = wsc
		}
	case *net.TCPConn:
		// Scenario 3: std's *net.TCPConn.
		if transferConn {
			// 3.1 Transfer the conn to poller.
			nbc, err = nbio.NBConn(vt)
			if err != nil {
				return nil, u.returnError(w, r, http.StatusInternalServerError, err)
			}
			parser = &nbhttp.Parser{Execute: nbc.Execute}
			if engine.EpollMod == nbio.EPOLLET && engine.EPOLLONESHOT == nbio.EPOLLONESHOT {
				parser.Execute = nbhttp.SyncExecutor
			}
			wsc = NewConn(u, nbc, subprotocol, compress, false)
			nbc.SetSession(wsc)
			nbc.OnData(func(c *nbio.Conn, data []byte) {
				defer func() {
					if err := recover(); err != nil {
						const size = 64 << 10
						buf := make([]byte, size)
						buf = buf[:runtime.Stack(buf, false)]
						logging.Error("execute failed: %v\n%v\n", err, *(*string)(unsafe.Pointer(&buf)))
					}
				}()

				errRead := wsc.Read(parser, data)
				if errRead != nil {
					logging.Debug("websocket Conn Read failed: %v", errRead)
					c.CloseWithError(errRead)
					return
				}
			})
			err = engine.AddTransferredConn(nbc)
			if err != nil {
				return nil, u.returnError(w, r, http.StatusInternalServerError, err)
			}
		} else {
			// 3.2 Don't transfer the conn to poller.
			wsc = NewConn(u, conn, subprotocol, compress, u.BlockingModAsyncWrite)
			wsc.isBlockingMod = true
			getParser()
		}
	default:
		// Scenario 4: Unknown conn type, mostly is std's *tls.Conn, from std's http.Server.
		wsc = NewConn(u, conn, subprotocol, compress, u.BlockingModAsyncWrite)
		wsc.isBlockingMod = true
		getParser()
	}

	err = u.commResponse(wsc.Conn, responseHeader, challengeKey, subprotocol, compress)
	if err != nil {
		return nil, err
	}

	if wsc.openHandler != nil {
		wsc.openHandler(wsc)
	}

	if wsc.isBlockingMod {
		if parser == nil {
			go wsc.BlockingModReadLoop(u.BlockingModReadBufferSize)
		}
	}

	return wsc, nil
}

func (u *Upgrader) UpgradeAndTransferConnToPoller(w http.ResponseWriter, r *http.Request, responseHeader http.Header) (*Conn, error) {
	const trasferConn = true
	return u.Upgrade(w, r, responseHeader, trasferConn)
}

func (u *Upgrader) commCheck(w http.ResponseWriter, r *http.Request, responseHeader http.Header) (string, string, bool, error) {
	if !headerContains(r.Header, "Connection", "upgrade") {
		return "", "", false, u.returnError(w, r, http.StatusBadRequest, ErrUpgradeTokenNotFound)
	}

	if !headerContains(r.Header, "Upgrade", "websocket") {
		return "", "", false, u.returnError(w, r, http.StatusBadRequest, ErrUpgradeTokenNotFound)
	}

	if r.Method != "GET" {
		return "", "", false, u.returnError(w, r, http.StatusMethodNotAllowed, ErrUpgradeMethodIsGet)
	}

	if !headerContains(r.Header, "Sec-Websocket-Version", "13") {
		return "", "", false, u.returnError(w, r, http.StatusBadRequest, ErrUpgradeInvalidWebsocketVersion)
	}

	if _, ok := responseHeader["Sec-Websocket-Extensions"]; ok {
		return "", "", false, u.returnError(w, r, http.StatusInternalServerError, ErrUpgradeUnsupportedExtensions)
	}

	checkOrigin := u.CheckOrigin
	if checkOrigin == nil {
		checkOrigin = checkSameOrigin
	}
	if !checkOrigin(r) {
		return "", "", false, u.returnError(w, r, http.StatusForbidden, ErrUpgradeOriginNotAllowed)
	}

	challengeKey := r.Header.Get("Sec-Websocket-Key")
	if challengeKey == "" {
		return "", "", false, u.returnError(w, r, http.StatusBadRequest, ErrUpgradeMissingWebsocketKey)
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
	return challengeKey, subprotocol, compress, nil
}

func (u *Upgrader) commResponse(conn net.Conn, responseHeader http.Header, challengeKey, subprotocol string, compress bool) error {
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

	_, err := conn.Write(buf)
	mempool.Free(buf)
	if err != nil {
		conn.Close()
		return err
	}

	if u.KeepaliveTime <= 0 {
		conn.SetReadDeadline(time.Now().Add(nbhttp.DefaultKeepaliveTime))
	} else {
		conn.SetReadDeadline(time.Now().Add(u.KeepaliveTime))
	}

	return nil
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
