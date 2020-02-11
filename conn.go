package nbio

import (
	"errors"
	"net"
	"sync"
	"syscall"
	"time"
	"unsafe"
)

var (
	errClosed       = errors.New("conn closed")
	errInvalidData  = errors.New("invalid data")
	errWriteWaiting = errors.New("write waiting")
	errTimeout      = errors.New("timeout")
	errReadTimeout  = errors.New("read timeout")
	errWriteTimeout = errors.New("write timeout")
)

// Conn implement net.Conn
type Conn struct {
	mux sync.Mutex

	g *Gopher // g

	fd     int // file descriptor
	rTimer *time.Timer
	wTimer *time.Timer

	left      int      // left to send
	writeList [][]byte // send queue

	closed   bool  // is closed
	isWAdded bool  // write event
	closeErr error // err on closed

	lAddr net.Addr // local addr
	rAddr net.Addr // remote addr

	session interface{} // user session
}

// Fd return system file descriptor
func (c *Conn) Fd() int {
	return c.fd
}

// Hash return a hashcode
func (c *Conn) Hash() int {
	return c.fd
}

// Read implement net.Conn
func (c *Conn) Read(b []byte) (int, error) {
	c.mux.Lock()

	if c.closed {
		c.mux.Unlock()
		return 0, errClosed
	}

	if c.g.onRead != nil {
		n, err := c.g.onRead(c, b)
		c.mux.Unlock()
		return n, err
	}

	n, err := syscall.Read(int(c.fd), b)
	c.mux.Unlock()
	return n, err
}

// Write implement net.Conn
// IF return syscall.EINVAL, should Close
// ELSE the data would be send or push to write list
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
		c.closeWithErrorWithoutLock(errInvalidData)
		return n, err
	}

	if c.left == 0 {
		if c.wTimer != nil {
			c.wTimer.Stop()
		}
	} else {
		c.setReadWrite()
	}

	c.mux.Unlock()
	return n, err
}

// Writev wrap writevimplement and extend net.Conn
// IF return syscall.EINVAL, should Close
// ELSE the data would be send or push to write list
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
		c.closeWithErrorWithoutLock(err)
		return n, err
	}
	if c.left == 0 {
		if c.wTimer != nil {
			c.wTimer.Stop()
		}
	} else {
		c.setReadWrite()
	}

	c.mux.Unlock()
	return n, err
}

// Close implement net.Conn
func (c *Conn) Close() error {
	return c.closeWithError(nil)
}

// LocalAddr return socket local addr
func (c *Conn) LocalAddr() net.Addr {
	return c.lAddr
}

// RemoteAddr return socket remote addr
func (c *Conn) RemoteAddr() net.Addr {
	return c.rAddr
}

// SetDeadline set socket recv & send deadline
func (c *Conn) SetDeadline(t time.Time) error {
	c.mux.Lock()
	if !c.closed {
		now := time.Now()
		if t.Before(now) {
			c.closeWithErrorWithoutLock(errTimeout)
			return errTimeout
		}
		if c.rTimer == nil {
			c.rTimer = time.AfterFunc(t.Sub(now), func() { c.closeWithError(errReadTimeout) })
		} else {
			c.rTimer.Reset(t.Sub(now))
		}
		if c.wTimer == nil {
			c.wTimer = time.AfterFunc(t.Sub(now), func() { c.closeWithError(errWriteTimeout) })
		} else {
			c.wTimer.Reset(t.Sub(now))
		}
	}
	c.mux.Unlock()
	return nil
}

// SetReadDeadline set socket recv deadline
func (c *Conn) SetReadDeadline(t time.Time) error {
	c.mux.Lock()
	if !c.closed && len(c.writeList) == 0 {
		now := time.Now()
		if t.Before(now) {
			c.closeWithErrorWithoutLock(errReadTimeout)
			return errReadTimeout
		}
		if c.rTimer == nil {
			c.rTimer = time.AfterFunc(t.Sub(now), func() { c.closeWithError(errReadTimeout) })
		} else {
			c.rTimer.Reset(t.Sub(now))
		}
	}
	c.mux.Unlock()
	return nil
}

