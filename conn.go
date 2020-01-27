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
	errReadTimeout  = errors.New("read timeout")
	errWriteTimeout = errors.New("write timeout")
)

// Conn implement net.Conn
type Conn struct {
	mux sync.Mutex

	g *Gopher // g

	fd     int // file descriptor
	rIndex int // read timer index
	wIndex int // write timer index

	left      int      // left to send
	writeList [][]byte // send queue

	closed   bool  // is closed
	closeErr error // err on closed

	remoteAddr net.Addr // remote addr

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
	defer c.mux.Unlock()
	if c.closed {
		return 0, errClosed
	}
	if c.g.onRead != nil {
		return c.g.onRead(c, b)
	}

	return syscall.Read(int(c.fd), b)
}

// Write implement net.Conn
func (c *Conn) Write(b []byte) error {
	c.mux.Lock()
	defer c.mux.Unlock()
	if c.closed {
		return errClosed
	}

	if len(b) == 0 {
		return errInvalidData
	}

	if c.g.memControl && len(b) > int(c.g.maxWriteBuffer) {
		return syscall.EINVAL
	}

	if len(c.writeList) == 0 {
		var n int
		var err error
		for {
			n, err = syscall.Write(int(c.fd), b)
			if n > 0 {
				if n < len(b) {
					b = b[n:]
					c.writeList = append(c.writeList, b)
					c.g.pollers[c.fd%len(c.g.pollers)].addWrite(c.fd)
					c.left += len(b)
				}
				return nil
			}

			if err == syscall.EAGAIN {
				c.writeList = append(c.writeList, b)
				c.g.pollers[c.fd%len(c.g.pollers)].addWrite(c.fd)
				c.left += len(b)
			} else if err == syscall.EINTR {
				continue
			}
			return err
		}
	} else {
		if c.left+len(b) > int(c.g.maxWriteBuffer) {
			return syscall.EINVAL
		}

		c.writeList = append(c.writeList, b)
		c.left += len(b)
	}

	return errWriteWaiting
}

