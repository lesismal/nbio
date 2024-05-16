// Copyright 2020 lesismal. All rights reserved.
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

//go:build linux || darwin || netbsd || freebsd || openbsd || dragonfly
// +build linux darwin netbsd freebsd openbsd dragonfly

package nbio

import (
	"encoding/binary"
	"errors"
	"net"
	"runtime"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
)

// var (
// used to reset toWrite struct to empty value.
// emptyToWrite = toWrite{}

//	poolToWrite = sync.Pool{
//		New: func() interface{} {
//			return &toWrite{}
//		},
//	}
// )

func (c *Conn) newToWriteBuf(buf []byte) {
	c.left += len(buf)

	allocator := c.p.g.BodyAllocator
	appendBuffer := func() {
		t := &toWrite{} // poolToWrite.New().(*toWrite)
		b := allocator.Malloc(len(buf))
		copy(b, buf)
		t.buf = b
		c.writeList = append(c.writeList, t)
	}

	if len(c.writeList) == 0 {
		appendBuffer()
		return
	}

	tail := c.writeList[len(c.writeList)-1]
	if tail.buf == nil {
		appendBuffer()
	} else {
		l := len(buf)
		tailLen := len(tail.buf)
		if tailLen+l > maxWriteCacheOrFlushSize {
			appendBuffer()
		} else {
			if cap(tail.buf) < tailLen+l {
				b := allocator.Malloc(tailLen + l)[:tailLen]
				copy(b, tail.buf)
				allocator.Free(tail.buf)
				tail.buf = b
			}
			tail.buf = allocator.Append(tail.buf, buf...)
		}
	}
}

func (c *Conn) newToWriteFile(fd int, offset, remain int64) {
	t := &toWrite{} // poolToWrite.New().(*toWrite)
	t.fd = fd
	t.offset = offset
	t.remain = remain
	c.writeList = append(c.writeList, t)
}

func (c *Conn) releaseToWrite(t *toWrite) {
	if t.buf != nil {
		c.p.g.BodyAllocator.Free(t.buf)
	}
	if t.fd > 0 {
		syscall.Close(t.fd)
	}
	// *t = emptyToWrite
	// poolToWrite.Put(t)
}

const maxWriteCacheOrFlushSize = 1024 * 64

type toWrite struct {
	fd     int    // file descriptor, used for sendfile
	buf    []byte // buffer to write
	offset int64  // buffer or file offset
	remain int64  // buffer or file remain bytes
}

// Conn implements net.Conn with non-blocking interfaces.
type Conn struct {
	mux sync.Mutex

	// the poller that handles io events for this connection.
	p *poller

	// file descriptor.
	fd int

	connUDP *udpConn

	// used for read deadline.
	rTimer *time.Timer
	// used for write deadline.
	wTimer *time.Timer

	// how many bytes are cached and wait to be written.
	left int
	// cache for buffers or files to be sent.
	writeList []*toWrite

	typ    ConnType
	closed bool

	// whether the writing event has been set in the poller.
	isWAdded bool
	// the first closing error.
	closeErr error

	// local addr.
	lAddr net.Addr
	// remote addr.
	rAddr net.Addr

	// user session.
	session interface{}

	// job list.
	jobList []func()

	readEvents int32

	dataHandler func(c *Conn, data []byte)

	onConnected func(c *Conn, err error)
}

// Hash returns a hash code of this connection.
func (c *Conn) Hash() int {
	return c.fd
}

// AsyncReadInPoller is used for reading data async.
func (c *Conn) AsyncRead() {
	g := c.p.g

	// If is EPOLLONESHOT, run the read job directly, because the reading event wouldn't
	// be re-dispatched before this reading event has been handled and set again.
	if g.isOneshot {
		g.IOExecute(func(buffer []byte) {
			for i := 0; i < g.MaxConnReadTimesPerEventLoop; i++ {
				rc, n, err := c.ReadAndGetConn(buffer)
				if n > 0 {
					g.onData(rc, buffer[:n])
				}
				if errors.Is(err, syscall.EINTR) {
					continue
				}
				if errors.Is(err, syscall.EAGAIN) {
					break
				}
				if err != nil {
					c.closeWithError(err)
					return
				}
				if n < len(buffer) {
					break
				}
			}
			c.ResetPollerEvent()
		})
		return
	}

	// If is not EPOLLONESHOT, the reading event may be re-dispatched for more than
	// once, here we reduce the duplicate reading events.
	cnt := atomic.AddInt32(&c.readEvents, 1)
	if cnt > 2 {
		atomic.AddInt32(&c.readEvents, -1)
		return
	}
	// Only handle it when it's the first reading event.
	if cnt > 1 {
		return
	}

	g.IOExecute(func(buffer []byte) {
		for {
			// try to read all the data available.
			for i := 0; i < g.MaxConnReadTimesPerEventLoop; i++ {
				rc, n, err := c.ReadAndGetConn(buffer)
				if n > 0 {
					g.onData(rc, buffer[:n])
				}
				if errors.Is(err, syscall.EINTR) {
					continue
				}
				if errors.Is(err, syscall.EAGAIN) {
					break
				}
				if err != nil {
					c.closeWithError(err)
					return
				}
				if n < len(buffer) {
					break
				}
			}
			if atomic.AddInt32(&c.readEvents, -1) == 0 {
				return
			}
		}
	})
}