// SetWriteDeadline set socket send deadline
func (c *Conn) SetWriteDeadline(t time.Time) error {
	c.mux.Lock()
	if !c.closed {
		now := time.Now()
		if t.Before(now) {
			c.closeWithErrorWithoutLock(errWriteTimeout)
			return errWriteTimeout
		}
		if c.wTimer == nil {
			c.wTimer = time.AfterFunc(t.Sub(now), func() { c.closeWithError(errWriteTimeout) })
		} else {
			c.wTimer.Reset(t.Sub(now))
		}
	}
	c.mux.Unlock()
	return nil
}

// SetNoDelay set socket nodelay
func (c *Conn) SetNoDelay(nodelay bool) error {
	if nodelay {
		return syscall.SetsockoptInt(c.fd, syscall.IPPROTO_TCP, syscall.TCP_NODELAY, 1)
	}
	return syscall.SetsockoptInt(c.fd, syscall.IPPROTO_TCP, syscall.TCP_NODELAY, 0)
}

// SetReadBuffer set socket recv buffer length
func (c *Conn) SetReadBuffer(bytes int) error {
	return syscall.SetsockoptInt(c.fd, syscall.SOL_SOCKET, syscall.SO_RCVBUF, bytes)
}

// SetWriteBuffer set socket send buffer length
func (c *Conn) SetWriteBuffer(bytes int) error {
	return syscall.SetsockoptInt(c.fd, syscall.SOL_SOCKET, syscall.SO_SNDBUF, bytes)
}

// SetKeepAlive set socket keepalive
func (c *Conn) SetKeepAlive(keepalive bool) error {
	if keepalive {
		return syscall.SetsockoptInt(c.fd, syscall.SOL_SOCKET, syscall.SO_KEEPALIVE, 1)
	}
	return syscall.SetsockoptInt(c.fd, syscall.SOL_SOCKET, syscall.SO_KEEPALIVE, 0)
}

// SetKeepAlivePeriod set socket keepalive peroid
func (c *Conn) SetKeepAlivePeriod(d time.Duration) error {
	d += (time.Second - time.Nanosecond)
	secs := int(d.Seconds())
	if err := syscall.SetsockoptInt(c.fd, syscall.IPPROTO_TCP, syscall.TCP_KEEPINTVL, secs); err != nil {
		return err
	}
	return syscall.SetsockoptInt(c.fd, syscall.IPPROTO_TCP, syscall.TCP_KEEPIDLE, secs)
}

// SetLinger set socket linger
// to decrease time_wait, plz set onoff=1 && linger=0
func (c *Conn) SetLinger(onoff int32, linger int32) error {
	return syscall.SetsockoptLinger(c.fd, syscall.SOL_SOCKET, syscall.SO_LINGER, &syscall.Linger{
		Onoff:  onoff,  // 1
		Linger: linger, // 0
	})
}

// Session return user session
func (c *Conn) Session() interface{} {
	return c.session
}

// SetSession set user session
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

// setRead event
func (c *Conn) setRead() {
	if !c.closed && c.isWAdded {
		c.isWAdded = false
		c.g.pollers[c.fd%len(c.g.pollers)].setRead(c.fd)
	}
}

func (c *Conn) setReadWrite() {
	if !c.closed && !c.isWAdded {
		c.isWAdded = true
		c.g.pollers[c.fd%len(c.g.pollers)].setReadWrite(c.fd)
	}
}

