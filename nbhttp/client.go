package nbhttp

import (
	"bufio"
	"encoding/base64"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/lesismal/llib/std/crypto/tls"
	"github.com/lesismal/nbio"
	"github.com/lesismal/nbio/mempool"
)

type Client struct {
	mux sync.Mutex

	Conn *httpConn

	Engine *Engine

	Transport http.RoundTripper

	CheckRedirect func(req *http.Request, via []*http.Request) error

	Jar http.CookieJar

	Timeout                     time.Duration
	MaxConcurrencyPerConnection int
	MaxIdleConns                int
	MaxIdleConnsPerHost         int
	MaxConnsPerHost             int
	IdleConnTimeout             time.Duration

	// http.Transport
	Proxy func(*http.Request) (*url.URL, error)
	// DialContext func(ctx context.Context, network, addr string) (net.Conn, error)
	// Dial            func(network, addr string) (net.Conn, error)
	// DialTLSContext  func(ctx context.Context, network, addr string) (net.Conn, error)
	// DialTLS         func(network, addr string) (net.Conn, error)
	// TLSClientConfig *tls.Config
	// TLSHandshakeTimeout time.Duration
	// DisableKeepAlives bool
	// DisableCompression bool
	// MaxIdleConns int
	// MaxIdleConnsPerHost int
	// MaxConnsPerHost int
	// IdleConnTimeout time.Duration
	// ResponseHeaderTimeout time.Duration
	// ExpectContinueTimeout time.Duration
	// TLSNextProto map[string]func(authority string, c *tls.Conn) RoundTripper
	// ProxyConnectHeader http.Header
	// GetProxyConnectHeader func(ctx context.Context, proxyURL *url.URL, target string) (http.Header, error)

}

func (c *Client) Close() {
	c.mux.Lock()
	defer c.mux.Unlock()
	if c.Conn != nil {
		c.Conn.Close()
	}
}

func (c *Client) CloseWithError(err error) {
	c.mux.Lock()
	defer c.mux.Unlock()
	if c.Conn != nil {
		c.Conn.CloseWithError(err)
	}
}

type resHandler struct {
	c net.Conn
	t time.Time
	h func(res *http.Response, conn net.Conn, err error)
}

type httpConn struct {
	cli      *Client
	conn     net.Conn
	mux      sync.Mutex
	handlers []resHandler
	executor func(f func())
}

func (c *httpConn) Close() {
	c.mux.Lock()
	defer c.mux.Unlock()
	for _, h := range c.handlers {
		h.h(nil, c.conn, io.EOF)
	}
	c.handlers = nil
	if c.conn != nil {
		c.conn.Close()
	}
}

func (c *httpConn) CloseWithError(err error) {
	c.mux.Lock()
	defer c.mux.Unlock()
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
	defer c.mux.Unlock()

	if len(c.handlers) > 0 {
		head := c.handlers[0]
		head.h(res, c.conn, err)
		c.handlers = c.handlers[1:]
		if c.cli.Timeout > 0 {
			if len(c.handlers) > 0 {
				now := time.Now()
				if len(c.handlers) > 0 {
					head = c.handlers[0]
					deadline := head.t.Add(c.cli.Timeout)
					if deadline.Before(now) {
						c.executor(func() {
							c.CloseWithError(ErrClientTimeout)
						})
					} else {
						c.conn.SetReadDeadline(deadline)
					}
				}
			} else {
				c.conn.SetReadDeadline(time.Time{})
			}
		}
		if len(c.handlers) == 0 {
			c.handlers = nil
		}
	}
}

