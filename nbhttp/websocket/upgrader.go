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
	"github.com/lesismal/nbio/nbhttp"
)

var (
	// DefaultBlockingReadBufferSize .
	DefaultBlockingReadBufferSize = 1024 * 4

	// DefaultBlockingModAsyncWrite .
	DefaultBlockingModAsyncWrite = true

	// DefaultBlockingModHandleRead .
	DefaultBlockingModHandleRead = true

	// DefaultBlockingModTransferConnToPoller .
	DefaultBlockingModTransferConnToPoller = false

	// DefaultBlockingModSendQueueInitSize .
	DefaultBlockingModSendQueueInitSize = 4

	// DefaultBlockingModSendQueueMaxSize .
	DefaultBlockingModSendQueueMaxSize uint16 = 0

	// DefaultMessageLengthLimit .
	DefaultMessageLengthLimit = 1024 * 1024 * 4

	// DefaultBlockingModAsyncCloseDelay .
	DefaultBlockingModAsyncCloseDelay = time.Second / 10

	// DefaultEngine will be set to a Upgrader.Engine to handle details such as buffers.
	DefaultEngine = nbhttp.NewEngine(nbhttp.Config{
		ReleaseWebsocketPayload: true,
	})
)

type commonFields struct {
	KeepaliveTime              time.Duration
	MessageLengthLimit         int
	BlockingModAsyncCloseDelay time.Duration

	ReleasePayload        bool
	WebsocketCompressor   func(c *Conn, w io.WriteCloser, level int) io.WriteCloser
	WebsocketDecompressor func(c *Conn, r io.Reader) io.ReadCloser

	pingMessageHandler  func(c *Conn, appData string)
	pongMessageHandler  func(c *Conn, appData string)
	closeMessageHandler func(c *Conn, code int, text string)

	openHandler      func(*Conn)
	messageHandler   func(c *Conn, messageType MessageType, messagePtr *[]byte)
	dataFrameHandler func(c *Conn, messageType MessageType, fin bool, framePtr *[]byte)
}

type Options = Upgrader

//go:norace
func NewOptions() *Options {
	return NewUpgrader()
}

// Upgrader .
type Upgrader struct {
	commonFields

	// Engine .
	Engine *nbhttp.Engine

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

	// BlockingModHandleRead represents whether start a goroutine to handle reading automatically during `Upgrade``:
	// true: start a new goroutine to handle reading.
	// false: use the current goroutine to handle reading.
	//
	// Notice:
	// If we start a goroutine to handle read during `Upgrade`, we may receive a new websocket message.
	// before we have left the http.Handler for the `Websocket Handshake`.
	// Then if we have the logic of `websocket.Conn.SetSession` in the http.Handler, it's possible that when we receive
	// and are handling a websocket message and call `websocket.Conn.Session()`, we get nil.
	//
	// To fix this nil session problem, can use `websocket.Conn.SessionWithLock()`.
	//
	// For other concurrent problems(including the nil session problem), we can:
	// 1st: set this `BlockingModHandleRead = false`
	// 2nd: `go wsConn.HandleRead(YourBufSize)` after `Upgrade` and finished initialization.
	// Then the websocket message wouldn't come before the http.Handler for `Websocket Handshake` has done.
	BlockingModHandleRead bool

	// BlockingModTrasferConnToPoller represents whether try to transfer a blocking connection to nonblocking and add to `Engine``.
	// true: try to transfer.
	// false: don't try to transfer.
	//
	// Notice:
	// Only `net.TCPConn` and `llib's blocking tls.Conn` can be transferred to nonblocking.
	BlockingModTrasferConnToPoller bool

	// BlockingModSendQueueInitSize represents the init size of a Conn's send queue,
	// only takes effect when `BlockingModAsyncWrite` is true.
	BlockingModSendQueueInitSize int

	// BlockingModSendQueueInitSize represents the max size of a Conn's send queue,
	// only takes effect when `BlockingModAsyncWrite` is true.
	BlockingModSendQueueMaxSize uint16

	enableCompression bool
	compressionLevel  int
	onClose           func(c *Conn, err error)
}

