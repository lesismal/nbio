// +build windows

package nbio

import (
	"net"
	"sync"
	"time"
)

// Conn wraps net.Conn
type Conn struct {
	g *Gopher

	hash int

	mux sync.Mutex

	conn net.Conn

	closed   bool
	closeErr error

	ReadBuffer []byte

	// user session
	session interface{}
}

// Hash returns a hashcode
func (c *Conn) Hash() int {
	return c.hash
}

// Read wraps net.Conn.Read
func (c *Conn) Read(b []byte) (int, error) {
	c.g.beforeRead(c)
	nread, err := c.conn.Read(b)
	if c.closeErr == nil {
		c.closeErr = err
	}
	return nread, err
}

// Write wraps net.Conn.Write
func (c *Conn) Write(b []byte) (int, error) {
	c.g.beforeWrite(c)

	nwrite, err := c.conn.Write(b)
	if err != nil {
		if c.closeErr == nil {
			c.closeErr = err
		}
		c.Close()
	}

	return nwrite, err
}

// Writev wraps buffers.WriteTo/syscall.Writev
func (c *Conn) Writev(in [][]byte) (int, error) {
	buffers := net.Buffers(in)
	nwrite, err := buffers.WriteTo(c.conn)
	if err != nil {
		if c.closeErr == nil {
			c.closeErr = err
		}
		c.Close()
	}
	return int(nwrite), err
}

// Close wraps net.Conn.Close
func (c *Conn) Close() error {
	c.mux.Lock()
	if !c.closed {
		c.closed = true
		err := c.conn.Close()
		c.mux.Unlock()
		if c.g != nil {
			c.g.pollers[c.Hash()%len(c.g.pollers)].deleteConn(c)
		}
		return err
	}
	c.mux.Unlock()
	return nil
}

// LocalAddr wraps net.Conn.LocalAddr
func (c *Conn) LocalAddr() net.Addr {
	return c.conn.LocalAddr()
}

// RemoteAddr wraps net.Conn.RemoteAddr
func (c *Conn) RemoteAddr() net.Addr {
	return c.conn.RemoteAddr()
}

// SetDeadline wraps net.Conn.SetDeadline
func (c *Conn) SetDeadline(t time.Time) error {
	return c.conn.SetDeadline(t)
}

// SetReadDeadline wraps net.Conn.SetReadDeadline
func (c *Conn) SetReadDeadline(t time.Time) error {
	return c.conn.SetReadDeadline(t)
}

// SetWriteDeadline wraps net.Conn.SetWriteDeadline
func (c *Conn) SetWriteDeadline(t time.Time) error {
	return c.conn.SetWriteDeadline(t)
}

// SetNoDelay wraps net.Conn.SetNoDelay
func (c *Conn) SetNoDelay(nodelay bool) error {
	conn, ok := c.conn.(*net.TCPConn)
	if ok {
		return conn.SetNoDelay(nodelay)
	}
	return nil
}

// SetReadBuffer wraps net.Conn.SetReadBuffer
func (c *Conn) SetReadBuffer(bytes int) error {
	conn, ok := c.conn.(*net.TCPConn)
	if ok {
		return conn.SetReadBuffer(bytes)
	}
	return nil
}

// SetWriteBuffer wraps net.Conn.SetWriteBuffer
func (c *Conn) SetWriteBuffer(bytes int) error {
	conn, ok := c.conn.(*net.TCPConn)
	if ok {
		return conn.SetWriteBuffer(bytes)
	}
	return nil
}

// SetKeepAlive wraps net.Conn.SetKeepAlive
func (c *Conn) SetKeepAlive(keepalive bool) error {
	conn, ok := c.conn.(*net.TCPConn)
	if ok {
		return conn.SetKeepAlive(keepalive)
	}
	return nil
}

// SetKeepAlivePeriod wraps net.Conn.SetKeepAlivePeriod
func (c *Conn) SetKeepAlivePeriod(d time.Duration) error {
	conn, ok := c.conn.(*net.TCPConn)
	if ok {
		return conn.SetKeepAlivePeriod(d)
	}
	return nil
}

// SetLinger wraps net.Conn.SetLinger
func (c *Conn) SetLinger(onoff int32, linger int32) error {
	conn, ok := c.conn.(*net.TCPConn)
	if ok {
		return conn.SetLinger(int(linger))
	}
	return nil
}

// Session returns user session
func (c *Conn) Session() interface{} {
	return c.session
}

// SetSession sets user session
func (c *Conn) SetSession(session interface{}) bool {
	if session == nil {
		return false
	}
	c.mux.Lock()
	if c.session == nil {
		c.session = session
		c.mux.Unlock()
		return true
	}
	c.mux.Unlock()
	return false
}

func newConn(conn net.Conn, fromClient ...interface{}) *Conn {
	c := &Conn{
		conn: conn,
	}

	addr := conn.RemoteAddr().String()
	if len(fromClient) > 0 {
		addr = conn.LocalAddr().String()
	}
	for _, ch := range addr {
		c.hash = 31*c.hash + int(ch)
	}
	if c.hash < 0 {
		c.hash = -c.hash
	}

	return c
}

// Dial wraps net.Dial
func Dial(network string, address string) (*Conn, error) {
	conn, err := net.Dial(network, address)
	if err != nil {
		return nil, err
	}

	c := &Conn{
		conn: conn,
	}

	addr := conn.LocalAddr().String()
	for _, ch := range addr {
		c.hash = 31*c.hash + int(ch)
	}
	if c.hash < 0 {
		c.hash = -c.hash
	}

	return c, nil
}
