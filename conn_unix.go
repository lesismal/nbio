// Copyright 2020 lesismal. All rights reserved.
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

//go:build linux || darwin || netbsd || freebsd || openbsd || dragonfly
// +build linux darwin netbsd freebsd openbsd dragonfly

package nbio

import (
	"errors"
	"net"
	"runtime"
	"strconv"
	"sync"
	"syscall"
	"time"

	"github.com/lesismal/nbio/mempool"
	"github.com/lesismal/nbio/timer"
)

// Conn implements net.Conn.
type Conn struct {
	mux sync.Mutex

	p *poller

	fd int

	connUDP *udpConn

	rTimer *timer.Item
	wTimer *timer.Item

	writeBuffer []byte

	typ      ConnType
	closed   bool
	isWAdded bool
	closeErr error

	lAddr net.Addr
	rAddr net.Addr

	ReadBuffer []byte

	session interface{}

	chWaitWrite chan struct{}

	execList []func()

	DataHandler func(c *Conn, data []byte)
}

// Hash returns a hash code.
func (c *Conn) Hash() int {
	return noRaceGetFdOnConn(c)
}

// Read implements Read.
func (c *Conn) Read(b []byte) (int, error) {
	// use lock to prevent multiple conn data confusion when fd is reused on unix.
	c.mux.Lock()
	if c.closed {
		c.mux.Unlock()
		return 0, errClosed
	}

	_, n, err := c.doRead(b)
	c.mux.Unlock()
	if err == nil {
		c.p.g.afterRead(c)
	}

	return n, err
}

// ReadUDP .
func (c *Conn) ReadUDP(b []byte) (*Conn, int, error) {
	return c.ReadAndGetConn(b)
}

// ReadAndGetConn .
func (c *Conn) ReadAndGetConn(b []byte) (*Conn, int, error) {
	// use lock to prevent multiple conn data confusion when fd is reused on unix.
	c.mux.Lock()
	if c.closed {
		c.mux.Unlock()
		return c, 0, errClosed
	}

	dstConn, n, err := c.doRead(b)
	c.mux.Unlock()
	if err == nil {
		c.p.g.afterRead(c)
	}

	return dstConn, n, err
}

func (c *Conn) doRead(b []byte) (*Conn, int, error) {
	switch c.typ {
	case ConnTypeTCP:
		return c.readTCP(b)
	case ConnTypeUDPServer, ConnTypeUDPClientFromDial:
		return c.readUDP(b)
	case ConnTypeUDPClientFromRead:
	default:
	}
	return c, 0, errors.New("invalid udp conn for reading")
}

func (c *Conn) readTCP(b []byte) (*Conn, int, error) {
	nread, err := syscall.Read(c.fd, b)
	return c, nread, err
}

func (c *Conn) readUDP(b []byte) (*Conn, int, error) {
	nread, rAddr, err := syscall.Recvfrom(c.fd, b, 0)
	if c.closeErr == nil {
		c.closeErr = err
	}
	if err != nil {
		return c, 0, err
	}

	var g = c.p.g
	var dstConn = c
	if c.typ == ConnTypeUDPServer {
		uc, ok := c.connUDP.getConn(c.p, c.fd, rAddr)
		if g.udpReadTimeout > 0 {
			uc.SetReadDeadline(time.Now().Add(g.udpReadTimeout))
		}
		if !ok {
			g.onOpen(uc)
		}
		dstConn = uc
	}

	return dstConn, nread, err
}

// Write implements Write.
func (c *Conn) Write(b []byte) (int, error) {
	c.mux.Lock()
	if c.closed {
		c.mux.Unlock()
		return -1, errClosed
	}

	c.p.g.beforeWrite(c)

	n, err := c.write(b)
	if err != nil && !errors.Is(err, syscall.EINTR) && !errors.Is(err, syscall.EAGAIN) {
		c.closed = true
		c.mux.Unlock()
		c.closeWithErrorWithoutLock(err)
		return n, err
	}

	if len(c.writeBuffer) == 0 {
		if c.wTimer != nil {
			c.wTimer.Stop()
			c.wTimer = nil
		}
	} else {
		c.modWrite()
	}

	c.mux.Unlock()
	return n, err
}

// Writev implements Writev.
func (c *Conn) Writev(in [][]byte) (int, error) {
	c.mux.Lock()
	if c.closed {
		c.mux.Unlock()

		return 0, errClosed
	}

	c.p.g.beforeWrite(c)

	var n int
	var err error
	switch len(in) {
	case 1:
		n, err = c.write(in[0])
	default:
		n, err = c.writev(in)
	}
	if err != nil && !errors.Is(err, syscall.EINTR) && !errors.Is(err, syscall.EAGAIN) {
		c.closed = true
		c.mux.Unlock()
		c.closeWithErrorWithoutLock(err)
		return n, err
	}
	if len(c.writeBuffer) == 0 {
		if c.wTimer != nil {
			c.wTimer.Stop()
			c.wTimer = nil
		}
	} else {
		c.modWrite()
	}

	c.mux.Unlock()
	return n, err
}

