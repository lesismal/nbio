package websocket

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/lesismal/llib/std/crypto/tls"
	"github.com/lesismal/nbio"
	"github.com/lesismal/nbio/nbhttp"
)

const (
	hostHeaderField                = "Host"
	upgradeHeaderField             = "Upgrade"
	connectionHeaderField          = "Connection"
	secWebsocketKeyHeaderField     = "Sec-Websocket-Key"
	secWebsocketVersionHeaderField = "Sec-Websocket-Version"
	secWebsocketExtHeaderField     = "Sec-Websocket-Extensions"
	secWebsocketProtoHeaderField   = "Sec-Websocket-Protocol"
)

// Dialer .
type Dialer struct {
	Engine *nbhttp.Engine

	Options  *Options
	Upgrader *Upgrader

	Jar http.CookieJar

	DialTimeout time.Duration

	TLSClientConfig *tls.Config

	Proxy func(*http.Request) (*url.URL, error)

	CheckRedirect func(req *http.Request, via []*http.Request) error

	Subprotocols []string

	EnableCompression bool

	Cancel context.CancelFunc
}

// Dial .
//
//go:norace
func (d *Dialer) Dial(urlStr string, requestHeader http.Header, v ...interface{}) (*Conn, *http.Response, error) {
	ctx := context.Background()
	if d.DialTimeout > 0 {
		ctx, d.Cancel = context.WithTimeout(ctx, d.DialTimeout)
	}
	return d.DialContext(ctx, urlStr, requestHeader, v...)
}

