package nbhttp

import (
	"io"
	"net"
	"net/http"
	"net/url"
	"runtime"
	"strings"
	"sync"
	"time"
	"unsafe"

	"github.com/lesismal/llib/std/crypto/tls"
	"github.com/lesismal/nbio"
	"github.com/lesismal/nbio/logging"
	"github.com/lesismal/nbio/mempool"
)

type resHandler struct {
	c net.Conn
	t time.Time
	h func(res *http.Response, conn net.Conn, err error)
}

// ClientConn .
type ClientConn struct {
	mux      sync.Mutex
	conn     net.Conn
	handlers []resHandler

	closed bool

	onClose func()

	Engine *Engine

	Jar http.CookieJar

	Timeout time.Duration

	IdleConnTimeout time.Duration

	TLSClientConfig *tls.Config

	Proxy func(*http.Request) (*url.URL, error)

	CheckRedirect func(req *http.Request, via []*http.Request) error
}

// Reset .
func (c *ClientConn) Reset() {
	c.mux.Lock()
	if c.closed {
		c.conn = nil
		c.handlers = nil
		c.closed = false
	}
	c.mux.Unlock()
}

// OnClose .
func (c *ClientConn) OnClose(h func()) {
	if h == nil {
		return
	}

	pre := c.onClose
	c.onClose = func() {
		if pre != nil {
			pre()
		}
		h()
	}
}

// Close .
func (c *ClientConn) Close() {
	c.CloseWithError(io.EOF)
}

// CloseWithError .
func (c *ClientConn) CloseWithError(err error) {
	c.mux.Lock()
	defer c.mux.Unlock()
	if !c.closed {
		c.closed = true
		c.closeWithErrorWithoutLock(err)
	}
}

func (c *ClientConn) closeWithErrorWithoutLock(err error) {
	if err == nil {
		err = io.EOF
	}
	for _, h := range c.handlers {
		h.h(nil, c.conn, err)
	}
	c.handlers = nil
	if c.conn != nil {
		c.conn.Close()
		c.conn = nil
	}
	if c.onClose != nil {
		c.onClose()
	}
}

func (c *ClientConn) onResponse(res *http.Response, err error) {
	c.mux.Lock()
	defer c.mux.Unlock()

	if !c.closed && len(c.handlers) > 0 {
		head := c.handlers[0]
		head.h(res, c.conn, err)

		c.handlers = c.handlers[1:]
		if len(c.handlers) > 0 {
			next := c.handlers[0]
			timeout := c.Timeout
			deadline := next.t.Add(timeout)
			if timeout > 0 {
				if time.Now().After(deadline) {
					c.closeWithErrorWithoutLock(ErrClientTimeout)
				}
			} else {
				c.conn.SetReadDeadline(deadline)
			}
		} else {
			if c.IdleConnTimeout > 0 {
				c.conn.SetReadDeadline(time.Now().Add(c.IdleConnTimeout))
			} else {
				c.conn.SetReadDeadline(time.Time{})
			}
		}
		if len(c.handlers) == 0 {
			c.handlers = nil
		}
	}
}

