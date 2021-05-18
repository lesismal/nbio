// Copyright 2020 lesismal. All rights reserved.
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

// +build linux darwin netbsd freebsd openbsd dragonfly

package nbio

import (
	"errors"
	"net"
	"sync"
	"syscall"
	"time"

	"github.com/lesismal/nbio/mempool"
)

// Conn implements net.Conn
type Conn struct {
	mux sync.Mutex

	g *Gopher

	fd int

	rTimer *htimer
	wTimer *htimer

	leftSize     int
	writeBuffers [][]byte

	closed   bool
	isWAdded bool
	closeErr error

	lAddr net.Addr
	rAddr net.Addr

	ReadBuffer []byte

	session interface{}

	chWaitWrite chan struct{}
}

// Hash returns a hash code
func (c *Conn) Hash() int {
	return c.fd
}

// Read implements Read
func (c *Conn) Read(b []byte) (int, error) {
	// use lock to prevent multiple conn data confusion when fd is reused on unix
	c.mux.Lock()
	if c.closed {
		c.mux.Unlock()
		return 0, errClosed
	}
	c.mux.Unlock()

	n, err := syscall.Read(int(c.fd), b)
	if err == nil {
		c.g.afterRead(c)
	}

	return n, err
}

// Write implements Write
func (c *Conn) Write(b []byte) (int, error) {
	c.mux.Lock()
	if c.closed {
		c.mux.Unlock()
		c.g.onWriteBufferFree(c, b)
		return -1, errClosed
	}

	c.g.beforeWrite(c)

	n, err := c.write(b)
	if err != nil && err != syscall.EINTR && err != syscall.EAGAIN {
		c.closeWithErrorWithoutLock(err)
		c.mux.Unlock()
		return n, err
	}

	if len(c.writeBuffers) == 0 {
		if c.wTimer != nil {
			c.wTimer.Stop()
		}
	} else {
		c.modWrite()
	}

	c.mux.Unlock()
	return n, err
}

// Writev implements Writev
func (c *Conn) Writev(in [][]byte) (int, error) {
	c.mux.Lock()
	if c.closed {
		c.mux.Unlock()
		for _, v := range in {
			c.g.onWriteBufferFree(c, v)
		}
		return 0, errClosed
	}

	c.g.beforeWrite(c)

	var n int
	var err error
	switch len(in) {
	case 1:
		n, err = c.write(in[0])
	default:
		n, err = c.writev(in)
	}
	if err != nil && err != syscall.EINTR && err != syscall.EAGAIN {
		c.closeWithErrorWithoutLock(err)
		c.mux.Unlock()
		return n, err
	}
	if len(c.writeBuffers) == 0 {
		if c.wTimer != nil {
			c.wTimer.Stop()
		}
	} else {
		c.modWrite()
	}

	c.mux.Unlock()
	return n, err
}

// Close implements Close
func (c *Conn) Close() error {
	return c.closeWithError(nil)
}

// CloseWithError .
func (c *Conn) CloseWithError(err error) error {
	return c.closeWithError(err)
}

// LocalAddr implements LocalAddr
func (c *Conn) LocalAddr() net.Addr {
	return c.lAddr
}

// RemoteAddr implements RemoteAddr
func (c *Conn) RemoteAddr() net.Addr {
	return c.rAddr
}