func (c *Conn) writeTCP(b []byte) (int, error) {
	return syscall.Write(c.fd, b)
}

func (c *Conn) writeUDPClientFromDial(b []byte) (int, error) {
	return syscall.Write(c.fd, b)
}

func (c *Conn) writeUDPClientFromRead(b []byte) (int, error) {
	err := syscall.Sendto(c.fd, b, 0, c.connUDP.rAddr)
	if err != nil {
		return 0, err
	}
	return len(b), nil
}

// Close implements Close.
func (c *Conn) Close() error {
	return c.closeWithError(nil)
}

// CloseWithError .
func (c *Conn) CloseWithError(err error) error {
	return c.closeWithError(err)
}

// LocalAddr implements LocalAddr.
//
//go:norace
func (c *Conn) LocalAddr() net.Addr {
	return c.lAddr
}

// RemoteAddr implements RemoteAddr.
func (c *Conn) RemoteAddr() net.Addr {
	return c.rAddr
}

// SetDeadline implements SetDeadline.
func (c *Conn) SetDeadline(t time.Time) error {
	c.mux.Lock()
	if !c.closed {
		if !t.IsZero() {
			g := c.p.g
			now := time.Now()
			if c.rTimer == nil {
				c.rTimer = g.AfterFunc(t.Sub(now), func() { c.closeWithError(errReadTimeout) })
			} else {
				c.rTimer.Reset(t.Sub(now))
			}
			if c.wTimer == nil {
				c.wTimer = g.AfterFunc(t.Sub(now), func() { c.closeWithError(errWriteTimeout) })
			} else {
				c.wTimer.Reset(t.Sub(now))
			}
		} else {
			if c.rTimer != nil {
				c.rTimer.Stop()
				c.rTimer = nil
			}
			if c.wTimer != nil {
				c.wTimer.Stop()
				c.wTimer = nil
			}
		}
	}
	c.mux.Unlock()
	return nil
}

func (c *Conn) setDeadline(timer **timer.Item, returnErr error, t time.Time) error {
	c.mux.Lock()
	defer c.mux.Unlock()
	if c.closed {
		return nil
	}
	if !t.IsZero() {
		now := time.Now()
		if *timer == nil {
			*timer = c.p.g.AfterFunc(t.Sub(now), func() { c.closeWithError(returnErr) })
		} else {
			(*timer).Reset(t.Sub(now))
		}
	} else if *timer != nil {
		(*timer).Stop()
		(*timer) = nil
	}
	return nil
}

// SetReadDeadline implements SetReadDeadline.
func (c *Conn) SetReadDeadline(t time.Time) error {
	return c.setDeadline(&c.rTimer, errReadTimeout, t)
}

// SetWriteDeadline implements SetWriteDeadline.
func (c *Conn) SetWriteDeadline(t time.Time) error {
	return c.setDeadline(&c.wTimer, errWriteTimeout, t)
}

// SetNoDelay implements SetNoDelay.
func (c *Conn) SetNoDelay(nodelay bool) error {
	if nodelay {
		return syscall.SetsockoptInt(c.fd, syscall.IPPROTO_TCP, syscall.TCP_NODELAY, 1)
	}
	return syscall.SetsockoptInt(c.fd, syscall.IPPROTO_TCP, syscall.TCP_NODELAY, 0)
}

// SetReadBuffer implements SetReadBuffer.
func (c *Conn) SetReadBuffer(bytes int) error {
	return syscall.SetsockoptInt(c.fd, syscall.SOL_SOCKET, syscall.SO_RCVBUF, bytes)
}

// SetWriteBuffer implements SetWriteBuffer.
func (c *Conn) SetWriteBuffer(bytes int) error {
	return syscall.SetsockoptInt(c.fd, syscall.SOL_SOCKET, syscall.SO_SNDBUF, bytes)
}

// SetKeepAlive implements SetKeepAlive.
func (c *Conn) SetKeepAlive(keepalive bool) error {
	if keepalive {
		return syscall.SetsockoptInt(c.fd, syscall.SOL_SOCKET, syscall.SO_KEEPALIVE, 1)
	}
	return syscall.SetsockoptInt(c.fd, syscall.SOL_SOCKET, syscall.SO_KEEPALIVE, 0)
}

