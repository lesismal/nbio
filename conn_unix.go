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

	// rIndex int
	// wIndex int
	rTimer *htimer
	wTimer *htimer

	leftSize    int
	writeBuffer []byte

	closed   bool
	isWAdded bool
	closeErr error

	lAddr net.Addr
	rAddr net.Addr

	ReadBuffer []byte

	session interface{}
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
	// use lock to prevent multiple conn data confusion when fd is reused on unix
	c.mux.Lock()
	if c.closed {
		c.mux.Unlock()
		c.g.onWriteBufferFree(c, b)
		return -1, errClosed
	}

	c.g.beforeWrite(c)

	n, err := c.write(b)
	if err != nil && err != syscall.EINTR && err != syscall.EAGAIN {
		c.closed = true
		c.mux.Unlock()
		if c.wTimer != nil {
			c.wTimer.Stop()
		}
		c.closeWithErrorWithoutLock(errInvalidData)
		return n, err
	}

	if len(c.writeBuffer) == 0 {
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

	n, err := c.writev(in)
	if err != nil && err != syscall.EINTR && err != syscall.EAGAIN {
		c.closed = true
		c.mux.Unlock()
		if c.wTimer != nil {
			c.wTimer.Stop()
		}
		// tw := c.g.pollers[c.fd%len(c.g.pollers)].twWrite
		// tw.delete(c, &c.wIndex)
		c.closeWithErrorWithoutLock(err)
		return n, err
	}
	if len(c.writeBuffer) == 0 {
		if c.wTimer != nil {
			c.wTimer.Stop()
		}
		// tw := c.g.pollers[c.fd%len(c.g.pollers)].twWrite
		// tw.delete(c, &c.wIndex)
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

	var (
		err    error
		nwrite int
	)

	if len(c.writeBuffer) == 0 {
		for {
			n, err := syscall.Write(int(c.fd), b[nwrite:])
			if n > 0 {
				nwrite += n
				size := len(b) - nwrite
				if size > 0 {
					if size < c.g.minConnCacheSize {
						c.writeBuffer = mempool.Malloc(c.g.minConnCacheSize)[:size]
					} else {
						c.writeBuffer = mempool.Malloc(size)
					}
					copy(c.writeBuffer, b[nwrite:])
					c.g.onWriteBufferFree(c, b)
					return nwrite, err
				}
			}
			if err == syscall.EINTR || err == syscall.EAGAIN {
				continue
			}
			c.g.onWriteBufferFree(c, b)
			return nwrite, err
		}
	}
	need := len(c.writeBuffer) + len(b)
	if need < c.g.minConnCacheSize {
		c.writeBuffer = mempool.Realloc(c.writeBuffer, c.g.minConnCacheSize)[:need]
	} else {
		c.writeBuffer = mempool.Realloc(c.writeBuffer, need)
	}
	copy(c.writeBuffer[len(c.writeBuffer)-len(b):], b)
	c.g.onWriteBufferFree(c, b)

	return nwrite, err
}

func (c *Conn) flush() error {
	c.mux.Lock()
	if c.closed {
		c.mux.Unlock()
		return errClosed
	}

	buffer := c.writeBuffer
	c.writeBuffer = nil
	_, err := c.write(buffer)
	if err != nil && err != syscall.EINTR && err != syscall.EAGAIN {
		c.closed = true
		c.mux.Unlock()
		if c.wTimer != nil {
			c.wTimer.Stop()
		}
		c.closeWithErrorWithoutLock(err)
		return err
	}
	if len(c.writeBuffer) == 0 {
		if c.wTimer != nil {
			c.wTimer.Stop()
		}
		c.resetRead()
	} else {
		c.modWrite()
	}

	c.mux.Unlock()
	return err
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
	left := len(c.writeBuffer)
	if left > 0 {
		c.writeBuffer = mempool.Realloc(c.writeBuffer, left+size)
		copied := left
		for _, v := range in {
			copy(c.writeBuffer[copied:], v)
			copied += len(v)
			c.g.onWriteBufferFree(c, v)
		}
		return 0, nil
	}

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

func (c *Conn) overflow(n int) bool {
	return c.g.maxWriteBufferSize > 0 && (len(c.writeBuffer)+n > int(c.g.maxWriteBufferSize))
}

func (c *Conn) closeWithError(err error) error {
	c.mux.Lock()
	if !c.closed {
		c.closed = true
		c.mux.Unlock()
		if c.wTimer != nil {
			c.wTimer.Stop()
			c.wTimer = nil
		}
		if c.rTimer != nil {
			c.rTimer.Stop()
			c.rTimer = nil
		}
		if c.writeBuffer != nil {
			mempool.Free(c.writeBuffer)
			c.writeBuffer = nil
		}
		return c.closeWithErrorWithoutLock(err)
	}
	c.mux.Unlock()
	return nil
}

func (c *Conn) closeWithErrorWithoutLock(err error) error {
	fd := c.fd
	c.closeErr = err
	if c.g != nil {
		c.g.pollers[c.Hash()%len(c.g.pollers)].deleteConn(c)
	}

	return syscall.Close(fd)
}

func dupStdConn(conn net.Conn) (*Conn, error) {
	sc, ok := conn.(interface {
		SyscallConn() (syscall.RawConn, error)
	})
	if !ok {
		return nil, errors.New("RawConn Unsupported")
	}
	rc, err := sc.SyscallConn()
	if err != nil {
		return nil, errors.New("RawConn Unsupported")
	}

	var newFd int
	errCtrl := rc.Control(func(fd uintptr) {
		newFd, err = syscall.Dup(int(fd))
	})

	if errCtrl != nil {
		return nil, errCtrl
	}

	if err != nil {
		return nil, err
	}

	err = syscall.SetNonblock(newFd, true)
	if err != nil {
		syscall.Close(newFd)
		return nil, err
	}

	return &Conn{
		fd:    newFd,
		lAddr: conn.LocalAddr(),
		rAddr: conn.RemoteAddr(),
	}, nil
}

func newConn(fd int, lAddr, rAddr net.Addr) *Conn {
	return &Conn{
		fd:    fd,
		lAddr: lAddr,
		rAddr: rAddr,
	}
}

// Dial wraps syscall.Connect
func Dial(network string, address string) (*Conn, error) {
	sa, _, err := getSockaddr(network, address)
	if err != nil {
		return nil, err
	}

	fd, err := syscall.Socket(syscall.AF_INET, syscall.SOCK_STREAM, syscall.IPPROTO_TCP)
	if err != nil {
		return nil, err
	}

	err = syscall.Connect(fd, sa)
	if err != nil {
		return nil, err
	}

	err = syscall.SetNonblock(fd, true)
	if err != nil {
		syscall.Close(fd)
		return nil, err
	}

	la, err := syscall.Getsockname(fd)
	if err != nil {
		return nil, err
	}

	c := newConn(fd, sockaddrToAddr(la), sockaddrToAddr(sa))

	return c, nil
}