func (c *Client) Do(req *http.Request, tlsConfig *tls.Config, handler func(res *http.Response, conn net.Conn, err error)) {
	originHandler := handler
	handler = func(res *http.Response, conn net.Conn, err error) {
		if err == nil && c.Timeout > 0 && conn != nil {
			conn.SetReadDeadline(time.Time{})
		}
		originHandler(res, conn, err)
	}
	sendRequest := func() {
		err := req.Write(c.Conn.conn)
		if err != nil {
			handler(nil, nil, err)
			return
		}
		c.Conn.handlers = append(c.Conn.handlers, resHandler{c: c.Conn.conn, t: time.Now(), h: handler})
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

			var netDial netDialerFunc
			if c.Timeout <= 0 {
				netDial = func(network, addr string) (net.Conn, error) {
					return net.Dial(network, addr)
				}
			} else {
				netDial = func(network, addr string) (net.Conn, error) {
					conn, err := net.DialTimeout(network, addr, c.Timeout)
					if err == nil {
						conn.SetReadDeadline(time.Now().Add(c.Timeout))
					}
					return conn, err
				}
			}

			if c.Proxy != nil {
				proxyURL, err := c.Proxy(req)
				if err != nil {
					handler(nil, nil, err)
					return
				}
				if proxyURL != nil {
					dialer, err := proxy_FromURL(proxyURL, netDialerFunc(netDial))
					if err != nil {
						handler(nil, nil, err)
						return
					}
					netDial = dialer.Dial
				}
			}

			netConn, err := netDial("tcp", addr)
			if err != nil {
				handler(nil, nil, err)
				return
			}

			switch req.URL.Scheme {
			case "http":
				nbc, err := nbio.NBConn(netConn)
				if err != nil {
					handler(nil, nil, err)
					return
				}

				c.Conn = &httpConn{cli: c, conn: nbc, executor: nbc.Execute}
				processor := NewClientProcessor(c.Conn, c.Conn.onResponse)
				parser := NewParser(processor, true, c.Engine.ReadLimit, nbc.Execute)
				parser.Conn = nbc
				parser.Engine = c.Engine
				nbc.SetSession(parser)

				nbc.OnData(c.Engine.DataHandler)
				c.Engine.AddConn(nbc)
			case "https":
				if tlsConfig == nil {
					tlsConfig = &tls.Config{}
				} else {
					tlsConfig = tlsConfig.Clone()
				}
				tlsConfig.ServerName = req.URL.Host
				tlsConn := tls.NewConn(netConn, tlsConfig, true, false, mempool.DefaultMemPool)
				err = tlsConn.Handshake()
				if err != nil {
					handler(nil, nil, err)
					return
				}
				if !tlsConfig.InsecureSkipVerify {
					if err := tlsConn.VerifyHostname(tlsConfig.ServerName); err != nil {
						handler(nil, nil, err)
						return
					}
				}

				nbc, err := nbio.NBConn(tlsConn.Conn())
				if err != nil {
					handler(nil, nil, err)
					return
				}

				isNonblock := true
				tlsConn.ResetConn(nbc, isNonblock)

				c.Conn = &httpConn{cli: c, conn: tlsConn}
				processor := NewClientProcessor(c.Conn, c.Conn.onResponse)
				parser := NewParser(processor, true, c.Engine.ReadLimit, nbc.Execute)
				parser.Conn = tlsConn
				parser.Engine = c.Engine
				nbc.SetSession(parser)

				nbc.OnData(c.Engine.DataHandlerTLS)
				_, err = c.Engine.AddConn(nbc)
				if err != nil {
					handler(nil, nil, err)
					return
				}
			default:
				handler(nil, c.Conn.conn, ErrClientUnsupportedSchema)
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

type netDialerFunc func(network, addr string) (net.Conn, error)

func (fn netDialerFunc) Dial(network, addr string) (net.Conn, error) {
	return fn(network, addr)
}

var proxy_proxySchemes map[string]func(*url.URL, proxy_Dialer) (proxy_Dialer, error)

func proxy_RegisterDialerType(scheme string, f func(*url.URL, proxy_Dialer) (proxy_Dialer, error)) {
	if proxy_proxySchemes == nil {
		proxy_proxySchemes = make(map[string]func(*url.URL, proxy_Dialer) (proxy_Dialer, error))
	}
	proxy_proxySchemes[scheme] = f
}
func proxy_FromURL(u *url.URL, forward proxy_Dialer) (proxy_Dialer, error) {
	var auth *proxy_Auth
	if u.User != nil {
		auth = new(proxy_Auth)
		auth.User = u.User.Username()
		if p, ok := u.User.Password(); ok {
			auth.Password = p
		}
	}

	switch u.Scheme {
	case "socks5":
		return proxy_SOCKS5("tcp", u.Host, auth, forward)
	}

	if proxy_proxySchemes != nil {
		if f, ok := proxy_proxySchemes[u.Scheme]; ok {
			return f(u, forward)
		}
	}

	return nil, errors.New("proxy: unknown scheme: " + u.Scheme)
}

func hostPortNoPort(u *url.URL) (hostPort, hostNoPort string) {
	hostPort = u.Host
	hostNoPort = u.Host
	if i := strings.LastIndex(u.Host, ":"); i > strings.LastIndex(u.Host, "]") {
		hostNoPort = hostNoPort[:i]
	} else {
		switch u.Scheme {
		case "wss":
			hostPort += ":443"
		case "https":
			hostPort += ":443"
		default:
			hostPort += ":80"
		}
	}
	return hostPort, hostNoPort
}

type proxy_Dialer interface {
	Dial(network, addr string) (c net.Conn, err error)
}

type httpProxyDialer struct {
	proxyURL    *url.URL
	forwardDial func(network, addr string) (net.Conn, error)
}

func (hpd *httpProxyDialer) Dial(network string, addr string) (net.Conn, error) {
	hostPort, _ := hostPortNoPort(hpd.proxyURL)
	conn, err := hpd.forwardDial(network, hostPort)
	if err != nil {
		return nil, err
	}

	connectHeader := make(http.Header)
	if user := hpd.proxyURL.User; user != nil {
		proxyUser := user.Username()
		if proxyPassword, passwordSet := user.Password(); passwordSet {
			credential := base64.StdEncoding.EncodeToString([]byte(proxyUser + ":" + proxyPassword))
			connectHeader.Set("Proxy-Authorization", "Basic "+credential)
		}
	}

	connectReq := &http.Request{
		Method: "CONNECT",
		URL:    &url.URL{Opaque: addr},
		Host:   addr,
		Header: connectHeader,
	}

	if err := connectReq.Write(conn); err != nil {
		conn.Close()
		return nil, err
	}

	br := bufio.NewReader(conn)
	resp, err := http.ReadResponse(br, connectReq)
	if err != nil {
		conn.Close()
		return nil, err
	}

	if resp.StatusCode != 200 {
		conn.Close()
		f := strings.SplitN(resp.Status, " ", 2)
		return nil, errors.New(f[1])
	}
	return conn, nil
}

type proxy_Auth struct {
	User, Password string
}

func proxy_SOCKS5(network, addr string, auth *proxy_Auth, forward proxy_Dialer) (proxy_Dialer, error) {
	s := &proxy_socks5{
		network: network,
		addr:    addr,
		forward: forward,
	}
	if auth != nil {
		s.user = auth.User
		s.password = auth.Password
	}

	return s, nil
}

type proxy_socks5 struct {
	user, password string
	network, addr  string
	forward        proxy_Dialer
}

const proxy_socks5Version = 5

const (
	proxy_socks5AuthNone     = 0
	proxy_socks5AuthPassword = 2
)

const proxy_socks5Connect = 1

const (
	proxy_socks5IP4    = 1
	proxy_socks5Domain = 3
	proxy_socks5IP6    = 4
)

var proxy_socks5Errors = []string{
	"",
	"general failure",
	"connection forbidden",
	"network unreachable",
	"host unreachable",
	"connection refused",
	"TTL expired",
	"command not supported",
	"address type not supported",
}

func (s *proxy_socks5) Dial(network, addr string) (net.Conn, error) {
	switch network {
	case "tcp", "tcp6", "tcp4":
	default:
		return nil, errors.New("proxy: no support for SOCKS5 proxy connections of type " + network)
	}

	conn, err := s.forward.Dial(s.network, s.addr)
	if err != nil {
		return nil, err
	}
	if err := s.connect(conn, addr); err != nil {
		conn.Close()
		return nil, err
	}
	return conn, nil
}

func (s *proxy_socks5) connect(conn net.Conn, target string) error {
	host, portStr, err := net.SplitHostPort(target)
	if err != nil {
		return err
	}

	port, err := strconv.Atoi(portStr)
	if err != nil {
		return errors.New("proxy: failed to parse port number: " + portStr)
	}
	if port < 1 || port > 0xffff {
		return errors.New("proxy: port number out of range: " + portStr)
	}

	// the size here is just an estimate
	buf := make([]byte, 0, 6+len(host))

	buf = append(buf, proxy_socks5Version)
	if len(s.user) > 0 && len(s.user) < 256 && len(s.password) < 256 {
		buf = append(buf, 2 /* num auth methods */, proxy_socks5AuthNone, proxy_socks5AuthPassword)
	} else {
		buf = append(buf, 1 /* num auth methods */, proxy_socks5AuthNone)
	}

	if _, err := conn.Write(buf); err != nil {
		return errors.New("proxy: failed to write greeting to SOCKS5 proxy at " + s.addr + ": " + err.Error())
	}

	if _, err := io.ReadFull(conn, buf[:2]); err != nil {
		return errors.New("proxy: failed to read greeting from SOCKS5 proxy at " + s.addr + ": " + err.Error())
	}
	if buf[0] != 5 {
		return errors.New("proxy: SOCKS5 proxy at " + s.addr + " has unexpected version " + strconv.Itoa(int(buf[0])))
	}
	if buf[1] == 0xff {
		return errors.New("proxy: SOCKS5 proxy at " + s.addr + " requires authentication")
	}

	// See RFC 1929
	if buf[1] == proxy_socks5AuthPassword {
		buf = buf[:0]
		buf = append(buf, 1 /* password protocol version */)
		buf = append(buf, uint8(len(s.user)))
		buf = append(buf, s.user...)
		buf = append(buf, uint8(len(s.password)))
		buf = append(buf, s.password...)

		if _, err := conn.Write(buf); err != nil {
			return errors.New("proxy: failed to write authentication request to SOCKS5 proxy at " + s.addr + ": " + err.Error())
		}

		if _, err := io.ReadFull(conn, buf[:2]); err != nil {
			return errors.New("proxy: failed to read authentication reply from SOCKS5 proxy at " + s.addr + ": " + err.Error())
		}

		if buf[1] != 0 {
			return errors.New("proxy: SOCKS5 proxy at " + s.addr + " rejected username/password")
		}
	}

	buf = buf[:0]
	buf = append(buf, proxy_socks5Version, proxy_socks5Connect, 0 /* reserved */)

	if ip := net.ParseIP(host); ip != nil {
		if ip4 := ip.To4(); ip4 != nil {
			buf = append(buf, proxy_socks5IP4)
			ip = ip4
		} else {
			buf = append(buf, proxy_socks5IP6)
		}
		buf = append(buf, ip...)
	} else {
		if len(host) > 255 {
			return errors.New("proxy: destination host name too long: " + host)
		}
		buf = append(buf, proxy_socks5Domain)
		buf = append(buf, byte(len(host)))
		buf = append(buf, host...)
	}
	buf = append(buf, byte(port>>8), byte(port))

	if _, err := conn.Write(buf); err != nil {
		return errors.New("proxy: failed to write connect request to SOCKS5 proxy at " + s.addr + ": " + err.Error())
	}

	if _, err := io.ReadFull(conn, buf[:4]); err != nil {
		return errors.New("proxy: failed to read connect reply from SOCKS5 proxy at " + s.addr + ": " + err.Error())
	}

	failure := "unknown error"
	if int(buf[1]) < len(proxy_socks5Errors) {
		failure = proxy_socks5Errors[buf[1]]
	}

	if len(failure) > 0 {
		return errors.New("proxy: SOCKS5 proxy at " + s.addr + " failed to connect: " + failure)
	}

	bytesToDiscard := 0
	switch buf[3] {
	case proxy_socks5IP4:
		bytesToDiscard = net.IPv4len
	case proxy_socks5IP6:
		bytesToDiscard = net.IPv6len
	case proxy_socks5Domain:
		_, err := io.ReadFull(conn, buf[:1])
		if err != nil {
			return errors.New("proxy: failed to read domain length from SOCKS5 proxy at " + s.addr + ": " + err.Error())
		}
		bytesToDiscard = int(buf[0])
	default:
		return errors.New("proxy: got unknown address type " + strconv.Itoa(int(buf[3])) + " from SOCKS5 proxy at " + s.addr)
	}

	if cap(buf) < bytesToDiscard {
		buf = make([]byte, bytesToDiscard)
	} else {
		buf = buf[:bytesToDiscard]
	}
	if _, err := io.ReadFull(conn, buf); err != nil {
		return errors.New("proxy: failed to read address from SOCKS5 proxy at " + s.addr + ": " + err.Error())
	}

	if _, err := io.ReadFull(conn, buf[:2]); err != nil {
		return errors.New("proxy: failed to read port from SOCKS5 proxy at " + s.addr + ": " + err.Error())
	}

	return nil
}

func init() {
	proxy_RegisterDialerType("http", func(proxyURL *url.URL, forwardDialer proxy_Dialer) (proxy_Dialer, error) {
		return &httpProxyDialer{proxyURL: proxyURL, forwardDial: forwardDialer.Dial}, nil
	})
}
