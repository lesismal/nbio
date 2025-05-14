// Copyright 2020 lesismal. All rights reserved.
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

//go:build windows
// +build windows

package nbio

import (
	"bytes"
	"errors"
	"io"
	"net"
	"sync"
	"syscall"
	"time"

	"github.com/lesismal/nbio/timer"
)

// Conn wraps net.Conn.
type Conn struct {
	p *poller

	hash int

	mux sync.Mutex

	conn    net.Conn
	connUDP *udpConn

	rTimer *time.Timer

	typ      ConnType
	closed   bool
	closeErr error

	ReadBuffer []byte

	// user session.
	session interface{}

	jobList []func()

	cache *bytes.Buffer

	dataHandler func(c *Conn, data []byte)

	onConnected func(c *Conn, err error)
}

// Hash returns a hashcode.
//
//go:norace
func (c *Conn) Hash() int {
	return c.hash
}

// Read wraps net.Conn.Read.
//
//go:norace
func (c *Conn) Read(b []byte) (int, error) {
	if c.closeErr != nil {
		return 0, c.closeErr
	}

	var reader io.Reader = c.conn
	if c.cache != nil {
		reader = c.cache
	}
	nread, err := reader.Read(b)
	if c.closeErr == nil {
		c.closeErr = err
	}
	return nread, err
}

//go:norace
func (c *Conn) read(b []byte) (int, error) {
	var err error
	var nread int
	switch c.typ {
	case ConnTypeTCP:
		nread, err = c.readTCP(b)
	case ConnTypeUDPServer, ConnTypeUDPClientFromDial:
		nread, err = c.readUDP(b)
	case ConnTypeUDPClientFromRead:
		err = errors.New("invalid udp conn for reading")
	default:
	}
	return nread, err
}

//go:norace
func (c *Conn) readTCP(b []byte) (int, error) {
	g := c.p.g
	// g.beforeRead(c)
	nread, err := c.conn.Read(b)
	if c.closeErr == nil {
		c.closeErr = err
	}
	if g.onRead != nil {
		if nread > 0 {
			if c.cache == nil {
				c.cache = bytes.NewBuffer(nil)
			}
			c.cache.Write(b[:nread])
		}
		g.onRead(c)
		return nread, nil
	} else if nread > 0 {
		b = b[:nread]
		g.onDataPtr(c, &b)
	}
	return nread, err
}

//go:norace
func (c *Conn) readUDP(b []byte) (int, error) {
	if c.connUDP == nil {
		return 0, errors.New("invalid conn")
	}
	nread, rAddr, err := c.connUDP.ReadFromUDP(b)
	if c.closeErr == nil {
		c.closeErr = err
	}
	if err != nil {
		return 0, err
	}

	var g = c.p.g
	var dstConn = c
	if c.typ == ConnTypeUDPServer {
		uc, ok := c.connUDP.getConn(c.p, rAddr)
		if g.UDPReadTimeout > 0 {
			uc.SetReadDeadline(time.Now().Add(g.UDPReadTimeout))
		}
		if !ok {
			p := g.pollers[c.Hash()%len(g.pollers)]
			p.addConn(uc)
		}
		dstConn = uc
	}

	if g.onRead != nil {
		if nread > 0 {
			if dstConn.cache == nil {
				dstConn.cache = bytes.NewBuffer(nil)
			}
			dstConn.cache.Write(b[:nread])
		}
		g.onRead(dstConn)
		return nread, nil
	} else if nread > 0 {
		buf := b[:nread]
		g.onDataPtr(dstConn, &buf)
	}

	return nread, err
}

// Write wraps net.Conn.Write.
//
//go:norace
func (c *Conn) Write(b []byte) (int, error) {
	var n int
	var err error
	switch c.typ {
	case ConnTypeTCP:
		n, err = c.writeTCP(b)
	case ConnTypeUDPServer:
	case ConnTypeUDPClientFromDial:
		n, err = c.writeUDPClientFromDial(b)
	case ConnTypeUDPClientFromRead:
		n, err = c.writeUDPClientFromRead(b)
	default:
	}
	if c.p.g.onWrittenSize != nil && n > 0 {
		c.p.g.onWrittenSize(c, b[:n], n)
	}
	return n, err
}

//go:norace
func (c *Conn) writeTCP(b []byte) (int, error) {
	// c.p.g.beforeWrite(c)
	nwrite, err := c.conn.Write(b)
	if err != nil {
		if c.closeErr == nil {
			c.closeErr = err
		}
		c.Close()
	}

	return nwrite, err
}

//go:norace
func (c *Conn) writeUDPClientFromDial(b []byte) (int, error) {
	nwrite, err := c.connUDP.Write(b)
	if err != nil {
		if c.closeErr == nil {
			c.closeErr = err
		}
		c.Close()
	}
	return nwrite, err
}