// NewUpgrader .
//
//go:norace
func NewUpgrader() *Upgrader {
	u := &Upgrader{
		commonFields: commonFields{
			KeepaliveTime:              nbhttp.DefaultKeepaliveTime,
			MessageLengthLimit:         DefaultMessageLengthLimit,
			BlockingModAsyncCloseDelay: DefaultBlockingModAsyncCloseDelay,
		},
		compressionLevel:               defaultCompressionLevel,
		Engine:                         DefaultEngine,
		BlockingModReadBufferSize:      DefaultBlockingReadBufferSize,
		BlockingModAsyncWrite:          DefaultBlockingModAsyncWrite,
		BlockingModHandleRead:          DefaultBlockingModHandleRead,
		BlockingModTrasferConnToPoller: DefaultBlockingModTransferConnToPoller,
		BlockingModSendQueueInitSize:   DefaultBlockingModSendQueueInitSize,
		BlockingModSendQueueMaxSize:    DefaultBlockingModSendQueueMaxSize,
	}
	u.pingMessageHandler = func(c *Conn, data string) {
		err := c.WriteMessage(PongMessage, []byte(data))
		if err != nil {
			logging.Debug("failed to send pong %v", err)
			_ = c.Close()
			return
		}
	}
	u.pongMessageHandler = func(*Conn, string) {}
	u.closeMessageHandler = func(c *Conn, code int, text string) {
		if code == 1005 {
			_ = c.WriteMessage(CloseMessage, nil)
			return
		}
		pbuf := u.Engine.BodyAllocator.Malloc(len(text) + 2)
		binary.BigEndian.PutUint16((*pbuf)[:2], uint16(code))
		copy((*pbuf)[2:], text)
		_ = c.WriteMessage(CloseMessage, (*pbuf))
		u.Engine.BodyAllocator.Free(pbuf)
	}

	return u
}

// EnableCompression .
//
//go:norace
func (u *Upgrader) EnableCompression(enable bool) {
	u.enableCompression = enable
}

// SetCompressionLevel .
//
//go:norace
func (u *Upgrader) SetCompressionLevel(level int) error {
	if !isValidCompressionLevel(level) {
		return errors.New("websocket: invalid compression level")
	}
	u.compressionLevel = level
	return nil
}

// SetCloseHandler .
//
//go:norace
func (u *Upgrader) SetCloseHandler(h func(*Conn, int, string)) {
	if h != nil {
		u.closeMessageHandler = h
	}
}

// SetPingHandler .
//
//go:norace
func (u *Upgrader) SetPingHandler(h func(*Conn, string)) {
	if h != nil {
		u.pingMessageHandler = h
	}
}

// SetPongHandler .
//
//go:norace
func (u *Upgrader) SetPongHandler(h func(*Conn, string)) {
	if h != nil {
		u.pongMessageHandler = h
	}
}

// OnOpen .
//
//go:norace
func (u *Upgrader) OnOpen(h func(*Conn)) {
	u.openHandler = h
}

// OnMessage .
//
//go:norace
func (u *Upgrader) OnMessage(h func(*Conn, MessageType, []byte)) {
	u.messageHandler = func(c *Conn, messageType MessageType, messagePtr *[]byte) {
		if !c.closed && h != nil {
			if messagePtr != nil {
				h(c, messageType, *messagePtr)
			} else {
				h(c, messageType, nil)
			}
		}
	}
}

// OnMessage .
//
//go:norace
func (u *Upgrader) OnMessagePtr(h func(*Conn, MessageType, *[]byte)) {
	u.messageHandler = func(c *Conn, messageType MessageType, messagePtr *[]byte) {
		if !c.closed && h != nil {
			h(c, messageType, messagePtr)
		}
	}
}

