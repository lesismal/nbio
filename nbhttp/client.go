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
	"sync/atomic"
	"time"

	"github.com/lesismal/llib/std/crypto/tls"
)

//go:norace
func newHostConns(cli *Client) *hostConns {
	hcs := &hostConns{
		cli:        cli,
		conns:      map[*ClientConn]struct{}{},
		maxConnNum: 1024, // 1024 by default
	}
	if cli.MaxConnsPerHost > 0 {
		hcs.maxConnNum = cli.MaxConnsPerHost
	}
	hcs.chConnss = make(chan *ClientConn, hcs.maxConnNum)

	return hcs
}

type hostConns struct {
	mux        sync.Mutex
	cli        *Client
	conns      map[*ClientConn]struct{}
	connNum    int32
	maxConnNum int32
	chConnss   chan *ClientConn
}

//go:norace
func (hcs *hostConns) closeWithError(err error) {
	hcs.mux.Lock()
	for hc := range hcs.conns {
		hc.CloseWithError(err)
	}
	hcs.mux.Unlock()
}

//go:norace
func (hcs *hostConns) getConn() (*hostConns, *ClientConn, error) {
	c := hcs.cli
	if !c.closed {
		timer := time.NewTimer(c.Timeout)
		defer timer.Stop()

		// 1. Get an existing free connection.
		select {
		case hc, ok := <-hcs.chConnss:
			if !ok {
				return nil, nil, ErrClientClosed
			}
			return hcs, hc, nil
		case <-timer.C:
			return nil, nil, ErrClientTimeout
		default:
		}

		// 2. Try to create a new connection if the num of existing
		//    connections is smaller than maxConnNum
		if atomic.AddInt32(&hcs.connNum, 1) <= hcs.maxConnNum {
			hc := &ClientConn{
				Engine:          c.Engine,
				Jar:             c.Jar,
				Timeout:         c.Timeout,
				IdleConnTimeout: c.IdleConnTimeout,
				TLSClientConfig: c.TLSClientConfig,
				Dial:            c.Dial,
				Proxy:           c.Proxy,
				CheckRedirect:   c.CheckRedirect,
			}
			hcs.mux.Lock()
			hcs.conns[hc] = struct{}{}
			hcs.mux.Unlock()
			return hcs, hc, nil
		}
		atomic.AddInt32(&hcs.connNum, -1)

		// 3. Wait for an existed working connection to be free.
		select {
		case hc, ok := <-hcs.chConnss:
			if !ok {
				return nil, nil, ErrClientClosed
			}
			return hcs, hc, nil
		case <-timer.C:
			return nil, nil, ErrClientTimeout
		}
	}

	return nil, nil, ErrClientClosed
}

//go:norace
func (hcs *hostConns) releaseConn(hc *ClientConn) {
	hcs.chConnss <- hc
}

// Client implements the similar functions with std http.Client.
type Client struct {
	mux    sync.Mutex
	closed bool

	connsMux     sync.RWMutex
	connsOfHosts map[string]*hostConns

	Engine *Engine

	Jar http.CookieJar

	Timeout time.Duration

	MaxConnsPerHost int32
	IdleConnTimeout time.Duration

	TLSClientConfig *tls.Config

	Dial func(network, addr string) (net.Conn, error)

	Proxy func(*http.Request) (*url.URL, error)

	CheckRedirect func(req *http.Request, via []*http.Request) error
}

// Close closes all underlayer connections with EOF.
//
//go:norace
func (c *Client) Close() {
	c.CloseWithError(io.EOF)
}

// CloseWithError closes all underlayer connections with error.
//
//go:norace
func (c *Client) CloseWithError(err error) {
	c.mux.Lock()
	if !c.closed {
		c.closed = true
		for _, hcs := range c.connsOfHosts {
			hcs.closeWithError(err)
		}
	}
	c.mux.Unlock()
}

//go:norace
func (c *Client) getConn(host string) (*hostConns, *ClientConn, error) {
	c.connsMux.Lock()
	if c.closed {
		c.connsMux.Unlock()
		return nil, nil, ErrClientClosed
	}

	if c.connsOfHosts == nil {
		c.connsOfHosts = map[string]*hostConns{}
	}
	hcs, ok := c.connsOfHosts[host]
	if !ok {
		hcs = newHostConns(c)
		c.connsOfHosts[host] = hcs
	}
	c.connsMux.Unlock()

	return hcs.getConn()
}

// Do sends an HTTP request and returns an HTTP response.
// Notice:
//  1. It's blocking when Dial to the server;
//  2. It's non-blocking for waiting for the response;
//  3. It calls the handler when the response is received
//     or other errors occur, such as timeout.
//
//go:norace
func (c *Client) Do(req *http.Request, handler func(res *http.Response, conn net.Conn, err error)) {
	c.Engine.ExecuteClient(func() {
		host := req.URL.Host
		hcs, hc, err := c.getConn(host)
		if err != nil {
			handler(nil, nil, err)
			return
		}
		hc.Reset()
		hc.Do(req, func(res *http.Response, conn net.Conn, err error) {
			hcs.releaseConn(hc)
			handler(res, conn, err)
		})
	})
}

type netDialerFunc func(network, addr string) (net.Conn, error)

//go:norace
func (fn netDialerFunc) Dial(network, addr string) (net.Conn, error) {
	return fn(network, addr)
}

var proxySchemes map[string]func(*url.URL, proxyDialer) (proxyDialer, error)

