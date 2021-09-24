package nbhttp

import (
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/lesismal/llib/std/crypto/tls"
	"github.com/lesismal/nbio"
	"github.com/lesismal/nbio/mempool"
)

type resHandler struct {
	c net.Conn
	t time.Time
	h func(res *http.Response, conn net.Conn, err error)
}

type httpConn struct {
	mux      sync.Mutex
	cli      *Client
	conn     net.Conn
	handlers []resHandler
	executor func(f func())

	closed bool

	Timeout         time.Duration
	IdleConnTimeout time.Duration
}

func (c *httpConn) Close() {
	c.mux.Lock()
	closed := c.closed
	c.closed = true
	c.mux.Unlock()
	if !closed {
		c.closeWithErrorWithoutLock(io.EOF)
	}
}

func (c *httpConn) CloseWithError(err error) {
	c.mux.Lock()
	closed := c.closed
	c.closed = true
	c.mux.Unlock()
	if !closed {
		c.closeWithErrorWithoutLock(err)
	}
}

func (c *httpConn) closeWithErrorWithoutLock(err error) {
	for _, h := range c.handlers {
		h.h(nil, c.conn, err)
	}
	c.handlers = nil
	if c.conn != nil {
		c.conn.Close()
	}
}

func (c *httpConn) onResponse(res *http.Response, err error) {
	c.mux.Lock()
	if c.closed {
		c.mux.Unlock()
		return
	}
	defer c.mux.Unlock()

	if len(c.handlers) > 0 {
		head := c.handlers[0]
		head.h(res, c.conn, err)
		c.handlers = c.handlers[1:]

		if len(c.handlers) > 0 {
			head = c.handlers[0]
			timeout := c.cli.Timeout
			deadline := head.t.Add(timeout)
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

func (c *httpConn) Do(req *http.Request, handler func(res *http.Response, conn net.Conn, err error)) {
	c.mux.Lock()
	if c.closed {
		defer c.mux.Unlock()
		handler(nil, nil, ErrClientClosed)
		return
	}

	var engine = c.cli.Engine
	var confTimeout = c.cli.Timeout

	var deadline time.Time
	if confTimeout > 0 {
		deadline = time.Now().Add(confTimeout)
	}

	// originHandler := handler
	// handler = func(res *http.Response, conn net.Conn, err error) {
	// 	if err == nil && confTimeout > 0 && conn != nil {
	// 		if c.IdleConnTimeout > 0 {
	// 			conn.SetReadDeadline(time.Now().Add(c.IdleConnTimeout))
	// 		} else {
	// 			conn.SetReadDeadline(time.Time{})
	// 		}
	// 	}
	// 	originHandler(res, conn, err)
	// }

	c.handlers = append(c.handlers, resHandler{c: c.conn, t: time.Now(), h: handler})

	sendRequest := func() {
		err := req.Write(c.conn)
		if err != nil {
			c.closeWithErrorWithoutLock(err)
			return
		}
	}

	if c.conn != nil {
		defer c.mux.Unlock()
		sendRequest()
	} else {
		engine.ExecuteClient(func() {
			defer c.mux.Unlock()

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

			if c.cli.Proxy != nil {
				proxyURL, err := c.cli.Proxy(req)
				if err != nil {
					c.closeWithErrorWithoutLock(err)
					return
				}
				if proxyURL != nil {
					dialer, err := proxy_FromURL(proxyURL, netDial)
					if err != nil {
						c.closeWithErrorWithoutLock(err)
						return
					}
					netDial = dialer.Dial
				}
			}

			netConn, err := netDial("tcp", addr)
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
				// c.executor = nbc.Execute
				processor := NewClientProcessor(c, c.onResponse)
				parser := NewParser(processor, true, engine.ReadLimit, nbc.Execute)
				parser.Conn = nbc
				parser.Engine = engine
				parser.OnClose(func(p *Parser, err error) {
					c.CloseWithError(err)
				})
				// processor.(*ClientProcessor).parser = parser
				nbc.SetSession(parser)

				nbc.OnData(engine.DataHandler)
				engine.AddConn(nbc)
			case "https":
				tlsConfig := c.cli.TLSClientConfig
				if tlsConfig == nil {
					tlsConfig = &tls.Config{}
				} else {
					tlsConfig = tlsConfig.Clone()
				}
				tlsConfig.ServerName = req.URL.Host
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
				// c.executor = nbc.Execute
				processor := NewClientProcessor(c, c.onResponse)
				parser := NewParser(processor, true, engine.ReadLimit, nbc.Execute)
				parser.Conn = tlsConn
				parser.Engine = engine
				parser.OnClose(func(p *Parser, err error) {
					c.CloseWithError(err)
				})
				// processor.(*ClientProcessor).parser = parser
				nbc.SetSession(parser)

				nbc.OnData(engine.DataHandlerTLS)
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
		})
	}
}
