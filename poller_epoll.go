// Copyright 2020 lesismal. All rights reserved.
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

//go:build linux
// +build linux

package nbio

import (
	"errors"
	"io"
	"net"
	"os"
	"runtime"
	"syscall"
	"time"
	"unsafe"

	"github.com/lesismal/nbio/logging"
)

const (
	// EPOLLLT .
	EPOLLLT = 0

	// EPOLLET .
	EPOLLET = 0x80000000
)

const (
	epollEventsRead  = syscall.EPOLLPRI | syscall.EPOLLIN
	epollEventsWrite = syscall.EPOLLOUT
	epollEventsError = syscall.EPOLLERR | syscall.EPOLLHUP | syscall.EPOLLRDHUP
)

const (
	IPPROTO_TCP   = syscall.IPPROTO_TCP
	TCP_KEEPINTVL = syscall.TCP_KEEPINTVL
	TCP_KEEPIDLE  = syscall.TCP_KEEPIDLE
)

type poller struct {
	g *Engine

	epfd  int
	evtfd int

	index int

	shutdown bool

	listener     net.Listener
	isListener   bool
	unixSockAddr string

	ReadBuffer []byte

	pollType string
}

func (p *poller) addConn(c *Conn) {
	if c.typ != ConnTypeUDPServer {
		p.g.onOpen(c)
	}
	fd := c.fd
	noRaceAddConnOnPoller(p, fd, c)
	err := p.addRead(fd)
	if err != nil {
		noRaceAddConnOnPoller(p, fd, nil)
		c.closeWithError(err)
		logging.Error("[%v] add read event failed: %v", c.fd, err)
	}
}

func (p *poller) getConn(fd int) *Conn {
	return noRaceGetConnOnPoller(p, fd)
}

func (p *poller) deleteConn(c *Conn) {
	if c == nil {
		return
	}
	fd := c.fd

	if c.typ != ConnTypeUDPClientFromRead {
		noRaceDeleteConnElemOnPoller(p, fd, c)
		p.deleteEvent(fd)
	}

	if c.typ != ConnTypeUDPServer {
		p.g.onClose(c, c.closeErr)
	}
}

func (p *poller) start() {
	defer p.g.Done()

	logging.Debug("NBIO[%v][%v_%v] start", p.g.Name, p.pollType, p.index)
	defer logging.Debug("NBIO[%v][%v_%v] stopped", p.g.Name, p.pollType, p.index)

	if p.isListener {
		p.acceptorLoop()
	} else {
		defer func() {
			syscall.Close(p.epfd)
			syscall.Close(p.evtfd)
		}()
		p.readWriteLoop()
	}
}

func (p *poller) acceptorLoop() {
	if p.g.lockListener {
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
	}

	noRaceSetShutdown(p, false)
	for !noRaceLoadShutdown(p) {
		conn, err := p.listener.Accept()
		if err == nil {
			var c *Conn
			c, err = NBConn(conn)
			if err != nil {
				conn.Close()
				continue
			}
			noRaceConnOperation(p.g, c, noRaceConnOpAdd)
		} else {
			var ne net.Error
			if ok := errors.As(err, &ne); ok && ne.Timeout() {
				logging.Error("NBIO[%v][%v_%v] Accept failed: temporary error, retrying...", p.g.Name, p.pollType, p.index)
				time.Sleep(time.Second / 20)
			} else {
				logging.Error("NBIO[%v][%v_%v] Accept failed: %v, exit...", p.g.Name, p.pollType, p.index, err)
				break
			}
		}
	}
}

func (p *poller) readWriteLoop() {
	if p.g.lockPoller {
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
	}

	msec := -1
	events := make([]syscall.EpollEvent, 1024)

	if p.g.onRead == nil && p.g.epollMod == EPOLLET {
		p.g.maxConnReadTimesPerEventLoop = 1<<31 - 1
	}

	noRaceSetShutdown(p, false)

	for !noRaceLoadShutdown(p) {
		n, err := syscall.EpollWait(p.epfd, events, msec)
		if err != nil && !errors.Is(err, syscall.EINTR) {
			return
		}

		if n <= 0 {
			msec = -1
			// runtime.Gosched()
			continue
		}
		msec = 20

		for _, ev := range events[:n] {
			fd := int(ev.Fd)
			switch fd {
			case p.evtfd:
			default:
				c := p.getConn(fd)
				if c != nil {
					if ev.Events&epollEventsError != 0 {
						c.closeWithError(io.EOF)
						continue
					}

					if ev.Events&epollEventsWrite != 0 {
						c.flush()
					}

					if ev.Events&epollEventsRead != 0 {
						if p.g.onRead == nil {
							for i := 0; i < p.g.maxConnReadTimesPerEventLoop; i++ {
								buffer := p.g.borrow(c)
								rc, n, err := c.ReadAndGetConn(buffer)
								if n > 0 {
									p.g.onData(rc, buffer[:n])
								}
								p.g.payback(c, buffer)
								if errors.Is(err, syscall.EINTR) {
									continue
								}
								if errors.Is(err, syscall.EAGAIN) {
									break
								}
								if err != nil {
									c.closeWithError(err)
								}
								if n < len(buffer) {
									break
								}
							}
						} else {
							p.g.onRead(c)
						}
					}
				} else {
					syscall.Close(fd)
					// p.deleteEvent(fd)
				}
			}
		}
	}
}