// Read .
// Depracated .
// It was used to customize users' reading implementation, but better to use
// `ReadAndGetConn` instead, which can handle different types of connection and
// returns the consistent connection instance for UDP.
// Notice: non-blocking interface, should not be used as you use std.
func (c *Conn) Read(b []byte) (int, error) {
	// When the connection is closed and the fd is reused on Unix,
	// new connection maybe hold the same fd.
	// Use lock to prevent data confusion.
	c.mux.Lock()
	if c.closed {
		c.mux.Unlock()
		return 0, net.ErrClosed
	}

	_, n, err := c.doRead(b)
	c.mux.Unlock()
	// if err == nil {
	// 	c.p.g.afterRead(c)
	// }

	return n, err
}

// ReadAndGetConn handles reading for different types of connection.
// It returns the real connection:
//  1. For Non-UDP connection, it returns the Conn itself.
//  2. For UDP connection, it may be a UDP Server fd, then it returns consistent
//     Conn for the same socket which has the same local addr and remote addr.
//
// Notice: non-blocking interface, should not be used as you use std.
func (c *Conn) ReadAndGetConn(b []byte) (*Conn, int, error) {
	// When the connection is closed and the fd is reused on Unix,
	// new connection maybe hold the same fd.
	// Use lock to prevent data confusion.
	c.mux.Lock()
	if c.closed {
		c.mux.Unlock()
		return c, 0, net.ErrClosed
	}

	dstConn, n, err := c.doRead(b)
	c.mux.Unlock()
	// if err == nil {
	// 	c.p.g.afterRead(c)
	// }

	return dstConn, n, err
}

func (c *Conn) doRead(b []byte) (*Conn, int, error) {
	switch c.typ {
	case ConnTypeTCP, ConnTypeUnix:
		return c.readStream(b)
	case ConnTypeUDPServer, ConnTypeUDPClientFromDial:
		return c.readUDP(b)
	case ConnTypeUDPClientFromRead:
		// no need to read for this type of connection,
		// it's handled when reading ConnTypeUDPServer.
	default:
	}
	return c, 0, errors.New("invalid udp conn for reading")
}

// read from TCP/Unix socket.
func (c *Conn) readStream(b []byte) (*Conn, int, error) {
	nread, err := syscall.Read(c.fd, b)
	return c, nread, err
}

// read from UDP socket.
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
		// get or create and cache the consistent connection for the socket
		// that has the same local addr and remote addr.
		uc, ok := c.connUDP.getConn(c.p, c.fd, rAddr)
		if g.UDPReadTimeout > 0 {
			uc.SetReadDeadline(time.Now().Add(g.UDPReadTimeout))
		}
		if !ok {
			g.onOpen(uc)
		}
		dstConn = uc
	}

	return dstConn, nread, err
}

// Write writes data to the connection.
// Notice:
//  1. This is a non-blocking interface, but you can use it as you use std.
//  2. When it can't write all the data now, the connection will cache the data
//     left to be written and wait for the writing event then try to flush it.
func (c *Conn) Write(b []byte) (int, error) {
	// c.p.g.beforeWrite(c)

	c.mux.Lock()
	if c.closed {
		c.mux.Unlock()
		return -1, net.ErrClosed
	}

	n, err := c.write(b)
	if err != nil &&
		!errors.Is(err, syscall.EINTR) &&
		!errors.Is(err, syscall.EAGAIN) {
		c.closed = true
		c.mux.Unlock()
		c.closeWithErrorWithoutLock(err)
		return n, err
	}

	if len(c.writeList) == 0 {
		// no data left to be written, clear write deadline timer.
		if c.wTimer != nil {
			c.wTimer.Stop()
			c.wTimer = nil
		}
	} else {
		// has data left to be written, set writing event.
		c.modWrite()
	}

	c.mux.Unlock()
	return n, err
}