// Do .
func (c *ClientConn) Do(req *http.Request, handler func(res *http.Response, conn net.Conn, err error)) {
	c.mux.Lock()
	defer func() {
		c.mux.Unlock()
		if err := recover(); err != nil {
			const size = 64 << 10
			buf := make([]byte, size)
			buf = buf[:runtime.Stack(buf, false)]
			logging.Error("ClientConn Do failed: %v\n%v\n", err, *(*string)(unsafe.Pointer(&buf)))
		}
	}()

	if c.closed {
		handler(nil, nil, ErrClientClosed)
		return
	}

	var engine = c.Engine
	var confTimeout = c.Timeout

	c.handlers = append(c.handlers, resHandler{c: c.conn, t: time.Now(), h: handler})

	var deadline time.Time
	if confTimeout > 0 {
		deadline = time.Now().Add(confTimeout)
	}

	sendRequest := func() {
		if c.Engine.WriteTimeout > 0 {
			c.conn.SetWriteDeadline(time.Now().Add(c.Engine.WriteTimeout))
		}
		err := req.Write(c.conn)
		if err != nil {
			c.closeWithErrorWithoutLock(err)
			return
		}
	}

	if c.conn != nil {
		if confTimeout > 0 && len(c.handlers) == 1 {
			c.conn.SetReadDeadline(deadline)
		}
		sendRequest()
	} else {
		var timeout time.Duration
		if confTimeout > 0 {
			timeout = time.Until(deadline)
			if timeout <= 0 {
				c.closeWithErrorWithoutLock(ErrClientTimeout)
				return
			}
		}

		strs := strings.Split(req.URL.Host, ":")
		host := strs[0]
		port := req.URL.Scheme
		if len(strs) >= 2 {
			port = strs[1]
		}
		addr := host + ":" + port

		var netDial netDialerFunc
		if confTimeout <= 0 {
			netDial = func(network, addr string) (net.Conn, error) {
				return net.Dial(network, addr)
			}
		} else {
			netDial = func(network, addr string) (net.Conn, error) {
				conn, err := net.DialTimeout(network, addr, timeout)
				if err == nil {
					conn.SetReadDeadline(deadline)
				}
				return conn, err
			}
		}

		if c.Proxy != nil {
			proxyURL, err := c.Proxy(req)
			if err != nil {
				c.closeWithErrorWithoutLock(err)
				return
			}
			if proxyURL != nil {
				dialer, err := proxyFromURL(proxyURL, netDial)
				if err != nil {
					c.closeWithErrorWithoutLock(err)
					return
				}
				netDial = dialer.Dial
			}
		}

		netConn, err := netDial(defaultNetwork, addr)
		if err != nil {
			c.closeWithErrorWithoutLock(err)
			return
		}

		switch req.URL.Scheme {
		case "http":
			var nbc *nbio.Conn
			nbc, err = nbio.NBConn(netConn)
			if err != nil {
				c.closeWithErrorWithoutLock(err)
				return
			}

			c.conn = nbc
			processor := NewClientProcessor(c, c.onResponse)
			parser := NewParser(processor, true, engine.ReadLimit, nbc.Execute)
			parser.Conn = nbc
			parser.Engine = engine
			parser.OnClose(func(p *Parser, err error) {
				c.CloseWithError(err)
			})
			nbc.SetSession(parser)

			nbc.OnData(engine.DataHandler)
			engine.AddConn(nbc)
		case "https":
			tlsConfig := c.TLSClientConfig
			if tlsConfig == nil {
				tlsConfig = &tls.Config{}
			} else {
				tlsConfig = tlsConfig.Clone()
			}
			if tlsConfig.ServerName == "" {
				tlsConfig.ServerName = host
			}
			tlsConn := tls.NewConn(netConn, tlsConfig, true, false, mempool.DefaultMemPool)
			err = tlsConn.Handshake()
			if err != nil {
				c.closeWithErrorWithoutLock(err)
				return
			}
			if !tlsConfig.InsecureSkipVerify {
				if err := tlsConn.VerifyHostname(tlsConfig.ServerName); err != nil {
					c.closeWithErrorWithoutLock(err)
					return
				}
			}

			nbc, err := nbio.NBConn(tlsConn.Conn())
			if err != nil {
				c.closeWithErrorWithoutLock(err)
				return
			}

			isNonblock := true
			tlsConn.ResetConn(nbc, isNonblock)

			c.conn = tlsConn
			processor := NewClientProcessor(c, c.onResponse)
			parser := NewParser(processor, true, engine.ReadLimit, nbc.Execute)
			parser.Conn = tlsConn
			parser.Engine = engine
			parser.OnClose(func(p *Parser, err error) {
				c.CloseWithError(err)
			})
			nbc.SetSession(parser)

			nbc.OnData(engine.TLSDataHandler)
			_, err = engine.AddConn(nbc)
			if err != nil {
				c.closeWithErrorWithoutLock(err)
				return
			}
		default:
			c.closeWithErrorWithoutLock(ErrClientUnsupportedSchema)
			return
		}

		sendRequest()
	}
}
