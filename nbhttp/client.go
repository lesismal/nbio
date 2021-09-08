package nbhttp

import (
	"io"
	"log"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/lesismal/llib/std/crypto/tls"
	"github.com/lesismal/nbio"
	"github.com/lesismal/nbio/mempool"
)

type Client struct {
	Conn net.Conn

	Engine *Engine

	Transport http.RoundTripper

	CheckRedirect func(req *http.Request, via []*http.Request) error

	Jar http.CookieJar

	Timeout time.Duration

	mux      sync.Mutex
	handlers []func(res *http.Response, conn net.Conn, err error)
}

func (c *Client) Close() {
	c.mux.Lock()
	defer c.mux.Unlock()
	for _, h := range c.handlers {
		h(nil, c.Conn, io.EOF)
	}
	c.handlers = nil
}

func (c *Client) CloseWithError(err error) {
	c.mux.Lock()
	defer c.mux.Unlock()
	for _, h := range c.handlers {
		h(nil, c.Conn, err)
	}
	c.handlers = nil
}

func (c *Client) onResponse(res *http.Response, err error) {
	c.mux.Lock()
	defer c.mux.Unlock()

	switch len(c.handlers) {
	case 0:
	case 1:
		c.handlers[0](res, c.Conn, err)
		c.handlers = nil
	default:
		c.handlers[0](res, c.Conn, err)
		c.handlers = c.handlers[1:]
	}
}

func (c *Client) Do(req *http.Request, handler func(res *http.Response, conn net.Conn, err error)) {
	sendRequest := func() {
		err := req.Write(c.Conn)
		if err != nil {
			handler(nil, c.Conn, err)
			return
		}
		c.handlers = append(c.handlers, handler)
	}

	c.mux.Lock()
	if c.Conn == nil {
		c.Engine.ExecuteClient(func() {
			defer c.mux.Unlock()
			strs := strings.Split(req.URL.Host, ":")
			host := strs[0]
			port := req.URL.Scheme
			if len(strs) >= 2 {
				port = strs[1]
			}
			addr := host + ":" + port
			switch req.URL.Scheme {
			case "http":
				var err error
				var conn net.Conn
				if c.Timeout <= 0 {
					conn, err = net.Dial("tcp", addr)
				} else {
					conn, err = net.DialTimeout("tcp", addr, c.Timeout)
				}
				if err != nil {
					handler(nil, c.Conn, err)
					return
				}

				nbc, err := nbio.NBConn(conn)
				if err != nil {
					handler(nil, c.Conn, err)
					return
				}

				processor := NewClientProcessor(c, c.onResponse)
				parser := NewParser(processor, true, c.Engine.ReadLimit, nbc.Execute)
				parser.Conn = nbc
				parser.Engine = c.Engine
				nbc.SetSession(parser)

				c.Conn, _ = c.Engine.AddConn(nbc)
				nbc.OnData(c.Engine.DataHandler)

			case "https":
				var err error
				var tlsConn *tls.Conn
				var tlsConfig = &tls.Config{
					ServerName: req.URL.Host,
					// InsecureSkipVerify: true,
				}
				if c.Timeout <= 0 {
					tlsConn, err = tls.Dial("tcp", addr, tlsConfig, mempool.DefaultMemPool)
				} else {
					tlsConn, err = tls.DialWithDialer(&net.Dialer{Timeout: c.Timeout}, "tcp", addr, tlsConfig, mempool.DefaultMemPool)
				}
				if err != nil {
					log.Fatalf("Dial failed: %v\n", err)
				}

				nbc, err := nbio.NBConn(tlsConn.Conn())
				if err != nil {
					handler(nil, c.Conn, err)
					return
				}

				isNonblock := true
				tlsConn.ResetConn(nbc, isNonblock)

				processor := NewClientProcessor(c, c.onResponse)
				parser := NewParser(processor, true, c.Engine.ReadLimit, nbc.Execute)
				parser.Conn = tlsConn
				parser.Engine = c.Engine
				nbc.SetSession(parser)

				c.Conn = tlsConn
				nbc.OnData(c.Engine.DataHandlerTLS)
				c.Engine.AddConn(nbc)

			default:
				handler(nil, c.Conn, ErrClientUnsupportedSchema)
				return
			}

			sendRequest()

		})
	} else {
		defer c.mux.Unlock()
		sendRequest()
	}
}

func NewClient(engine *Engine) *Client {
	return &Client{
		Engine: engine,
	}
}