// DialContext .
//
//go:norace
func (d *Dialer) DialContext(ctx context.Context, urlStr string, requestHeader http.Header, v ...interface{}) (*Conn, *http.Response, error) {
	if d.Cancel != nil {
		defer d.Cancel()
	}

	options := d.Options
	if options == nil {
		options = d.Upgrader
	}
	if options == nil {
		return nil, nil, errors.New("invalid Options: nil")
	}

	challengeKey, err := challengeKey()
	if err != nil {
		return nil, nil, err
	}

	u, err := url.Parse(urlStr)
	if err != nil {
		return nil, nil, err
	}

	switch u.Scheme {
	case "ws":
		u.Scheme = "http"
	case "wss":
		u.Scheme = "https"
	default:
		return nil, nil, ErrMalformedURL
	}

	if u.User != nil {
		return nil, nil, ErrMalformedURL
	}

	req := &http.Request{
		Method:     "GET",
		URL:        u,
		Proto:      "HTTP/1.1",
		ProtoMajor: 1,
		ProtoMinor: 1,
		Header:     make(http.Header),
		Host:       u.Host,
	}

	if d.Jar != nil {
		for _, cookie := range d.Jar.Cookies(u) {
			req.AddCookie(cookie)
		}
	}

	req.Header[upgradeHeaderField] = []string{"websocket"}
	req.Header[connectionHeaderField] = []string{"Upgrade"}
	req.Header[secWebsocketKeyHeaderField] = []string{challengeKey}
	req.Header[secWebsocketVersionHeaderField] = []string{"13"}
	if len(d.Subprotocols) > 0 {
		req.Header[secWebsocketProtoHeaderField] = []string{strings.Join(d.Subprotocols, ", ")}
	}
	for k, vs := range requestHeader {
		switch {
		case k == hostHeaderField:
			if len(vs) > 0 {
				req.Host = vs[0]
			}
		case k == upgradeHeaderField ||
			k == connectionHeaderField ||
			k == secWebsocketKeyHeaderField ||
			k == secWebsocketVersionHeaderField ||
			k == secWebsocketExtHeaderField ||
			(k == secWebsocketProtoHeaderField && len(d.Subprotocols) > 0):
			return nil, nil, errors.New("websocket: duplicate header not allowed: " + k)
		case k == secWebsocketProtoHeaderField:
			req.Header[secWebsocketProtoHeaderField] = vs
		default:
			req.Header[k] = vs
		}
	}

	if options.enableCompression {
		req.Header[secWebsocketExtHeaderField] = []string{"permessage-deflate; server_no_context_takeover; client_no_context_takeover"}
	}

	var asyncHandler func(*Conn, *http.Response, error)
	if len(v) > 0 {
		if h, ok := v[0].(func(*Conn, *http.Response, error)); ok {
			asyncHandler = h
		}
	}

	var wsConn *Conn
	var res *http.Response
	var errCh chan error
	if asyncHandler == nil {
		errCh = make(chan error, 1)
	}

	cliConn := &nbhttp.ClientConn{
		Engine:          d.Engine,
		Jar:             d.Jar,
		Timeout:         d.DialTimeout,
		TLSClientConfig: d.TLSClientConfig,
		Proxy:           d.Proxy,
		CheckRedirect:   d.CheckRedirect,
	}
	cliConn.Do(req, func(resp *http.Response, conn net.Conn, err error) {
		res = resp

		notifyResult := func(e error) {
			if asyncHandler == nil {
				select {
				case errCh <- e:
				case <-ctx.Done():
					if conn != nil {
						_ = conn.Close()
					}
				}
			} else {
				d.Engine.Execute(func() {
					asyncHandler(wsConn, res, e)
				})
			}
		}

		if err != nil {
			notifyResult(err)
			return
		}

		nbc, ok := conn.(*nbio.Conn)
		if !ok {
			nbhttpConn, ok2 := conn.(*nbhttp.Conn)
			if !ok2 {
				err = ErrBadHandshake
				notifyResult(err)
				return
			}
			tlsConn, tlsOk := nbhttpConn.Conn.(*tls.Conn)
			if !tlsOk {
				err = ErrBadHandshake
				notifyResult(err)
				return
			}
			nbc, tlsOk = tlsConn.Conn().(*nbio.Conn)
			if !tlsOk {
				err = errors.New(http.StatusText(http.StatusInternalServerError))
				notifyResult(err)
				return
			}
		}

		parser, ok := nbc.Session().(*nbhttp.Parser)
		if !ok {
			err = errors.New(http.StatusText(http.StatusInternalServerError))
			notifyResult(err)
			return
		}

		if d.Jar != nil {
			if rc := resp.Cookies(); len(rc) > 0 {
				d.Jar.SetCookies(req.URL, rc)
			}
		}

		remoteCompressionEnabled := false
		if resp.StatusCode != 101 ||
			!headerContains(resp.Header, "Upgrade", "websocket") ||
			!headerContains(resp.Header, "Connection", "upgrade") ||
			resp.Header.Get("Sec-Websocket-Accept") != acceptKeyString(challengeKey) {
			err = ErrBadHandshake
			notifyResult(err)
			return
		}

		for _, ext := range parseExtensions(resp.Header) {
			if ext[""] != "permessage-deflate" {
				continue
			}
			_, snct := ext["server_no_context_takeover"]
			_, cnct := ext["client_no_context_takeover"]
			if !snct || !cnct {
				err = ErrInvalidCompression
				notifyResult(err)
				return
			}

			remoteCompressionEnabled = true
			break
		}

		wsConn = NewClientConn(options, conn, resp.Header.Get(secWebsocketProtoHeaderField), remoteCompressionEnabled, false)
		parser.ParserCloser = wsConn
		wsConn.Engine = parser.Engine
		wsConn.Execute = parser.Execute
		nbc.SetSession(wsConn)

		if wsConn.openHandler != nil {
			wsConn.openHandler(wsConn)
		}

		notifyResult(err)
	})

	if asyncHandler == nil {
		select {
		case err = <-errCh:
		case <-ctx.Done():
			err = nbhttp.ErrClientTimeout
		}
		if err != nil {
			cliConn.CloseWithError(err)
		}
		return wsConn, res, err
	}

	return nil, nil, nil
}