//go:norace
func (c *Conn) writeUDPClientFromRead(b []byte) (int, error) {
	nwrite, err := c.connUDP.WriteToUDP(b, c.connUDP.rAddr)
	if err != nil {
		if c.closeErr == nil {
			c.closeErr = err
		}
		c.Close()
	}
	return nwrite, err
}

// Writev wraps buffers.WriteTo/syscall.Writev.
//
//go:norace
func (c *Conn) Writev(in [][]byte) (int, error) {
	if c.connUDP == nil {
		buffers := net.Buffers(in)
		nwrite, err := buffers.WriteTo(c.conn)
		if err != nil {
			if c.closeErr == nil {
				c.closeErr = err
			}
			c.Close()
		}
		if c.p.g.onWrittenSize != nil && nwrite > 0 {
			total := int(nwrite)
			for i := 0; total > 0; i++ {
				if total <= len(in[i]) {
					c.p.g.onWrittenSize(c, in[i][:total], total)
					total = 0
				} else {
					c.p.g.onWrittenSize(c, in[i], len(in[i]))
					total -= len(in[i])
				}
			}
		}
		return int(nwrite), err
	}

	var total = 0
	for _, b := range in {
		nwrite, err := c.Write(b)
		if nwrite > 0 {
			total += nwrite
		}
		if c.p.g.onWrittenSize != nil && nwrite > 0 {
			c.p.g.onWrittenSize(c, b[:nwrite], nwrite)
		}
		if err != nil {
			if c.closeErr == nil {
				c.closeErr = err
			}
			c.Close()
			return total, err
		}
	}
	return total, nil
}

// Close wraps net.Conn.Close.
//
//go:norace
func (c *Conn) Close() error {
	var err error
	c.mux.Lock()
	if !c.closed {
		c.closed = true

		if c.rTimer != nil {
			c.rTimer.Stop()
			c.rTimer = nil
		}

		switch c.typ {
		case ConnTypeTCP:
			err = c.conn.Close()
		case ConnTypeUDPServer, ConnTypeUDPClientFromDial, ConnTypeUDPClientFromRead:
			err = c.connUDP.Close()
		default:
		}

		c.mux.Unlock()
		if c.p.g != nil {
			c.p.deleteConn(c)
		}
		return err
	}
	c.mux.Unlock()
	return err
}

// CloseWithError .
//
//go:norace
func (c *Conn) CloseWithError(err error) error {
	if c.closeErr == nil {
		c.closeErr = err
	}
	return c.Close()
}

// LocalAddr wraps net.Conn.LocalAddr.
//
//go:norace
func (c *Conn) LocalAddr() net.Addr {
	switch c.typ {
	case ConnTypeTCP:
		return c.conn.LocalAddr()
	case ConnTypeUDPServer, ConnTypeUDPClientFromDial, ConnTypeUDPClientFromRead:
		return c.connUDP.LocalAddr()
	default:
	}
	return nil
}

// RemoteAddr wraps net.Conn.RemoteAddr.
//
//go:norace
func (c *Conn) RemoteAddr() net.Addr {
	switch c.typ {
	case ConnTypeTCP:
		return c.conn.RemoteAddr()
	case ConnTypeUDPClientFromDial:
		return c.connUDP.RemoteAddr()
	case ConnTypeUDPClientFromRead:
		return c.connUDP.rAddr
	default:
	}
	return nil
}

// SetDeadline wraps net.Conn.SetDeadline.
//
//go:norace
func (c *Conn) SetDeadline(t time.Time) error {
	if c.typ == ConnTypeTCP {
		return c.conn.SetDeadline(t)
	}
	return c.SetReadDeadline(t)
}

// SetReadDeadline wraps net.Conn.SetReadDeadline.
//
//go:norace
func (c *Conn) SetReadDeadline(t time.Time) error {
	if t.IsZero() {
		t = time.Now().Add(timer.TimeForever)
	}

	if c.typ == ConnTypeTCP {
		return c.conn.SetReadDeadline(t)
	}

	timeout := time.Until(t)
	if c.rTimer == nil {
		c.rTimer = c.p.g.AfterFunc(timeout, func() {
			c.CloseWithError(errReadTimeout)
		})
	} else {
		c.rTimer.Reset(timeout)
	}

	return nil
}

// SetWriteDeadline wraps net.Conn.SetWriteDeadline.
//
//go:norace
func (c *Conn) SetWriteDeadline(t time.Time) error {
	if c.typ != ConnTypeTCP {
		return nil
	}

	if t.IsZero() {
		t = time.Now().Add(timer.TimeForever)
	}

	return c.conn.SetWriteDeadline(t)
}

// SetNoDelay wraps net.Conn.SetNoDelay.
//
//go:norace
func (c *Conn) SetNoDelay(nodelay bool) error {
	if c.typ != ConnTypeTCP {
		return nil
	}

	conn, ok := c.conn.(*net.TCPConn)
	if ok {
		return conn.SetNoDelay(nodelay)
	}
	return nil
}