// write buffer
func (c *Conn) write(b []byte) (int, error) {
	if len(b) == 0 {
		return 0, nil
	}

	if c.overflow(len(b)) {
		return -1, syscall.EINVAL
	}

	c.left += len(b)

	var (
		err    error
		nwrite int
	)

	if len(c.writeList) == 0 {
		for {
			n, err := syscall.Write(int(c.fd), b)
			if n > 0 {
				nwrite += n
				c.left -= n
				if n < len(b) {
					c.writeList = append(c.writeList, b[n:])
					return n, err
				}
			}
			if err == syscall.EINTR {
				continue
			}

			break
		}
	} else {
		c.writeList = append(c.writeList, b)
	}

	return nwrite, err
}

// flush dump write list data to socket
func (c *Conn) flush() error {
	c.mux.Lock()
	if c.closed {
		c.mux.Unlock()
		return errClosed
	}

	wl := c.writeList
	c.left = 0
	c.writeList = nil
	_, err := c.writev(wl)
	if err != nil && err != syscall.EAGAIN && err != syscall.EINTR {
		c.closed = true
		c.mux.Unlock()
		c.closeWithErrorWithoutLock(err)
		return err
	}
	if c.left == 0 {
		if c.wTimer != nil {
			c.wTimer.Stop()
		}
	} else {
		c.setReadWrite()
	}

	c.mux.Unlock()
	return err
}

// writev
func (c *Conn) writev(in [][]byte) (int, error) {
	if len(c.writeList) == 0 {
		return c.writevSocket(in)
	}
	return c.writeCache(in)
}

// writeCache
func (c *Conn) writeCache(in [][]byte) (int, error) {
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

	c.left += ntotal
	c.writeList = append(c.writeList, in...)

	return 0, nil
}

// writevSocket
func (c *Conn) writevSocket(in [][]byte) (int, error) {
	var (
		err    error
		ntotal int
		nwrite int
		iovec  = make([]syscall.Iovec, len(in))
	)

	for i, slice := range in {
		ntotal += len(slice)
		iovec[i] = syscall.Iovec{&slice[0], uint64(len(slice))}
	}

	if ntotal == 0 {
		return 0, nil
	}

	if c.overflow(ntotal) {
		return -1, syscall.EINVAL
	}

	c.left += ntotal

	nwRaw, _, errno := syscall.Syscall(syscall.SYS_WRITEV, uintptr(c.fd), uintptr(unsafe.Pointer(&iovec[0])), uintptr(len(iovec)))
	if errno != 0 {
		err = syscall.Errno(errno)
	}
	nwrite = int(nwRaw)
	if nwrite > 0 {
		c.left -= nwrite
		if nwrite < ntotal {
			for i := 0; i < len(in); i++ {
				if len(in[i]) < nwrite {
					nwrite -= len(in[i])
				} else if len(in[i]) == nwrite {
					in = in[i+1:]
					iovec = iovec[i+1:]
					iovec[0] = syscall.Iovec{&(in[0][0]), uint64(len(in[0]))}
					break
				} else {
					in[i] = in[i][nwrite:]
					in = in[i:]
					iovec = iovec[i:]
					iovec[0] = syscall.Iovec{&(in[0][0]), uint64(len(in[0]))}
					break
				}
			}
			c.writeList = append(c.writeList, in...)
		}
	}

	return int(nwRaw), err
}

// overflow control write list size of each fd
func (c *Conn) overflow(n int) bool {
	return c.g.memControl && c.left+n > int(c.g.maxWriteBuffer)
}

// closeWithError with lock
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

// closeWithErrorWithoutLock
func (c *Conn) closeWithErrorWithoutLock(err error) error {
	fd := c.fd
	c.session = nil
	c.closeErr = err
	c.g.pollers[fd%len(c.g.pollers)].deleteConn(c)

	return syscallClose(fd)
}

// NewConn is a factory impl
func NewConn(fd int, lAddr, rAddr net.Addr) *Conn {
	return &Conn{
		fd:    fd,
		lAddr: lAddr,
		rAddr: rAddr,
	}
}