// OnDataFrame .
//
//go:norace
func (u *Upgrader) OnDataFrame(h func(*Conn, MessageType, bool, []byte)) {
	u.dataFrameHandler = func(c *Conn, messageType MessageType, fin bool, framePtr *[]byte) {
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
func (u *Upgrader) OnDataFramePtr(h func(*Conn, MessageType, bool, *[]byte)) {
	u.dataFrameHandler = func(c *Conn, messageType MessageType, fin bool, framePtr *[]byte) {
		if !c.closed && h != nil {
			h(c, messageType, fin, framePtr)
		}
	}
}

// OnClose .
//
//go:norace
func (u *Upgrader) OnClose(h func(*Conn, error)) {
	u.onClose = h
}

// Upgrade .
//
//go:norace
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
	var transferConn = u.BlockingModTrasferConnToPoller

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
		}
	}

	clearNBCWSSession := func() {
		if nbc != nil {
			if _, ok = nbc.Session().(*Conn); ok {
				nbc.SetSession(nil)
			}
		}
	}

	var underLayerConn net.Conn
	nbhttpConn, isReadingByParser := conn.(*nbhttp.Conn)
	if isReadingByParser {
		underLayerConn = nbhttpConn.Conn
		parser = nbhttpConn.Parser
	} else {
		underLayerConn = conn
	}

	switch vt := underLayerConn.(type) {
	case *nbio.Conn:
		// Scenario 1: *nbio.Conn, handled by nbhttp.Engine.
		nbc = vt
		parser, ok = nbc.Session().(*nbhttp.Parser)
		if !ok {
			return nil, u.returnError(w, r, http.StatusInternalServerError, err)
		}
		wsc = NewServerConn(u, conn, subprotocol, compress, false)
		wsc.Engine = parser.Engine
		wsc.Execute = parser.Execute
		nbc.SetSession(wsc)
		if nbhttpConn != nil {
			nbhttpConn.Parser = nil
		}
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
				if nbhttpConn != nil {
					nbhttpConn.Trasfered = true
				}
				vt.ResetRawInput()
				wsc = NewServerConn(u, vt, subprotocol, compress, false)
				wsc.Engine = engine
				wsc.Execute = nbc.Execute
				if engine.EpollMod == nbio.EPOLLET && engine.EPOLLONESHOT == nbio.EPOLLONESHOT {
					wsc.Execute = nbhttp.SyncExecutor
				}
				if nbhttpConn != nil {
					nbhttpConn.Parser = nil
				}
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
							_ = c.CloseWithError(err)
							return
						}
						if nread > 0 {
							errRead = wsc.Parse(buffer[:nread])
							if err != nil {
								logging.Debug("websocket Conn Parse failed: %v", errRead)
								_ = c.CloseWithError(errRead)
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
					clearNBCWSSession()
					return nil, u.returnError(w, r, http.StatusInternalServerError, err)
				}
			} else {
				// 2.1.2 Don't transfer the conn to poller.
				wsc = NewServerConn(u, conn, subprotocol, compress, u.BlockingModAsyncWrite)
				wsc.isBlockingMod = true
				getParser()
				if parser != nil {
					wsc.Execute = parser.Execute
					parser.ParserCloser = wsc
					if nbhttpConn != nil {
						nbhttpConn.Parser = nil
					}
				}
			}
		} else {
			// 2.2 The conn is from nbio poller.
			parser, ok = nbc.Session().(*nbhttp.Parser)
			if !ok {
				return nil, u.returnError(w, r, http.StatusInternalServerError, err)
			}
			wsc = NewServerConn(u, conn, subprotocol, compress, false)
			wsc.Engine = parser.Engine
			wsc.Execute = parser.Execute
			nbc.SetSession(wsc)
			if nbhttpConn != nil {
				nbhttpConn.Parser = nil
			}
		}
	case *net.TCPConn:
		// Scenario 3: std's *net.TCPConn.
		if transferConn {
			// 3.1 Transfer the conn to poller.
			nbc, err = nbio.NBConn(vt)
			if err != nil {
				return nil, u.returnError(w, r, http.StatusInternalServerError, err)
			}
			if nbhttpConn != nil {
				nbhttpConn.Trasfered = true
			}

			wsc = NewServerConn(u, nbc, subprotocol, compress, false)
			wsc.Engine = engine
			wsc.Execute = nbc.Execute
			if engine.EpollMod == nbio.EPOLLET && engine.EPOLLONESHOT == nbio.EPOLLONESHOT {
				wsc.Execute = nbhttp.SyncExecutor
			}
			if nbhttpConn != nil {
				nbhttpConn.Parser = nil
			}
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

				errRead := wsc.Parse(data)
				if errRead != nil {
					logging.Debug("websocket Conn Parse failed: %v", errRead)
					_ = c.CloseWithError(errRead)
					return
				}
			})
			err = engine.AddTransferredConn(nbc)
			if err != nil {
				clearNBCWSSession()
				return nil, u.returnError(w, r, http.StatusInternalServerError, err)
			}
		} else {
			// 3.2 Don't transfer the conn to poller.
			wsc = NewServerConn(u, conn, subprotocol, compress, u.BlockingModAsyncWrite)
			wsc.isBlockingMod = true
			getParser()
			if parser != nil {
				wsc.Execute = parser.Execute
				parser.ParserCloser = wsc
				if nbhttpConn != nil {
					nbhttpConn.Parser = nil
				}
			}
		}
	default:
		// Scenario 4: Unknown conn type, mostly is std *tls.Conn, from std http.Server.
		wsc = NewServerConn(u, conn, subprotocol, compress, u.BlockingModAsyncWrite)
		wsc.isBlockingMod = true
		getParser()
		if parser != nil {
			wsc.Execute = parser.Execute
			parser.ParserCloser = wsc
			if nbhttpConn != nil {
				nbhttpConn.Parser = nil
			}
		}
	}

	err = u.commResponse(wsc.Conn, responseHeader, challengeKey, subprotocol, compress)
	if err != nil {
		clearNBCWSSession()
		return nil, err
	}

	if u.KeepaliveTime > 0 {
		_ = conn.SetReadDeadline(time.Now().Add(u.KeepaliveTime))
	} else {
		_ = conn.SetReadDeadline(time.Time{})
	}

	if wsc.openHandler != nil {
		wsc.openHandler(wsc)
	}

	// if parser != nil {
	// 	parser.ReadCloser = wsc
	// 	wsc.Execute = parser.Execute
	// }
	wsc.isReadingByParser = isReadingByParser
	if wsc.isBlockingMod && (!wsc.isReadingByParser) {
		var handleRead = u.BlockingModHandleRead
		if len(args) > 1 {
			var b bool
			b, ok = args[1].(bool)
			handleRead = ok && b
		}
		if handleRead {
			wsc.chSessionInited = make(chan struct{})
			go wsc.HandleRead(u.BlockingModReadBufferSize)
		}
	}

	// If upgrader.ReleasePayload is false, which maybe by default,
	// then set it to engine.ReleaseWebsocketPayload.
	// Considering compatibility with old versions, should set it to
	// upgrader.ReleasePayload only when upgrader.ReleasePayload is true.
	wsc.releasePayload = u.ReleasePayload
	if !wsc.releasePayload {
		wsc.releasePayload = wsc.Engine.ReleaseWebsocketPayload
	}

	return wsc, nil
}