// SetReadBuffer wraps net.Conn.SetReadBuffer.
//
//go:norace
func (c *Conn) SetReadBuffer(bytes int) error {
	if c.typ != ConnTypeTCP {
		return nil
	}

	conn, ok := c.conn.(*net.TCPConn)
	if ok {
		return conn.SetReadBuffer(bytes)
	}
	return nil
}

// SetWriteBuffer wraps net.Conn.SetWriteBuffer.
//
//go:norace
func (c *Conn) SetWriteBuffer(bytes int) error {
	if c.typ != ConnTypeTCP {
		return nil
	}

	conn, ok := c.conn.(*net.TCPConn)
	if ok {
		return conn.SetWriteBuffer(bytes)
	}
	return nil
}

// SetKeepAlive wraps net.Conn.SetKeepAlive.
//
//go:norace
func (c *Conn) SetKeepAlive(keepalive bool) error {
	if c.typ != ConnTypeTCP {
		return nil
	}

	conn, ok := c.conn.(*net.TCPConn)
	if ok {
		return conn.SetKeepAlive(keepalive)
	}
	return nil
}

// SetKeepAlivePeriod wraps net.Conn.SetKeepAlivePeriod.
//
//go:norace
func (c *Conn) SetKeepAlivePeriod(d time.Duration) error {
	if c.typ != ConnTypeTCP {
		return nil
	}

	conn, ok := c.conn.(*net.TCPConn)
	if ok {
		return conn.SetKeepAlivePeriod(d)
	}
	return nil
}

// SetLinger wraps net.Conn.SetLinger.
//
//go:norace
func (c *Conn) SetLinger(onoff int32, linger int32) error {
	if c.typ != ConnTypeTCP {
		return nil
	}

	conn, ok := c.conn.(*net.TCPConn)
	if ok {
		return conn.SetLinger(int(linger))
	}
	return nil
}

//go:norace
func newConn(conn net.Conn) *Conn {
	c := &Conn{}
	addr := conn.LocalAddr().String()

	uc, ok := conn.(*net.UDPConn)
	if ok {
		rAddr := uc.RemoteAddr()
		if rAddr == nil {
			c.typ = ConnTypeUDPServer
			c.connUDP = &udpConn{
				UDPConn: uc,
				conns:   map[string]*Conn{},
			}
		} else {
			c.typ = ConnTypeUDPClientFromDial
			addr += rAddr.String()
			c.connUDP = &udpConn{
				UDPConn: uc,
			}
		}
	} else {
		c.conn = conn
		c.typ = ConnTypeTCP
	}

	for _, ch := range addr {
		c.hash = 31*c.hash + int(ch)
	}
	if c.hash < 0 {
		c.hash = -c.hash
	}

	return c
}

// NBConn converts net.Conn to *Conn.
//
//go:norace
func NBConn(conn net.Conn) (*Conn, error) {
	if conn == nil {
		return nil, errors.New("invalid conn: nil")
	}
	c, ok := conn.(*Conn)
	if !ok {
		c = newConn(conn)
	}
	return c, nil
}

type udpConn struct {
	*net.UDPConn
	rAddr *net.UDPAddr

	mux    sync.RWMutex
	parent *udpConn
	conns  map[string]*Conn
}

//go:norace
func (u *udpConn) Close() error {
	parent := u.parent
	if parent != nil {
		parent.mux.Lock()
		delete(parent.conns, u.rAddr.String())
		parent.mux.Unlock()
	} else {
		u.UDPConn.Close()
		for _, c := range u.conns {
			c.Close()
		}
		u.conns = nil
	}
	return nil
}

//go:norace
func (u *udpConn) getConn(p *poller, rAddr *net.UDPAddr) (*Conn, bool) {
	u.mux.RLock()
	addr := rAddr.String()
	c, ok := u.conns[addr]
	u.mux.RUnlock()

	if !ok {
		c = &Conn{
			p:   p,
			typ: ConnTypeUDPClientFromRead,
			connUDP: &udpConn{
				parent:  u,
				rAddr:   rAddr,
				UDPConn: u.UDPConn,
			},
		}
		hashAddr := u.LocalAddr().String() + addr
		for _, ch := range hashAddr {
			c.hash = 31*c.hash + int(ch)
		}
		if c.hash < 0 {
			c.hash = -c.hash
		}
		u.mux.Lock()
		u.conns[addr] = c
		u.mux.Unlock()
	}

	return c, ok
}

//go:norace
func (c *Conn) SyscallConn() (syscall.RawConn, error) {
	if rc, ok := c.conn.(interface {
		SyscallConn() (syscall.RawConn, error)
	}); ok {
		return rc.SyscallConn()
	}
	return nil, ErrUnsupported
}