// SetDeadline implements SetDeadline
func (c *Conn) SetDeadline(t time.Time) error {
	c.mux.Lock()
	if !c.closed {
		if !t.IsZero() {
			now := time.Now()
			if c.rTimer == nil {
				c.rTimer = c.g.afterFunc(t.Sub(now), func() { c.closeWithError(errReadTimeout) })
			} else {
				c.rTimer.Reset(t.Sub(now))
			}
			if c.wTimer == nil {
				c.wTimer = c.g.afterFunc(t.Sub(now), func() { c.closeWithError(errWriteTimeout) })
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

// SetReadDeadline implements SetReadDeadline
func (c *Conn) SetReadDeadline(t time.Time) error {
	c.mux.Lock()
	if !c.closed {
		if !t.IsZero() {
			now := time.Now()
			if c.rTimer == nil {
				c.rTimer = c.g.afterFunc(t.Sub(now), func() { c.closeWithError(errReadTimeout) })
			} else {
				c.rTimer.Reset(t.Sub(now))
			}
		} else if c.rTimer != nil {
			c.rTimer.Stop()
			c.rTimer = nil
		}
	}
	c.mux.Unlock()
	return nil
}

// SetWriteDeadline implements SetWriteDeadline
func (c *Conn) SetWriteDeadline(t time.Time) error {
	c.mux.Lock()
	if !c.closed {
		if !t.IsZero() {
			now := time.Now()
			if c.wTimer == nil {
				c.wTimer = c.g.afterFunc(t.Sub(now), func() { c.closeWithError(errWriteTimeout) })
			} else {
				c.wTimer.Reset(t.Sub(now))
			}
		} else if c.wTimer != nil {
			c.wTimer.Stop()
			c.wTimer = nil
		}
	}
	c.mux.Unlock()
	return nil
}

// SetNoDelay implements SetNoDelay
func (c *Conn) SetNoDelay(nodelay bool) error {
	if nodelay {
		return syscall.SetsockoptInt(c.fd, syscall.IPPROTO_TCP, syscall.TCP_NODELAY, 1)
	}
	return syscall.SetsockoptInt(c.fd, syscall.IPPROTO_TCP, syscall.TCP_NODELAY, 0)
}

// SetReadBuffer implements SetReadBuffer
func (c *Conn) SetReadBuffer(bytes int) error {
	return syscall.SetsockoptInt(c.fd, syscall.SOL_SOCKET, syscall.SO_RCVBUF, bytes)
}

// SetWriteBuffer implements SetWriteBuffer
func (c *Conn) SetWriteBuffer(bytes int) error {
	return syscall.SetsockoptInt(c.fd, syscall.SOL_SOCKET, syscall.SO_SNDBUF, bytes)
}

// SetKeepAlive implements SetKeepAlive
func (c *Conn) SetKeepAlive(keepalive bool) error {
	if keepalive {
		return syscall.SetsockoptInt(c.fd, syscall.SOL_SOCKET, syscall.SO_KEEPALIVE, 1)
	}
	return syscall.SetsockoptInt(c.fd, syscall.SOL_SOCKET, syscall.SO_KEEPALIVE, 0)
}

// SetKeepAlivePeriod implements SetKeepAlivePeriod
func (c *Conn) SetKeepAlivePeriod(d time.Duration) error {
	return errors.New("not supported")
	// d += (time.Second - time.Nanosecond)
	// secs := int(d.Seconds())
	// if err := syscall.SetsockoptInt(c.fd, syscall.IPPROTO_TCP, syscall.TCP_KEEPINTVL, secs); err != nil {
	// 	return err
	// }
	// return syscall.SetsockoptInt(c.fd, syscall.IPPROTO_TCP, syscall.TCP_KEEPIDLE, secs)
}

// SetLinger implements SetLinger
func (c *Conn) SetLinger(onoff int32, linger int32) error {
	return syscall.SetsockoptLinger(c.fd, syscall.SOL_SOCKET, syscall.SO_LINGER, &syscall.Linger{
		Onoff:  onoff,  // 1
		Linger: linger, // 0
	})
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

func (c *Conn) modWrite() {
	if !c.closed && !c.isWAdded {
		c.isWAdded = true
		c.g.pollers[c.Hash()%len(c.g.pollers)].modWrite(c.fd)
	}
}

func (c *Conn) resetRead() {
	if !c.closed && c.isWAdded {
		c.isWAdded = false
		p := c.g.pollers[c.Hash()%len(c.g.pollers)]
		p.deleteEvent(c.fd)
		p.addRead(c.fd)
	}
}

func (c *Conn) write(b []byte) (int, error) {
	if len(b) == 0 {
		return 0, nil
	}

	if c.overflow(len(b)) {
		c.g.onWriteBufferFree(c, b)
		return -1, syscall.EINVAL
	}

	if len(c.writeBuffers) == 0 {
		n, err := syscall.Write(int(c.fd), b)
		if err != nil && err != syscall.EINTR && err != syscall.EAGAIN {
			return n, err
		}

		left := len(b) - n
		if left > 0 {
			c.leftSize += left
			leftData := b
			if n > 0 {
				leftData = mempool.Malloc(left)
				copy(leftData, b[n:])
				c.g.onWriteBufferFree(c, b)
			}
			c.writeBuffers = append(c.writeBuffers, leftData)
			c.modWrite()
		} else {
			c.g.onWriteBufferFree(c, b)
		}
		return len(b), nil
	}
	c.leftSize += len(b)
	c.writeBuffers = append(c.writeBuffers, b)

	return len(b), nil
}

func (c *Conn) flush() error {
	c.mux.Lock()
	if c.closed {
		c.mux.Unlock()
		return errClosed
	}

	buffers := c.writeBuffers
	c.writeBuffers = nil
	var err error
	switch len(buffers) {
	case 1:
		_, err = c.write(buffers[0])
	default:
		_, err = c.writev(buffers)
	}
	if err != nil && err != syscall.EINTR && err != syscall.EAGAIN {
		c.closeWithErrorWithoutLock(err)
		c.mux.Unlock()
		return err
	}
	if len(c.writeBuffers) == 0 {
		if c.wTimer != nil {
			c.wTimer.Stop()
		}
		c.resetRead()
		if c.chWaitWrite != nil {
			select {
			case c.chWaitWrite <- struct{}{}:
			default:
			}
		}
	} else {
		c.modWrite()
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
		for _, v := range in {
			c.g.onWriteBufferFree(c, v)
		}
		return -1, syscall.EINVAL
	}
	if len(c.writeBuffers) > 0 {
		c.writeBuffers = append(c.writeBuffers, in...)
		return size, nil
	}

	if len(in) > 1 && size <= 16384 {
		var b []byte
		if size < c.g.minConnCacheSize {
			b = mempool.Malloc(c.g.minConnCacheSize)[:size]
		} else {
			b = mempool.Malloc(size)
		}
		copied := 0
		for _, v := range in {
			copy(b[copied:], v)
			copied += len(v)
			c.g.onWriteBufferFree(c, v)
		}
		return c.write(b)
	}

	nwrite := 0
	for i, b := range in {
		n, err := c.write(b)
		if n > 0 {
			nwrite += n
		}
		if err != nil {
			for j := i + 1; j < len(in); j++ {
				c.g.onWriteBufferFree(c, in[j])
			}
			return nwrite, err
		}
	}
	return nwrite, nil
}

func (c *Conn) overflow(n int) bool {
	return c.g.maxWriteBufferSize > 0 && (c.leftSize+n > int(c.g.maxWriteBufferSize))
}

func (c *Conn) closeWithError(err error) error {
	c.mux.Lock()
	if !c.closed {
		err = c.closeWithErrorWithoutLock(err)
		c.mux.Unlock()
		return err
	}
	c.mux.Unlock()
	return nil
}

func (c *Conn) closeWithErrorWithoutLock(err error) error {
	c.closed = true

	c.closeErr = err

	if c.wTimer != nil {
		c.wTimer.Stop()
		c.wTimer = nil
	}
	if c.rTimer != nil {
		c.rTimer.Stop()
		c.rTimer = nil
	}

	for _, b := range c.writeBuffers {
		mempool.Free(b)
	}
	c.writeBuffers = nil

	if c.chWaitWrite != nil {
		select {
		case c.chWaitWrite <- struct{}{}:
		default:
		}
	}

	if c.g != nil {
		c.g.pollers[c.Hash()%len(c.g.pollers)].deleteConn(c)
	}

	return syscall.Close(c.fd)
}

func newConn(fd int, lAddr, rAddr net.Addr) *Conn {
	return &Conn{
		fd:    fd,
		lAddr: lAddr,
		rAddr: rAddr,
	}
}

// NBConn converts net.Conn to *Conn
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
