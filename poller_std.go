// Copyright 2020 lesismal. All rights reserved.
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

//go:build windows
// +build windows

package nbio

import (
	"errors"
	"net"
	"runtime"
	"time"

	"github.com/lesismal/nbio/logging"
)

const (
	// EPOLLLT .
	EPOLLLT = 0

	// EPOLLET .
	EPOLLET = 1

	// EPOLLONESHOT .
	EPOLLONESHOT = 0
)

type poller struct {
	g *Engine

	index int

	ReadBuffer []byte

	pollType   string
	isListener bool
	listener   net.Listener
	shutdown   bool

	chStop chan struct{}
}

//go:norace
func (p *poller) accept() error {
	conn, err := p.listener.Accept()
	if err != nil {
		return err
	}

	c := newConn(conn)
	o := p.g.pollers[c.Hash()%len(p.g.pollers)]
	o.addConn(c)

	return nil
}

//go:norace
func (p *poller) readConn(c *Conn) {
	for {
		pbuf := p.g.borrow(c)
		_, err := c.read(*pbuf)
		p.g.payback(c, pbuf)
		if err != nil {
			c.Close()
			return
		}
	}
}

//go:norace
func (p *poller) addConn(c *Conn) error {
	c.p = p
	p.g.mux.Lock()
	p.g.connsStd[c] = struct{}{}
	p.g.mux.Unlock()
	// should not call onOpen for udp server conn
	if c.typ != ConnTypeUDPServer {
		p.g.onOpen(c)
	} else {
		p.g.onUDPListen(c)
	}
	// should not read udp client from reading udp server conn
	if c.typ != ConnTypeUDPClientFromRead {
		go p.readConn(c)
	}

	return nil
}

//go:norace
func (p *poller) addDialer(c *Conn) error {
	c.p = p
	p.g.mux.Lock()
	p.g.connsStd[c] = struct{}{}
	p.g.mux.Unlock()
	go p.readConn(c)
	return nil
}

//go:norace
func (p *poller) deleteConn(c *Conn) {
	p.g.mux.Lock()
	delete(p.g.connsStd, c)
	p.g.mux.Unlock()
	// should not call onClose for udp server conn
	if c.typ != ConnTypeUDPServer {
		p.g.onClose(c, c.closeErr)
	}
}

//go:norace
func (p *poller) start() {
	if p.g.LockListener {
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
	}
	defer p.g.Done()

	logging.Debug("NBIO[%v][%v_%v] start", p.g.Name, p.pollType, p.index)
	defer logging.Debug("NBIO[%v][%v_%v] stopped", p.g.Name, p.pollType, p.index)

	if p.isListener {
		var err error
		p.shutdown = false
		for !p.shutdown {
			err = p.accept()
			if err != nil {
				var ne net.Error
				if ok := errors.As(err, &ne); ok && ne.Timeout() {
					logging.Error("NBIO[%v][%v_%v] Accept failed: timeout error, retrying...", p.g.Name, p.pollType, p.index)
					time.Sleep(time.Second / 20)
				} else {
					if !p.shutdown {
						logging.Error("NBIO[%v][%v_%v] Accept failed: %v, exit...", p.g.Name, p.pollType, p.index, err)
					}
					if p.g.onAcceptError != nil {
						p.g.onAcceptError(err)
					}
				}
			}

		}
	}
	<-p.chStop
}

//go:norace
func (p *poller) stop() {
	logging.Debug("NBIO[%v][%v_%v] stop...", p.g.Name, p.pollType, p.index)
	p.shutdown = true
	if p.isListener {
		p.listener.Close()
	}
	close(p.chStop)
}

//go:norace
func newPoller(g *Engine, isListener bool, index int) (*poller, error) {
	p := &poller{
		g:          g,
		index:      index,
		isListener: isListener,
		chStop:     make(chan struct{}),
	}

	if isListener {
		var err error
		var addr = g.Addrs[index%len(g.Addrs)]
		p.listener, err = g.Listen(g.Network, addr)
		if err != nil {
			return nil, err
		}
		p.pollType = "LISTENER"
	} else {
		p.pollType = "POLLER"
	}

	return p, nil
}

//go:norace
func (c *Conn) ResetPollerEvent() {

}
