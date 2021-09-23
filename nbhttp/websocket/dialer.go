package websocket

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/url"
	"strings"

	"github.com/lesismal/llib/std/crypto/tls"
	"github.com/lesismal/nbio"
	"github.com/lesismal/nbio/nbhttp"
)

// ErrBadHandshake is returned when the server response to opening handshake is
// invalid.
// var ErrBadHandshake = errors.New("websocket: bad handshake")

// var errInvalidCompression = errors.New("websocket: invalid compression negotiation")

type Dialer struct {
	Proxy func(*http.Request) (*url.URL, error)

	TLSClientConfig *tls.Config

	Subprotocols []string

	EnableCompression bool

	Jar http.CookieJar

	Engine *nbhttp.Engine
}

// Dial creates a new client connection by calling DialContext with a background context.
func (d *Dialer) Dial(urlStr string, requestHeader http.Header, upgrader *Upgrader) (*Conn, *http.Response, error) {
	return d.DialContext(context.Background(), urlStr, requestHeader, upgrader)
}

var DefaultDialer = &Dialer{
	Proxy: http.ProxyFromEnvironment,
}

func (d *Dialer) DialContext(ctx context.Context, urlStr string, requestHeader http.Header, upgrader *Upgrader) (*Conn, *http.Response, error) {
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

	req.Header["Upgrade"] = []string{"websocket"}
	req.Header["Connection"] = []string{"Upgrade"}
	req.Header["Sec-WebSocket-Key"] = []string{challengeKey}
	req.Header["Sec-WebSocket-Version"] = []string{"13"}
	if len(d.Subprotocols) > 0 {
		req.Header["Sec-WebSocket-Protocol"] = []string{strings.Join(d.Subprotocols, ", ")}
	}
	for k, vs := range requestHeader {
		switch {
		case k == "Host":
			if len(vs) > 0 {
				req.Host = vs[0]
			}
		case k == "Upgrade" ||
			k == "Connection" ||
			k == "Sec-Websocket-Key" ||
			k == "Sec-Websocket-Version" ||
			k == "Sec-Websocket-Extensions" ||
			(k == "Sec-Websocket-Protocol" && len(d.Subprotocols) > 0):
			return nil, nil, errors.New("websocket: duplicate header not allowed: " + k)
		case k == "Sec-Websocket-Protocol":
			req.Header["Sec-WebSocket-Protocol"] = vs
		default:
			req.Header[k] = vs
		}
	}

	if d.EnableCompression {
		req.Header["Sec-WebSocket-Extensions"] = []string{"permessage-deflate; server_no_context_takeover; client_no_context_takeover"}
	}

	httpCli := nbhttp.NewClient(d.Engine)

	var wsConn *Conn
	var res *http.Response
	var errCh = make(chan error, 1)
	httpCli.Do(req, d.TLSClientConfig, func(resp *http.Response, conn net.Conn, err error) {
		res = resp

		if err != nil {
			errCh <- err
			return
		}

		nbc, ok := conn.(*nbio.Conn)
		if !ok {
			tlsConn, tlsOk := conn.(*tls.Conn)
			if !tlsOk {
				errCh <- ErrBadHandshake
				return
			}
			nbc, tlsOk = tlsConn.Conn().(*nbio.Conn)
			if !tlsOk {
				errCh <- errors.New(http.StatusText(http.StatusInternalServerError))
				return
			}
		}

		parser, ok := nbc.Session().(*nbhttp.Parser)
		if !ok {
			errCh <- errors.New(http.StatusText(http.StatusInternalServerError))
			return
		}

		parser.Upgrader = upgrader

		if d.Jar != nil {
			if rc := resp.Cookies(); len(rc) > 0 {
				d.Jar.SetCookies(req.URL, rc)
			}
		}

		remoteCompressionEnabled := false
		if resp == nil {
			panic("response should not be nil")
		}
		if resp.StatusCode != 101 ||
			!headerContains(resp.Header, "Upgrade", "websocket") ||
			!headerContains(resp.Header, "Connection", "upgrade") ||
			resp.Header.Get("Sec-Websocket-Accept") != acceptKeyString(challengeKey) {
			errCh <- ErrBadHandshake
			return
		}

		for _, ext := range parseExtensions(resp.Header) {
			if ext[""] != "permessage-deflate" {
				continue
			}
			_, snct := ext["server_no_context_takeover"]
			_, cnct := ext["client_no_context_takeover"]
			if !snct || !cnct {
				errCh <- ErrInvalidCompression
				return
			}

			remoteCompressionEnabled = true
			break
		}

		wsConn = newConn(upgrader, conn, resp.Header.Get("Sec-Websocket-Protocol"), remoteCompressionEnabled)
		wsConn.Engine = d.Engine
		wsConn.OnClose(upgrader.onClose)

		upgrader.conn = wsConn
		upgrader.Engine = parser.Engine

		if upgrader.openHandler != nil {
			upgrader.openHandler(wsConn)
		}

		errCh <- nil
	})

	select {
	case err = <-errCh:
	case <-ctx.Done():
		err = nbhttp.ErrClientTimeout
	}
	if err != nil {
		httpCli.CloseWithError(err)
	}
	return wsConn, res, err
}