// SetKeepAlivePeriod implements SetKeepAlivePeriod.
func (c *Conn) SetKeepAlivePeriod(d time.Duration) error {
	if runtime.GOOS == "linux" {
		d += (time.Second - time.Nanosecond)
		secs := int(d.Seconds())
		if err := syscall.SetsockoptInt(c.fd, IPPROTO_TCP, TCP_KEEPINTVL, secs); err != nil {
			return err
		}
		return syscall.SetsockoptInt(c.fd, IPPROTO_TCP, TCP_KEEPIDLE, secs)
	}
	return errors.New("not supported")
}

// SetLinger implements SetLinger.
func (c *Conn) SetLinger(onoff int32, linger int32) error {
	return syscall.SetsockoptLinger(c.fd, syscall.SOL_SOCKET, syscall.SO_LINGER, &syscall.Linger{
		Onoff:  onoff,  // 1
		Linger: linger, // 0
	})
}

// Session returns user session.
func (c *Conn) Session() interface{} {
	return c.session
}

// SetSession sets user session.
func (c *Conn) SetSession(session interface{}) {
	c.session = session
}

func (c *Conn) modWrite() {
	if !c.closed && !c.isWAdded {
		c.isWAdded = true
		noRaceConnOperation(c.p.g, c, noRaceConnOpMod)
	}
}

func (c *Conn) resetRead() {
	if !c.closed && c.isWAdded {
		c.isWAdded = false
		p := c.p
		p.deleteEvent(c.fd)
		p.addRead(c.fd)
	}
}

func (c *Conn) write(b []byte) (int, error) {
	if len(b) == 0 {
		return 0, nil
	}

	if c.overflow(len(b)) {
		return -1, syscall.EINVAL
	}

	if len(c.writeBuffer) == 0 {
		n, err := c.doWrite(b)
		if err != nil && !errors.Is(err, syscall.EINTR) && !errors.Is(err, syscall.EAGAIN) {
			return n, err
		}
		if n < 0 {
			n = 0
		}
		left := len(b) - n
		if left > 0 && c.typ == ConnTypeTCP {
			c.writeBuffer = mempool.Malloc(left)
			copy(c.writeBuffer, b[n:])
			c.modWrite()
		}
		return len(b), nil
	}
	c.writeBuffer = mempool.Append(c.writeBuffer, b...)

	return len(b), nil
}

func (c *Conn) flush() error {
	c.mux.Lock()
	if c.closed {
		c.mux.Unlock()
		return errClosed
	}

	if len(c.writeBuffer) == 0 {
		c.mux.Unlock()
		return nil
	}

	old := c.writeBuffer

	n, err := c.doWrite(old)
	if err != nil && !errors.Is(err, syscall.EINTR) && !errors.Is(err, syscall.EAGAIN) {
		c.closed = true
		c.mux.Unlock()
		c.closeWithErrorWithoutLock(err)
		return err
	}
	if n < 0 {
		n = 0
	}
	left := len(old) - n
	if left > 0 {
		if n > 0 {
			c.writeBuffer = mempool.Malloc(left)
			copy(c.writeBuffer, old[n:])
			mempool.Free(old)
		}
		// c.modWrite()
	} else {
		mempool.Free(old)
		c.writeBuffer = nil
		if c.wTimer != nil {
			c.wTimer.Stop()
			c.wTimer = nil
		}
		c.resetRead()
		if c.chWaitWrite != nil {
			select {
			case c.chWaitWrite <- struct{}{}:
			default:
			}
		}
	}

	c.mux.Unlock()
	return nil
}

func (c *Conn) writev(in [][]byte) (int, error) {
	size := 0
	for _, v := range in {
		size += len(v)
	}
	if c.overflow(size) {
		return -1, syscall.EINVAL
	}
	if len(c.writeBuffer) > 0 {
		for _, v := range in {
			c.writeBuffer = mempool.Append(c.writeBuffer, v...)
		}
		return size, nil
	}

	if len(in) > 1 && size <= 65536 {
		b := mempool.Malloc(size)
		copied := 0
		for _, v := range in {
			copy(b[copied:], v)
			copied += len(v)
		}
		n, err := c.write(b)
		mempool.Free(b)
		return n, err
	}

	nwrite := 0
	for _, b := range in {
		n, err := c.write(b)
		if n > 0 {
			nwrite += n
		}
		if err != nil {
			return nwrite, err
		}
	}
	return nwrite, nil
}

func (c *Conn) doWrite(b []byte) (int, error) {
	var err error
	var nread int
	switch c.typ {
	case ConnTypeTCP:
		nread, err = c.writeTCP(b)
	case ConnTypeUDPServer:
	case ConnTypeUDPClientFromDial:
		nread, err = c.writeUDPClientFromDial(b)
	case ConnTypeUDPClientFromRead:
		nread, err = c.writeUDPClientFromRead(b)
	default:
	}
	return nread, err
}

