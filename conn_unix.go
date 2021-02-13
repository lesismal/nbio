// +build linux darwin netbsd freebsd openbsd dragonfly

package nbio

import (
	"errors"
	"net"
	"sync"
	"syscall"
	"time"
	"unsafe"
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

	leftSize  int
	sendQueue [][]byte

	closed   bool
	isWAdded bool
	closeErr error

	lAddr net.Addr
	rAddr net.Addr

	session interface{}
}

// Hash returns a hash code
func (c *Conn) Hash() int {
	return c.fd
}

// Read implements Read
func (c *Conn) Read(b []byte) (int, error) {
	c.mux.Lock()
	if c.closed {
		c.mux.Unlock()
		return 0, errClosed
	}
	c.mux.Unlock()

	n, err := syscall.Read(int(c.fd), b)
	return n, err
}

// Write implements Write
func (c *Conn) Write(b []byte) (int, error) {
	c.mux.Lock()
	if c.closed {
		c.mux.Unlock()
		return -1, errClosed
	}

	n, err := c.write(b)
	if err != nil && err != syscall.EAGAIN {
		c.closed = true
		c.mux.Unlock()
		if c.wTimer != nil {
			c.wTimer.Stop()
		}
		// tw := c.g.pollers[c.fd%len(c.g.pollers)].twWrite
		// tw.delete(c, &c.wIndex)
		c.closeWithErrorWithoutLock(errInvalidData)
		return n, err
	}

	if c.leftSize == 0 {
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

// Writev implements Writev
func (c *Conn) Writev(in [][]byte) (int, error) {
	c.mux.Lock()

	if c.closed {
		c.mux.Unlock()
		return 0, errClosed
	}

	n, err := c.writev(in)
	if err != nil && err != syscall.EAGAIN {
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
	if c.leftSize == 0 {
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
	if c.wTimer != nil {
		c.wTimer.Stop()
	}
	if c.rTimer != nil {
		c.rTimer.Stop()
	}
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
		// tw := c.g.pollers[c.fd%len(c.g.pollers)].twRead
		// if tw != nil {
		// 	tw.reset(c, &c.rIndex, t)
		// }
		// tw = c.g.pollers[c.fd%len(c.g.pollers)].twWrite
		// if tw != nil {
		// 	tw.reset(c, &c.wIndex, t)
		// }
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
	}
	c.mux.Unlock()
	return nil
}

// SetReadDeadline implements SetReadDeadline
func (c *Conn) SetReadDeadline(t time.Time) error {
	c.mux.Lock()
	if !c.closed {
		// tw := c.g.pollers[c.fd%len(c.g.pollers)].twRead
		// if tw != nil {
		// 	tw.reset(c, &c.rIndex, t)
		// }
		now := time.Now()
		if c.rTimer == nil {
			c.rTimer = c.g.afterFunc(t.Sub(now), func() { c.closeWithError(errReadTimeout) })
		} else {
			c.rTimer.Reset(t.Sub(now))
		}
	}
	c.mux.Unlock()
	return nil
}

// SetWriteDeadline implements SetWriteDeadline
func (c *Conn) SetWriteDeadline(t time.Time) error {
	c.mux.Lock()
	if !c.closed {
		// tw := c.g.pollers[c.fd%len(c.g.pollers)].twWrite
		// if tw != nil {
		// 	tw.reset(c, &c.wIndex, t)
		// }
		now := time.Now()
		if c.wTimer == nil {
			c.wTimer = c.g.afterFunc(t.Sub(now), func() { c.closeWithError(errWriteTimeout) })
		} else {
			c.wTimer.Reset(t.Sub(now))
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
		p.deleteWrite(c.fd)
	}
}

func (c *Conn) write(b []byte) (int, error) {
	if len(b) == 0 {
		return 0, nil
	}

	if c.overflow(len(b)) {
		return -1, syscall.EINVAL
	}

	c.leftSize += len(b)

	var (
		err    error
		nwrite int
	)

	if len(c.sendQueue) == 0 {
		for {
			n, err := syscall.Write(int(c.fd), b)
			if n > 0 {
				nwrite += n
				c.leftSize -= n
				if n < len(b) {
					c.sendQueue = append(c.sendQueue, b[n:])
					return n, err
				}
			}
			if err == syscall.EINTR {
				continue
			}

			break
		}
	} else {
		c.sendQueue = append(c.sendQueue, b)
	}

	return nwrite, err
}

func (c *Conn) flush() error {
	c.mux.Lock()
	if c.closed {
		c.mux.Unlock()
		return errClosed
	}

	var err error
	var sendQ = c.sendQueue
	c.leftSize = 0
	c.sendQueue = nil
	if len(sendQ) == 1 {
		_, err = c.write(sendQ[0])
	} else {
		_, err = c.writev(sendQ)
	}
	if err != nil && err != syscall.EAGAIN && err != syscall.EINTR {
		c.closed = true
		c.mux.Unlock()
		c.closeWithErrorWithoutLock(err)
		return err
	}
	if c.leftSize == 0 {
		if c.wTimer != nil {
			c.wTimer.Stop()
		}
		c.resetRead()
		// p := c.g.pollers[c.fd%len(c.g.pollers)]
		// p.twWrite.delete(c, &c.wIndex)
	}
	// else {
	// c.modWrite()
	// }

	c.mux.Unlock()
	return err
}

func (c *Conn) writev(in [][]byte) (int, error) {
	if len(c.sendQueue) == 0 {
		return c.writevToSocket(in)
	}
	return c.writeToSendQueue(in)
}

func (c *Conn) writeToSendQueue(in [][]byte) (int, error) {
	var ntotal int
	for _, b := range in {
		if len(b) == 0 {
			return -1, errInvalidData
		}
		ntotal += len(b)
	}

	if ntotal == 0 {
		return 0, nil
	}

	if c.overflow(ntotal) {
		return -1, syscall.EINVAL
	}

	c.leftSize += ntotal
	c.sendQueue = append(c.sendQueue, in...)

	return 0, nil
}

func (c *Conn) writevToSocket(in [][]byte) (int, error) {
	var (
		err    error
		ntotal int
		nwrite int
		iovec  = make([]syscall.Iovec, len(in))
	)

	for i, slice := range in {
		ntotal += len(slice)
		iovec[i] = syscall.Iovec{Base: &slice[0], Len: uint64(len(slice))}
	}

	if ntotal == 0 {
		return 0, nil
	}

	if c.overflow(ntotal) {
		return -1, syscall.EINVAL
	}

	c.leftSize += ntotal

	nwRaw, _, errno := syscall.Syscall(syscall.SYS_WRITEV, uintptr(c.fd), uintptr(unsafe.Pointer(&iovec[0])), uintptr(len(iovec)))
	if errno != 0 {
		err = syscall.Errno(errno)
	}
	nwrite = int(nwRaw)
	if nwrite > 0 {
		c.leftSize -= nwrite
		if nwrite < ntotal {
			for i := 0; i < len(in); i++ {
				if len(in[i]) < nwrite {
					nwrite -= len(in[i])
				} else if len(in[i]) == nwrite {
					in = in[i+1:]
					iovec = iovec[i+1:]
					iovec[0] = syscall.Iovec{Base: &(in[0][0]), Len: uint64(len(in[0]))}
					break
				} else {
					in[i] = in[i][nwrite:]
					in = in[i:]
					iovec = iovec[i:]
					iovec[0] = syscall.Iovec{Base: &(in[0][0]), Len: uint64(len(in[0]))}
					break
				}
			}
			c.sendQueue = append(c.sendQueue, in...)
		}
	}

	return int(nwRaw), err
}

func (c *Conn) overflow(n int) bool {
	return c.g.maxWriteBufferSize > 0 && (c.leftSize+n > int(c.g.maxWriteBufferSize))
}

func (c *Conn) closeWithError(err error) error {
	c.mux.Lock()
	if !c.closed {
		c.closed = true
		c.mux.Unlock()
		return c.closeWithErrorWithoutLock(err)
	}
	c.mux.Unlock()
	return nil
}

func (c *Conn) closeWithErrorWithoutLock(err error) error {
	fd := c.fd
	c.session = nil
	c.closeErr = err
	if c.g != nil {
		c.g.pollers[c.Hash()%len(c.g.pollers)].deleteConn(c)
	}

	return syscall.Close(fd)
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
