// Copyright 2020 lesismal. All rights reserved.
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

//go:build linux
// +build linux

package nbio

import (
	"errors"
	"fmt"
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

	// EPOLLONESHOT .
	EPOLLONESHOT = syscall.EPOLLONESHOT
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
	fd := c.fd
	if fd >= len(p.g.connsUnix) {
		c.closeWithError(fmt.Errorf("too many open files, fd[%d] >= MaxOpenFiles[%d]", fd, len(p.g.connsUnix)))
		return
	}
	c.p = p
	if c.typ != ConnTypeUDPServer {
		p.g.onOpen(c)
	}
	p.g.connsUnix[fd] = c
	err := p.addRead(fd)
	if err != nil {
		p.g.connsUnix[fd] = nil
		c.closeWithError(err)
		logging.Error("[%v] add read event failed: %v", c.fd, err)
	}
}

func (p *poller) getConn(fd int) *Conn {
	return p.g.connsUnix[fd]
}

func (p *poller) deleteConn(c *Conn) {
	if c == nil {
		return
	}
	fd := c.fd

	if c.typ != ConnTypeUDPClientFromRead {
		if c == p.g.connsUnix[fd] {
			p.g.connsUnix[fd] = nil
		}
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
	if p.g.LockListener {
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
	}

	p.shutdown = false
	for !p.shutdown {
		conn, err := p.listener.Accept()
		if err == nil {
			var c *Conn
			c, err = NBConn(conn)
			if err != nil {
				conn.Close()
				continue
			}
			p.g.pollers[c.Hash()%len(p.g.pollers)].addConn(c)
		} else {
			var ne net.Error
			if ok := errors.As(err, &ne); ok && ne.Timeout() {
				logging.Error("NBIO[%v][%v_%v] Accept failed: timeout error, retrying...", p.g.Name, p.pollType, p.index)
				time.Sleep(time.Second / 20)
			} else {
				if !p.shutdown {
					logging.Error("NBIO[%v][%v_%v] Accept failed: %v, exit...", p.g.Name, p.pollType, p.index, err)
				}
				break
			}
		}
	}
}

func (p *poller) readWriteLoop() {
	if p.g.LockPoller {
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
	}

	msec := -1
	events := make([]syscall.EpollEvent, 1024)
	isOneshot := p.g.EpollMod == EPOLLET && p.g.EPOLLONESHOT == EPOLLONESHOT

	if p.g.onRead == nil && p.g.EpollMod == EPOLLET {
		p.g.MaxConnReadTimesPerEventLoop = 1<<31 - 1
	}

	p.shutdown = false
	for !p.shutdown {
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
					if ev.Events&epollEventsWrite != 0 {
						c.flush()
					}

					if ev.Events&epollEventsRead != 0 {
						if p.g.onRead == nil {
							for i := 0; i < p.g.MaxConnReadTimesPerEventLoop; i++ {
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
									break
								}
								if n < len(buffer) {
									break
								}
							}
							if isOneshot {
								c.ResetPollerEvent()
							}
						} else {
							p.g.onRead(c)
						}
					}

					if ev.Events&epollEventsError != 0 {
						c.closeWithError(io.EOF)
						continue
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
	p.shutdown = true
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
	switch p.g.EpollMod {
	case EPOLLET:
		return syscall.EpollCtl(p.epfd, syscall.EPOLL_CTL_ADD, fd, &syscall.EpollEvent{Fd: int32(fd), Events: syscall.EPOLLERR | syscall.EPOLLHUP | syscall.EPOLLRDHUP | syscall.EPOLLPRI | syscall.EPOLLIN | syscall.EPOLLOUT | EPOLLET | p.g.EPOLLONESHOT})
	default:
		return syscall.EpollCtl(p.epfd, syscall.EPOLL_CTL_ADD, fd, &syscall.EpollEvent{Fd: int32(fd), Events: syscall.EPOLLERR | syscall.EPOLLHUP | syscall.EPOLLRDHUP | syscall.EPOLLPRI | syscall.EPOLLIN})
	}
}

func (p *poller) resetRead(fd int) error {
	switch p.g.EpollMod {
	case EPOLLET:
		if p.g.EPOLLONESHOT == EPOLLONESHOT {
			return syscall.EpollCtl(p.epfd, syscall.EPOLL_CTL_MOD, fd, &syscall.EpollEvent{Fd: int32(fd), Events: syscall.EPOLLERR | syscall.EPOLLHUP | syscall.EPOLLRDHUP | syscall.EPOLLPRI | syscall.EPOLLIN | syscall.EPOLLOUT | EPOLLET | EPOLLONESHOT})
		}
		return nil
	default:
		return syscall.EpollCtl(p.epfd, syscall.EPOLL_CTL_MOD, fd, &syscall.EpollEvent{Fd: int32(fd), Events: syscall.EPOLLERR | syscall.EPOLLHUP | syscall.EPOLLRDHUP | syscall.EPOLLPRI | syscall.EPOLLIN})
	}
}

func (p *poller) modWrite(fd int) error {
	switch p.g.EpollMod {
	case EPOLLET:
		if p.g.EPOLLONESHOT == EPOLLONESHOT {
			return syscall.EpollCtl(p.epfd, syscall.EPOLL_CTL_MOD, fd, &syscall.EpollEvent{Fd: int32(fd), Events: syscall.EPOLLERR | syscall.EPOLLHUP | syscall.EPOLLRDHUP | syscall.EPOLLPRI | syscall.EPOLLIN | syscall.EPOLLOUT | EPOLLONESHOT})
		}
		return nil
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
		return nil, e0
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

func (c *Conn) ResetPollerEvent() {
	p := c.p
	g := p.g
	fd := c.fd
	if !c.closed && g.EpollMod == EPOLLET && g.EPOLLONESHOT == EPOLLONESHOT {
		if len(c.writeList) == 0 {
			p.resetRead(fd)
		} else {
			p.modWrite(fd)
		}
	}
}