//go:norace
func proxyRegisterDialerType(scheme string, f func(*url.URL, proxyDialer) (proxyDialer, error)) {
	if proxySchemes == nil {
		proxySchemes = make(map[string]func(*url.URL, proxyDialer) (proxyDialer, error))
	}
	proxySchemes[scheme] = f
}

//go:norace
func proxyFromURL(u *url.URL, forward proxyDialer) (proxyDialer, error) {
	var auth *proxyAuth
	if u.User != nil {
		auth = new(proxyAuth)
		auth.User = u.User.Username()
		if p, ok := u.User.Password(); ok {
			auth.Password = p
		}
	}

	switch u.Scheme {
	case "socks5":
		return proxySOCKS5("tcp", u.Host, auth, forward)
	}

	if proxySchemes != nil {
		if f, ok := proxySchemes[u.Scheme]; ok {
			return f(u, forward)
		}
	}

	return nil, errors.New("proxy: unknown scheme: " + u.Scheme)
}

//go:norace
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

type proxyDialer interface {
	Dial(network, addr string) (c net.Conn, err error)
}

type httpProxyDialer struct {
	proxyURL    *url.URL
	forwardDial func(network, addr string) (net.Conn, error)
}

//go:norace
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

	if errWrite := connectReq.Write(conn); errWrite != nil {
		_ = conn.Close()
		return nil, errWrite
	}

	br := bufio.NewReader(conn)
	resp, err := http.ReadResponse(br, connectReq)
	if err != nil {
		_ = conn.Close()
		return nil, err
	}
	_ = resp.Body.Close()

	if resp.StatusCode != 200 {
		_ = conn.Close()
		f := strings.SplitN(resp.Status, " ", 2)
		return nil, errors.New(f[1])
	}
	return conn, nil
}

type proxyAuth struct {
	User, Password string
}

//go:norace
func proxySOCKS5(network, addr string, auth *proxyAuth, forward proxyDialer) (proxyDialer, error) {
	s := &proxySocks5{
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

type proxySocks5 struct {
	user, password string
	network, addr  string
	forward        proxyDialer
}

const proxySocks5Version = 5

const (
	proxySocks5AuthNone     = 0
	proxySocks5AuthPassword = 2
)

const proxySocks5Connect = 1

const (
	proxySocks5IP4    = 1
	proxySocks5Domain = 3
	proxySocks5IP6    = 4
)

var proxySocks5Errors = []string{
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

//go:norace
func (s *proxySocks5) Dial(network, addr string) (net.Conn, error) {
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
		_ = conn.Close()
		return nil, err
	}
	return conn, nil
}

const errProxyAtSocks5Prefix = "proxy: SOCKS5 proxy at "

//go:norace
func (s *proxySocks5) connect(conn net.Conn, target string) error {
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

	buf = append(buf, proxySocks5Version)
	if len(s.user) > 0 && len(s.user) < 256 && len(s.password) < 256 {
		buf = append(buf, 2 /* num auth methods */, proxySocks5AuthNone, proxySocks5AuthPassword)
	} else {
		buf = append(buf, 1 /* num auth methods */, proxySocks5AuthNone)
	}

	if _, err := conn.Write(buf); err != nil {
		return errors.New("proxy: failed to write greeting to SOCKS5 proxy at " + s.addr + ": " + err.Error())
	}

	if _, err := io.ReadFull(conn, buf[:2]); err != nil {
		return errors.New("proxy: failed to read greeting from SOCKS5 proxy at " + s.addr + ": " + err.Error())
	}
	if buf[0] != 5 {
		return errors.New(errProxyAtSocks5Prefix + s.addr + " has unexpected version " + strconv.Itoa(int(buf[0])))
	}
	if buf[1] == 0xff {
		return errors.New(errProxyAtSocks5Prefix + s.addr + " requires authentication")
	}

	// See RFC 1929
	if buf[1] == proxySocks5AuthPassword {
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
			return errors.New(errProxyAtSocks5Prefix + s.addr + " rejected username/password")
		}
	}

	buf = buf[:0]
	buf = append(buf, proxySocks5Version, proxySocks5Connect, 0 /* reserved */)

	if ip := net.ParseIP(host); ip != nil {
		if ip4 := ip.To4(); ip4 != nil {
			buf = append(buf, proxySocks5IP4)
			ip = ip4
		} else {
			buf = append(buf, proxySocks5IP6)
		}
		buf = append(buf, ip...)
	} else {
		if len(host) > 255 {
			return errors.New("proxy: destination host name too long: " + host)
		}
		buf = append(buf, proxySocks5Domain)
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
	if int(buf[1]) < len(proxySocks5Errors) {
		failure = proxySocks5Errors[buf[1]]
	}

	if len(failure) > 0 {
		return errors.New(errProxyAtSocks5Prefix + s.addr + " failed to connect: " + failure)
	}

	var bytesToDiscard int
	switch buf[3] {
	case proxySocks5IP4:
		bytesToDiscard = net.IPv4len
	case proxySocks5IP6:
		bytesToDiscard = net.IPv6len
	case proxySocks5Domain:
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

//go:norace
func init() {
	proxyRegisterDialerType("http", func(proxyURL *url.URL, forwardDialer proxyDialer) (proxyDialer, error) {
		return &httpProxyDialer{proxyURL: proxyURL, forwardDial: forwardDialer.Dial}, nil
	})
}