func (p *poller) stop() {
	logging.Debug("NBIO[%v][%v_%v] stop...", p.g.Name, p.pollType, p.index)
	noRaceSetShutdown(p, true)
	if p.listener != nil {
		p.listener.Close()
		if p.unixSockAddr != "" {
			os.Remove(p.unixSockAddr)
		}
	} else {
		n := uint64(1)
		syscall.Write(p.evtfd, (*(*[8]byte)(unsafe.Pointer(&n)))[:])
	}
}

func (p *poller) addRead(fd int) error {
	switch p.g.epollMod {
	case EPOLLET:
		return syscall.EpollCtl(p.epfd, syscall.EPOLL_CTL_ADD, fd, &syscall.EpollEvent{Fd: int32(fd), Events: syscall.EPOLLERR | syscall.EPOLLHUP | syscall.EPOLLRDHUP | syscall.EPOLLPRI | syscall.EPOLLIN | EPOLLET})
	default:
		return syscall.EpollCtl(p.epfd, syscall.EPOLL_CTL_ADD, fd, &syscall.EpollEvent{Fd: int32(fd), Events: syscall.EPOLLERR | syscall.EPOLLHUP | syscall.EPOLLRDHUP | syscall.EPOLLPRI | syscall.EPOLLIN})
	}
}

// func (p *poller) addWrite(fd int) error {
// 	return syscall.EpollCtl(p.epfd, syscall.EPOLL_CTL_ADD, fd, &syscall.EpollEvent{Fd: int32(fd), Events: syscall.EPOLLOUT})
// }

func (p *poller) modWrite(fd int) error {
	switch p.g.epollMod {
	case EPOLLET:
		return syscall.EpollCtl(p.epfd, syscall.EPOLL_CTL_MOD, fd, &syscall.EpollEvent{Fd: int32(fd), Events: syscall.EPOLLERR | syscall.EPOLLHUP | syscall.EPOLLRDHUP | syscall.EPOLLPRI | syscall.EPOLLIN | syscall.EPOLLOUT | EPOLLET})
	default:
		return syscall.EpollCtl(p.epfd, syscall.EPOLL_CTL_MOD, fd, &syscall.EpollEvent{Fd: int32(fd), Events: syscall.EPOLLERR | syscall.EPOLLHUP | syscall.EPOLLRDHUP | syscall.EPOLLPRI | syscall.EPOLLIN | syscall.EPOLLOUT})
	}
}

func (p *poller) deleteEvent(fd int) error {
	return syscall.EpollCtl(p.epfd, syscall.EPOLL_CTL_DEL, fd, &syscall.EpollEvent{Fd: int32(fd)})
}

func newPoller(g *Engine, isListener bool, index int) (*poller, error) {
	if isListener {
		if len(g.addrs) == 0 {
			panic("invalid listener num")
		}

		addr := g.addrs[index%len(g.addrs)]
		ln, err := g.listen(g.network, addr)
		if err != nil {
			return nil, err
		}

		p := &poller{
			g:          g,
			index:      index,
			listener:   ln,
			isListener: isListener,
			pollType:   "LISTENER",
		}
		if g.network == "unix" {
			p.unixSockAddr = addr
		}

		return p, nil
	}

	fd, err := syscall.EpollCreate1(0)
	if err != nil {
		return nil, err
	}

	r0, _, e0 := syscall.Syscall(syscall.SYS_EVENTFD2, 0, syscall.O_NONBLOCK, 0)
	if e0 != 0 {
		syscall.Close(fd)
		return nil, err
	}

	err = syscall.EpollCtl(fd, syscall.EPOLL_CTL_ADD, int(r0),
		&syscall.EpollEvent{Fd: int32(r0),
			Events: syscall.EPOLLIN,
		},
	)
	if err != nil {
		syscall.Close(fd)
		syscall.Close(int(r0))
		return nil, err
	}

	p := &poller{
		g:          g,
		epfd:       fd,
		evtfd:      int(r0),
		index:      index,
		isListener: isListener,
		pollType:   "POLLER",
	}

	return p, nil
}