// Writev does similar things as Write, but with [][]byte input arg.
// Notice: doesn't support UDP if more than 1 []byte.
func (c *Conn) Writev(in [][]byte) (int, error) {
	// c.p.g.beforeWrite(c)

	c.mux.Lock()
	if c.closed {
		c.mux.Unlock()

		return 0, net.ErrClosed
	}

	var n int
	var err error
	switch len(in) {
	case 1:
		n, err = c.write(in[0])
	default:
		n, err = c.writev(in)
	}
	if err != nil &&
		!errors.Is(err, syscall.EINTR) &&
		!errors.Is(err, syscall.EAGAIN) {
		c.closed = true
		c.mux.Unlock()
		c.closeWithErrorWithoutLock(err)
		return n, err
	}
	if len(c.writeList) == 0 {
		// no data left to be written, clear write deadline timer.
		if c.wTimer != nil {
			c.wTimer.Stop()
			c.wTimer = nil
		}
	} else {
		// has data left to be written, set writing event.
		c.modWrite()
	}

	c.mux.Unlock()
	return n, err
}

// write to TCP/Unix socket.
func (c *Conn) writeStream(b []byte) (int, error) {
	return syscall.Write(c.fd, b)
}

// write to UDP dialer.
func (c *Conn) writeUDPClientFromDial(b []byte) (int, error) {
	return syscall.Write(c.fd, b)
}

// write to UDP connection which is from server reading.
func (c *Conn) writeUDPClientFromRead(b []byte) (int, error) {
	err := syscall.Sendto(c.fd, b, 0, c.connUDP.rAddr)
	if err != nil {
		return 0, err
	}
	return len(b), nil
}

// Close implements closes connection.
func (c *Conn) Close() error {
	return c.closeWithError(nil)
}

// CloseWithError closes connection with user specified error.
func (c *Conn) CloseWithError(err error) error {
	return c.closeWithError(err)
}

// LocalAddr returns the local network address, if known.
func (c *Conn) LocalAddr() net.Addr {
	return c.lAddr
}

// RemoteAddr returns the remote network address, if known.
func (c *Conn) RemoteAddr() net.Addr {
	return c.rAddr
}