//go:norace
func (u *Upgrader) UpgradeAndTransferConnToPoller(w http.ResponseWriter, r *http.Request, responseHeader http.Header) (*Conn, error) {
	const trasferConn = true
	return u.Upgrade(w, r, responseHeader, trasferConn)
}

//go:norace
func (u *Upgrader) UpgradeWithoutHandlingReadForConnFromSTDServer(w http.ResponseWriter, r *http.Request, responseHeader http.Header) (*Conn, error) {
	// handle std server's conn, no need transfer conn to nbio Engine
	const trasferConn = false
	const handleRead = false
	return u.Upgrade(w, r, responseHeader, trasferConn, handleRead)
}

//go:norace
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

//go:norace
func (u *Upgrader) commResponse(conn net.Conn, responseHeader http.Header, challengeKey, subprotocol string, compress bool) error {
	allocator := u.Engine.BodyAllocator
	pbuf := allocator.Malloc(1024)
	*pbuf = (*pbuf)[0:0]
	pbuf = allocator.AppendString(pbuf, "HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Accept: ")
	pbuf = allocator.Append(pbuf, acceptKeyBytes(challengeKey)...)
	pbuf = allocator.AppendString(pbuf, "\r\n")
	if subprotocol != "" {
		pbuf = allocator.AppendString(pbuf, "Sec-WebSocket-Protocol: ")
		pbuf = allocator.AppendString(pbuf, subprotocol)
		pbuf = allocator.AppendString(pbuf, "\r\n")
	}
	if compress {
		pbuf = allocator.AppendString(pbuf, "Sec-WebSocket-Extensions: permessage-deflate; server_no_context_takeover; client_no_context_takeover\r\n")
	}
	for k, vs := range responseHeader {
		if k == "Sec-Websocket-Protocol" {
			continue
		}
		for _, v := range vs {
			pbuf = allocator.AppendString(pbuf, k)
			pbuf = allocator.AppendString(pbuf, ": ")
			for i := 0; i < len(v); i++ {
				b := v[i]
				if b <= 31 {
					// prevent response splitting.
					b = ' '
				}
				pbuf = allocator.Append(pbuf, b)
			}
			pbuf = allocator.AppendString(pbuf, "\r\n")
		}
	}
	pbuf = allocator.AppendString(pbuf, "\r\n")

	if u.HandshakeTimeout > 0 {
		_ = conn.SetWriteDeadline(time.Now().Add(u.HandshakeTimeout))
	}

	_, err := conn.Write(*pbuf)
	allocator.Free(pbuf)
	if err != nil {
		_ = conn.Close()
		return err
	}

	return nil
}