// Writev wrap writevimplement and extend net.Conn
func (c *Conn) Writev(in [][]byte) (int, error) {
	c.mux.Lock()

	if c.closed {
		c.mux.Unlock()
		return 0, errClosed
	}

	n, err := c.writev(in)
	if c.left == 0 {
		tw := c.g.pollers[c.fd%len(c.g.pollers)].twWrite
		if tw != nil {
			tw.delete(c, &c.rIndex)
		}
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
	return c.g.localAddr
}

// RemoteAddr return socket remote addr
func (c *Conn) RemoteAddr() net.Addr {
	return c.remoteAddr
}

// SetDeadline set socket recv & send deadline
func (c *Conn) SetDeadline(t time.Time) error {
	c.mux.Lock()
	if !c.closed {
		tw := c.g.pollers[c.fd%len(c.g.pollers)].twRead
		if tw != nil {
			tw.reset(c, &c.rIndex, t)
		}
		tw = c.g.pollers[c.fd%len(c.g.pollers)].twWrite
		if tw != nil {
			tw.reset(c, &c.wIndex, t)
		}
	}
	c.mux.Unlock()
	return nil
}

// SetReadDeadline set socket recv deadline
func (c *Conn) SetReadDeadline(t time.Time) error {
	c.mux.Lock()
	if !c.closed && len(c.writeList) == 0 {
		tw := c.g.pollers[c.fd%len(c.g.pollers)].twRead
		if tw != nil {
			tw.reset(c, &c.rIndex, t)
		}
	}
	c.mux.Unlock()
	return nil
}

// SetWriteDeadline set socket send deadline
func (c *Conn) SetWriteDeadline(t time.Time) error {
	c.mux.Lock()
	if !c.closed {
		tw := c.g.pollers[c.fd%len(c.g.pollers)].twWrite
		if tw != nil {
			tw.reset(c, &c.wIndex, t)
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
	ok := (c.session == session)
	c.session = session
	c.mux.Unlock()
	return ok
}

func (c *Conn) writev(in [][]byte) (int, error) {
	if len(c.writeList) == 0 {
		return c.writeDirect(in)
	}
	return c.writeQueue(in)
}

func (c *Conn) writeQueue(in [][]byte) (int, error) {
	var ntotal int
	for _, b := range in {
		if len(b) == 0 {
			return 0, errInvalidData
		}
		ntotal += len(b)
	}
	if c.g.memControl && c.left+ntotal > int(c.g.maxWriteBuffer) {
		return -1, syscall.EINVAL
	}
	c.left += ntotal
	c.writeList = append(c.writeList, in...)

	return 0, nil
}

func (c *Conn) writeDirect(in [][]byte) (int, error) {
	var (
		err        error
		ntotal     int
		nwrite     int
		totalWrite int
		iovec      = make([]syscall.Iovec, len(in))
	)

	for i, slice := range in {
		ntotal += len(slice)
		iovec[i] = syscall.Iovec{&slice[0], uint64(len(slice))}
	}

	if c.g.memControl && c.left+ntotal > int(c.g.maxWriteBuffer) {
		return -1, syscall.EINVAL
	}

	if ntotal == 0 {
		return -1, errInvalidData
	}

	c.left += ntotal

	for {
		nwRaw, _, errno := syscall.Syscall(syscall.SYS_WRITEV, uintptr(c.fd), uintptr(unsafe.Pointer(&iovec[0])), uintptr(len(iovec)))
		err = syscall.Errno(errno)
		nwrite = int(nwRaw)
		if nwrite > 0 {
			totalWrite += nwrite
			c.left -= nwrite
			if nwrite < ntotal {

				ntotal = nwrite
				for i := 0; i < len(in); i++ {
					if len(in[i]) < ntotal {
						ntotal -= len(in[i])
					} else if len(in[i]) == ntotal {
						in = in[i+1:]
						iovec = iovec[i+1:]
						iovec[0] = syscall.Iovec{&(in[0][0]), uint64(len(in[0]))}
						break
					} else {
						in[i] = in[i][ntotal:]
						in = in[i:]
						iovec = iovec[i:]
						iovec[0] = syscall.Iovec{&(in[0][0]), uint64(len(in[0]))}
						break
					}
				}

				if len(in) > 0 {
					c.writeList = append(c.writeList, in...)
				}
			}
			if c.left == 0 {
				tw := c.g.pollers[c.fd%len(c.g.pollers)].twWrite
				if tw != nil {
					tw.delete(c, &c.wIndex)
				}
			}
			return totalWrite, err
		} else if err == syscall.EAGAIN {
			return totalWrite, nil
		} else if err == syscall.EINTR {
			continue
		}
		return totalWrite, err
	}

	return totalWrite, err
}

func (c *Conn) closeWithError(err error) error {
	c.mux.Lock()
	fd := c.fd
	if !c.closed {
		c.g.decrease()
		c.g.pollers[fd%len(c.g.pollers)].deleteConn(c)
		c.closed = true
		c.session = nil
		c.closeErr = err
		c.mux.Unlock()

		// if err != nil {
		// 	c.g.workers[fd%len(c.g.workers)].pushEvent(event{c: c, t: _EVENT_CLOSE})
		// } else {
		// 	c.g.workers[fd%len(c.g.workers)].onCloseEvent(c)
		// }
		c.g.workers[fd%len(c.g.workers)].onCloseEvent(c)

		return syscallClose(fd)
	}
	c.mux.Unlock()

	return nil
}

func (c *Conn) Flush() error {
	c.mux.Lock()
	defer c.mux.Unlock()
	if c.closed {
		return errClosed
	}
	wl := c.writeList
	c.writeList = nil
	_, err := c.writev(wl)
	if c.left == 0 {
		tw := c.g.pollers[c.fd%len(c.g.pollers)].twWrite
		if tw != nil {
			tw.delete(c, &c.wIndex)
		}
	}
	return err
}

// NewConn is a factory impl
func NewConn(fd int, remoteAddr net.Addr) *Conn {
	return &Conn{
		fd:         fd,
		remoteAddr: remoteAddr,
	}
}