// SetDeadline sets deadline for both read and write.
// If it is time.Zero, SetDeadline will clear the deadlines.
func (c *Conn) SetDeadline(t time.Time) error {
	c.mux.Lock()
	if !c.closed {
		if !t.IsZero() {
			g := c.p.g
			if c.rTimer == nil {
				c.rTimer = g.AfterFunc(time.Until(t), func() {
					c.closeWithError(errReadTimeout)
				})
			} else {
				c.rTimer.Reset(time.Until(t))
			}
			if c.wTimer == nil {
				c.wTimer = g.AfterFunc(time.Until(t), func() {
					c.closeWithError(errWriteTimeout)
				})
			} else {
				c.wTimer.Reset(time.Until(t))
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

func (c *Conn) setDeadline(timer **time.Timer, returnErr error, t time.Time) error {
	c.mux.Lock()
	defer c.mux.Unlock()
	if c.closed {
		return nil
	}
	if !t.IsZero() {
		if *timer == nil {
			*timer = c.p.g.AfterFunc(time.Until(t), func() {
				c.closeWithError(returnErr)
			})
		} else {
			(*timer).Reset(time.Until(t))
		}
	} else if *timer != nil {
		(*timer).Stop()
		(*timer) = nil
	}
	return nil
}

// SetReadDeadline sets the deadline for future Read calls.
// When the user doesn't update the deadline and the deadline exceeds,
// the connection will be closed.
// If it is time.Zero, SetReadDeadline will clear the deadline.
//
// Notice:
//  1. Users should update the read deadline in time.
//  2. For example, call SetReadDeadline whenever a new WebSocket message
//     is received.
func (c *Conn) SetReadDeadline(t time.Time) error {
	return c.setDeadline(&c.rTimer, errReadTimeout, t)
}

// SetWriteDeadline sets the deadline for future data writing.
// If it is time.Zero, SetReadDeadline will clear the deadline.
//
// If the next Write call writes all the data successfully and there's no data
// left to bewritten, the deadline timer will be cleared automatically;
// Else when the user doesn't update the deadline and the deadline exceeds,
// the connection will be closed.
func (c *Conn) SetWriteDeadline(t time.Time) error {
	return c.setDeadline(&c.wTimer, errWriteTimeout, t)
}

// SetNoDelay controls whether the operating system should delay
// packet transmission in hopes of sending fewer packets (Nagle's
// algorithm).  The default is true (no delay), meaning that data is
// sent as soon as possible after a Write.
func (c *Conn) SetNoDelay(nodelay bool) error {
	if nodelay {
		return syscall.SetsockoptInt(
			c.fd,
			syscall.IPPROTO_TCP,
			syscall.TCP_NODELAY,
			1,
		)
	}
	return syscall.SetsockoptInt(
		c.fd,
		syscall.IPPROTO_TCP,
		syscall.TCP_NODELAY,
		0,
	)
}

// SetReadBuffer sets the size of the operating system's
// receive buffer associated with the connection.
func (c *Conn) SetReadBuffer(bytes int) error {
	return syscall.SetsockoptInt(
		c.fd,
		syscall.SOL_SOCKET,
		syscall.SO_RCVBUF,
		bytes,
	)
}

// SetWriteBuffer sets the size of the operating system's
// transmit buffer associated with the connection.
func (c *Conn) SetWriteBuffer(bytes int) error {
	return syscall.SetsockoptInt(
		c.fd,
		syscall.SOL_SOCKET,
		syscall.SO_SNDBUF,
		bytes,
	)
}

// SetKeepAlive sets whether the operating system should send
// keep-alive messages on the connection.
func (c *Conn) SetKeepAlive(keepalive bool) error {
	if keepalive {
		return syscall.SetsockoptInt(
			c.fd,
			syscall.SOL_SOCKET,
			syscall.SO_KEEPALIVE,
			1,
		)
	}
	return syscall.SetsockoptInt(
		c.fd,
		syscall.SOL_SOCKET,
		syscall.SO_KEEPALIVE,
		0,
	)
}

// SetKeepAlivePeriod sets period between keep-alives.
func (c *Conn) SetKeepAlivePeriod(d time.Duration) error {
	if runtime.GOOS == "linux" {
		d += (time.Second - time.Nanosecond)
		secs := int(d.Seconds())
		if err := syscall.SetsockoptInt(
			c.fd,
			IPPROTO_TCP,
			TCP_KEEPINTVL,
			secs,
		); err != nil {
			return err
		}
		return syscall.SetsockoptInt(
			c.fd,
			IPPROTO_TCP,
			TCP_KEEPIDLE,
			secs,
		)
	}
	return errors.New("not supported")
}

// SetLinger .
func (c *Conn) SetLinger(onoff int32, linger int32) error {
	return syscall.SetsockoptLinger(
		c.fd,
		syscall.SOL_SOCKET,
		syscall.SO_LINGER,
		&syscall.Linger{
			Onoff:  onoff,  // 1
			Linger: linger, // 0
		},
	)
}

// sets writing event.
func (c *Conn) modWrite() {
	if !c.closed && !c.isWAdded {
		c.isWAdded = true
		c.p.modWrite(c.fd)
	}
}

// reset io event to read only.
func (c *Conn) resetRead() {
	if !c.closed && c.isWAdded {
		c.isWAdded = false
		p := c.p
		p.resetRead(c.fd)
	}
}

func (c *Conn) write(b []byte) (int, error) {
	if len(b) == 0 {
		return 0, nil
	}

	if c.overflow(len(b)) {
		return -1, errOverflow
	}

	if len(c.writeList) == 0 {
		n, err := c.doWrite(b)
		if err != nil &&
			!errors.Is(err, syscall.EINTR) &&
			!errors.Is(err, syscall.EAGAIN) {
			return n, err
		}
		if n < 0 {
			n = 0
		}
		left := len(b) - n
		if left > 0 && c.typ == ConnTypeTCP {
			c.newToWriteBuf(b[n:])
			// c.appendWrite(t)
		}
		return len(b), nil
	}

	c.newToWriteBuf(b)
	// c.appendWrite(t)

	return len(b), nil
}

func (c *Conn) writev(in [][]byte) (int, error) {
	size := 0
	for _, v := range in {
		size += len(v)
	}
	if c.overflow(size) {
		return -1, errOverflow
	}
	if len(c.writeList) > 0 {
		for _, v := range in {
			c.newToWriteBuf(v)
			// c.appendWrite(t)
		}
		return size, nil
	}

	nwrite, err := writev(c, in)
	if nwrite > 0 {
		n := nwrite
		onWrittenSize := c.p.g.onWrittenSize
		if n < size {
			for i := 0; i < len(in) && n > 0; i++ {
				b := in[i]
				if n == 0 {
					c.newToWriteBuf(b)
					// c.appendWrite(t)
				} else {
					if n < len(b) {
						if onWrittenSize != nil {
							onWrittenSize(c, b[:n], n)
						}
						c.newToWriteBuf(b[n:])
						// c.appendWrite(t)
						n = 0
					} else {
						if onWrittenSize != nil {
							onWrittenSize(c, b, len(b))
						}
						n -= len(b)
					}
				}
			}
		}
	} else {
		nwrite = 0
	}

	return nwrite, err
}

// func (c *Conn) appendWrite(t *toWrite) {
// c.writeList = append(c.writeList, t)
// if t.buf != nil {
// 	c.left += len(t.buf)
// }
// }

// flush cached data to the fd when writing available.
func (c *Conn) flush() error {
	c.mux.Lock()
	defer c.mux.Unlock()
	if c.closed {
		return net.ErrClosed
	}

	if len(c.writeList) == 0 {
		return nil
	}

	onWrittenSize := c.p.g.onWrittenSize

	// iovc := make([][]byte, 4)[0:0]
	// writeBuffers := func() error {
	// 	var (
	// 		n    int
	// 		err  error
	// 		head *toWrite
	// 	)

	// 	if len(c.writeList) == 1 {
	// 		head = c.writeList[0]
	// 		buf := head.buf[head.offset:]
	// 		for len(buf) > 0 && err == nil {
	// 			n, err = syscall.Write(c.fd, buf)
	// 			if n > 0 {
	// 				if c.p.g.onWrittenSize != nil {
	// 					c.p.g.onWrittenSize(c, buf[:n], n)
	// 				}
	// 				c.left -= n
	// 				head.offset += int64(n)
	// 				buf = buf[n:]
	// 				if len(buf) == 0 {
	// 					c.releaseToWrite(head)
	// 					c.writeList = nil
	// 				}
	// 			} else {
	// 				break
	// 			}
	// 		}
	// 		return err
	// 	}

	// 	writevSize := maxWriteCacheOrFlushSize
	// 	iovc = iovc[0:0]
	// 	for i := 0; i < len(c.writeList) && i < 1024; i++ {
	// 		head = c.writeList[i]
	// 		if head.buf != nil {
	// 			b := head.buf[head.offset:]
	// 			writevSize -= len(b)
	// 			if writevSize < 0 {
	// 				break
	// 			}
	// 			iovc = append(iovc, b)
	// 		}
	// 	}

	// 	for len(iovc) > 0 && err == nil {
	// 		n, err = writev(c, iovc)
	// 		if n > 0 {
	// 			c.left -= n
	// 			for n > 0 {
	// 				head = c.writeList[0]
	// 				headLeft := len(head.buf) - int(head.offset)
	// 				if n < headLeft {
	// 					if onWrittenSize != nil {
	// 						onWrittenSize(c, head.buf[head.offset:head.offset+int64(n)], n)
	// 					}
	// 					head.offset += int64(n)
	// 					iovc[0] = iovc[0][n:]
	// 					break
	// 				} else {
	// 					if onWrittenSize != nil {
	// 						onWrittenSize(c, head.buf[head.offset:], headLeft)
	// 					}
	// 					c.releaseToWrite(head)
	// 					c.writeList = c.writeList[1:]
	// 					if len(c.writeList) == 0 {
	// 						c.writeList = nil
	// 					}
	// 					iovc = iovc[1:]
	// 					n -= headLeft
	// 				}
	// 			}
	// 		} else {
	// 			break
	// 		}
	// 	}
	// 	return err
	// }
	writeBuffer := func() error {
		head := c.writeList[0]
		buf := head.buf[head.offset:]
		n, err := syscall.Write(c.fd, buf)
		if n > 0 {
			if c.p.g.onWrittenSize != nil {
				c.p.g.onWrittenSize(c, buf[:n], n)
			}
			c.left -= n
			head.offset += int64(n)
			if len(buf) == n {
				c.releaseToWrite(head)
				c.writeList[0] = nil
				c.writeList = c.writeList[1:]
			}
		}
		return err
	}
	writeFile := func() error {
		v := c.writeList[0]
		for v.remain > 0 {
			var offset = v.offset
			n, err := syscall.Sendfile(c.fd, v.fd, &offset, int(v.remain))
			if n > 0 {
				if onWrittenSize != nil {
					onWrittenSize(c, nil, n)
				}
				v.remain -= int64(n)
				v.offset += int64(n)
				if v.remain <= 0 {
					c.releaseToWrite(c.writeList[0])
					c.writeList = c.writeList[1:]
				}
			}
			if err != nil {
				return err
			}
		}
		return nil
	}

	for len(c.writeList) > 0 {
		var err error
		if c.writeList[0].fd == 0 {
			err = writeBuffer()
		} else {
			err = writeFile()
		}

		if errors.Is(err, syscall.EINTR) {
			continue
		}
		if errors.Is(err, syscall.EAGAIN) {
			// c.modWrite()
			return nil
		}
		if err != nil {
			c.closed = true
			c.closeWithErrorWithoutLock(err)
			return err
		}
	}

	c.resetRead()

	return nil
}

func (c *Conn) doWrite(b []byte) (int, error) {
	var n int
	var err error
	switch c.typ {
	case ConnTypeTCP, ConnTypeUnix:
		n, err = c.writeStream(b)
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

func (c *Conn) overflow(n int) bool {
	g := c.p.g
	return g.MaxWriteBufferSize > 0 && (c.left+n > g.MaxWriteBufferSize)
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

	if c.writeList != nil {
		for _, t := range c.writeList {
			c.releaseToWrite(t)
		}
		c.writeList = nil
	}

	if c.p != nil {
		c.p.deleteConn(c)
	}

	switch c.typ {
	case ConnTypeTCP, ConnTypeUnix:
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
	rAddrKey udpAddrKey

	mux   sync.RWMutex
	conns map[udpAddrKey]*Conn
}

func (u *udpConn) Close() error {
	parent := u.parent
	if parent.connUDP != u {
		// This connection is created by reding from a UDP server,
		// need to clear itself from the UDP server.
		parent.mux.Lock()
		delete(parent.connUDP.conns, u.rAddrKey)
		parent.mux.Unlock()
	} else {
		// This connection is a UDP server or dialer, need to close itself
		// and close all children if this is a server.
		syscall.Close(u.parent.fd)
		for _, c := range u.conns {
			c.Close()
		}
		u.conns = nil
	}
	return nil
}

func (u *udpConn) getConn(p *poller, fd int, rsa syscall.Sockaddr) (*Conn, bool) {
	rAddrKey := getUDPNetAddrKey(rsa)
	u.mux.RLock()
	c, ok := u.conns[rAddrKey]
	u.mux.RUnlock()

	// new connection, create it.
	if !ok {
		c = &Conn{
			p:     p,
			fd:    fd,
			lAddr: u.parent.lAddr,
			rAddr: getUDPNetAddr(rsa),
			typ:   ConnTypeUDPClientFromRead,
			connUDP: &udpConn{
				rAddr:    rsa,
				rAddrKey: rAddrKey,
				parent:   u.parent,
			},
		}

		// storage the consistent connection for the same remote addr.
		u.mux.Lock()
		u.conns[rAddrKey] = c
		u.mux.Unlock()
	}

	return c, ok
}

type udpAddrKey [22]byte

func getUDPNetAddrKey(sa syscall.Sockaddr) udpAddrKey {
	var ret udpAddrKey
	if sa == nil {
		return ret
	}

	switch vt := sa.(type) {
	case *syscall.SockaddrInet4:
		copy(ret[:], vt.Addr[:])
		binary.LittleEndian.PutUint16(ret[16:], uint16(vt.Port))
	case *syscall.SockaddrInet6:
		copy(ret[:], vt.Addr[:])
		binary.LittleEndian.PutUint16(ret[16:], uint16(vt.Port))
		binary.LittleEndian.PutUint32(ret[18:], vt.ZoneId)
	}
	return ret
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

func (c *Conn) SyscallConn() (syscall.RawConn, error) {
	return &rawConn{fd: c.fd}, nil
}

type rawConn struct {
	fd int
}

func (c *rawConn) Control(f func(fd uintptr)) error {
	f(uintptr(c.fd))
	return nil
}

func (c *rawConn) Read(f func(fd uintptr) (done bool)) error {
	return ErrUnsupported
}

func (c *rawConn) Write(f func(fd uintptr) (done bool)) error {
	return ErrUnsupported
}