//go:norace
func (u *Upgrader) returnError(w http.ResponseWriter, _ *http.Request, status int, err error) error {
	w.Header().Set("Sec-Websocket-Version", "13")
	http.Error(w, http.StatusText(status), status)
	return err
}

//go:norace
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

//go:norace
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

//go:norace
func acceptKeyString(challengeKey string) string {
	h := sha1.New() //nolint:gosec // per websocket protocol spec
	h.Write([]byte(challengeKey))
	h.Write(keyGUID)
	return base64.StdEncoding.EncodeToString(h.Sum(nil))
}

//go:norace
func acceptKeyBytes(challengeKey string) []byte {
	h := sha1.New() //nolint:gosec // per websocket protocol spec
	h.Write([]byte(challengeKey))
	h.Write(keyGUID)
	sum := h.Sum(nil)
	buf := make([]byte, base64.StdEncoding.EncodedLen(len(sum)))
	base64.StdEncoding.Encode(buf, sum)
	return buf
}

//go:norace
func challengeKey() (string, error) {
	p := make([]byte, 16)
	if _, err := io.ReadFull(rand.Reader, p); err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(p), nil
}

//go:norace
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

//go:norace
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

//go:norace
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

//go:norace
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

//go:norace
func skipSpace(s string) (rest string) {
	i := 0
	for ; i < len(s); i++ {
		if b := s[i]; b != ' ' && b != '\t' {
			break
		}
	}
	return s[i:]
}

//go:norace
func nextToken(s string) (token, rest string) {
	i := 0
	for ; i < len(s); i++ {
		if !isTokenOctet[s[i]] {
			break
		}
	}
	return s[:i], s[i:]
}

//go:norace
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