func (c *Conn) overflow(n int) bool {
	g := c.p.g
	return g.maxWriteBufferSize > 0 && (len(c.writeBuffer)+n > g.maxWriteBufferSize)
}

func (c *Conn) closeWithError(err error) error {
	c.mux.Lock()
	if !c.closed {
		c.closed = true

		if c.wTimer != nil {
			c.wTimer.Stop()
			c.wTimer = nil
		}
		if c.rTimer != nil {
			c.rTimer.Stop()
			c.rTimer = nil
		}

		c.mux.Unlock()
		return c.closeWithErrorWithoutLock(err)
	}
	c.mux.Unlock()
	return nil
}

func (c *Conn) closeWithErrorWithoutLock(err error) error {
	c.closeErr = err

	if c.writeBuffer != nil {
		mempool.Free(c.writeBuffer)
		c.writeBuffer = nil
	}

	if c.chWaitWrite != nil {
		select {
		case c.chWaitWrite <- struct{}{}:
		default:
		}
	}

	if c.p.g != nil {
		noRaceConnOperation(c.p.g, c, noRaceConnOpDel)
	}

	switch c.typ {
	case ConnTypeTCP:
		err = syscall.Close(c.fd)
	case ConnTypeUDPServer, ConnTypeUDPClientFromDial, ConnTypeUDPClientFromRead:
		err = c.connUDP.Close()
	default:
	}

	return err
}

// NBConn converts net.Conn to *Conn.
func NBConn(conn net.Conn) (*Conn, error) {
	if conn == nil {
		return nil, errors.New("invalid conn: nil")
	}
	c, ok := conn.(*Conn)
	if !ok {
		var err error
		c, err = dupStdConn(conn)
		if err != nil {
			return nil, err
		}
	}
	return c, nil
}

type udpConn struct {
	parent *Conn

	rAddr    syscall.Sockaddr
	rAddrStr string

	mux   sync.RWMutex
	conns map[string]*Conn
}

func (u *udpConn) Close() error {
	parent := u.parent
	if parent.connUDP != u {
		parent.mux.Lock()
		delete(parent.connUDP.conns, u.rAddrStr)
		parent.mux.Unlock()
	} else {
		for _, c := range u.conns {
			c.Close()
		}
		u.conns = nil
	}
	return nil
}

func (u *udpConn) getConn(p *poller, fd int, rsa syscall.Sockaddr) (*Conn, bool) {
	rAddrStr := getUDPNetAddrString(rsa)
	u.mux.RLock()
	c, ok := u.conns[rAddrStr]
	u.mux.RUnlock()

	if !ok {
		c = &Conn{
			p:     p,
			fd:    fd,
			lAddr: u.parent.lAddr,
			rAddr: getUDPNetAddr(rsa),
			typ:   ConnTypeUDPClientFromRead,
			connUDP: &udpConn{
				rAddr:    rsa,
				rAddrStr: rAddrStr,
				parent:   u.parent,
			},
		}
		u.mux.Lock()
		u.conns[rAddrStr] = c
		u.mux.Unlock()
	}

	return c, ok
}

func getUDPNetAddrString(sa syscall.Sockaddr) string {
	if sa == nil {
		return "<nil>"
	}
	var ip []byte
	var port int
	var zone string

	switch vt := sa.(type) {
	case *syscall.SockaddrInet4:
		ip = vt.Addr[:]
		port = vt.Port
		return string(ip) + strconv.Itoa(port)
	case *syscall.SockaddrInet6:
		ip = vt.Addr[:]
		port = vt.Port
		i, err := net.InterfaceByIndex(int(vt.ZoneId))
		if err == nil && i != nil {
			return string(ip) + i.Name + strconv.Itoa(port)
		}
	}

	return string(ip) + zone + strconv.Itoa(port)
}

func getUDPNetAddr(sa syscall.Sockaddr) *net.UDPAddr {
	ret := &net.UDPAddr{}
	switch vt := sa.(type) {
	case *syscall.SockaddrInet4:
		ret.IP = make([]byte, len(vt.Addr))
		copy(ret.IP[:], vt.Addr[:])
		ret.Port = vt.Port
	case *syscall.SockaddrInet6:
		ret.IP = make([]byte, len(vt.Addr))
		copy(ret.IP[:], vt.Addr[:])
		ret.Port = vt.Port
		i, err := net.InterfaceByIndex(int(vt.ZoneId))
		if err == nil && i != nil {
			ret.Zone = i.Name
		}
	}
	return ret
}
