// Copyright 2020 lesismal. All rights reserved.
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

//go:build linux || darwin || netbsd || freebsd || openbsd || dragonfly
// +build linux darwin netbsd freebsd openbsd dragonfly

package nbio

import (
	"net"
	"runtime"
	"strings"

	"github.com/lesismal/nbio/logging"
	"github.com/lesismal/nbio/timer"
)

// Start init and start pollers.
func (g *Engine) Start() error {
	udpListeners := make([]*net.UDPConn, len(g.addrs))[0:0]
	switch g.network {
	case "unix", "tcp", "tcp4", "tcp6":
		for i := range g.addrs {
			ln, err := newPoller(g, true, i)
			if err != nil {
				for j := 0; j < i; j++ {
					g.listeners[j].stop()
				}
				return err
			}
			g.addrs[i] = ln.listener.Addr().String()
			g.listeners = append(g.listeners, ln)
		}
	case "udp", "udp4", "udp6":
		for i, addrStr := range g.addrs {
			addr, err := net.ResolveUDPAddr(g.network, addrStr)
			if err != nil {
				for j := 0; j < i; j++ {
					udpListeners[j].Close()
				}
				return err
			}
			ln, err := g.listenUDP("udp", addr)
			if err != nil {
				for j := 0; j < i; j++ {
					udpListeners[j].Close()
				}
				return err
			}
			g.addrs[i] = ln.LocalAddr().String()
			udpListeners = append(udpListeners, ln)
		}
	}

	for i := 0; i < g.NPoller; i++ {
		p, err := newPoller(g, false, i)
		if err != nil {
			for j := 0; j < len(g.listeners); j++ {
				g.listeners[j].stop()
			}

			for j := 0; j < i; j++ {
				g.pollers[j].stop()
			}
			return err
		}
		g.pollers[i] = p
	}

	for i := 0; i < g.NPoller; i++ {
		g.pollers[i].ReadBuffer = make([]byte, g.ReadBufferSize)
		g.Add(1)
		go g.pollers[i].start()
	}

	for _, l := range g.listeners {
		g.Add(1)
		go l.start()
	}

	for _, ln := range udpListeners {
		_, err := g.AddConn(ln)
		if err != nil {
			for j := 0; j < len(g.listeners); j++ {
				g.listeners[j].stop()
			}

			for j := 0; j < len(g.pollers); j++ {
				g.pollers[j].stop()
			}

			for j := 0; j < len(udpListeners); j++ {
				udpListeners[j].Close()
			}

			return err
		}
	}

	g.Timer.Start()

	if len(g.addrs) == 0 {
		logging.Info("NBIO[%v] start", g.Name)
	} else {
		logging.Info("NBIO[%v] start listen on: [\"%v@%v\"]", g.Name, g.network, strings.Join(g.addrs, `", "`))
	}
	return nil
}

// NewEngine is a factory impl.
func NewEngine(conf Config) *Engine {
	cpuNum := runtime.NumCPU()
	if conf.Name == "" {
		conf.Name = "NB"
	}
	if conf.NPoller <= 0 {
		conf.NPoller = cpuNum
	}
	if conf.ReadBufferSize <= 0 {
		conf.ReadBufferSize = DefaultReadBufferSize
	}
	if conf.MaxConnReadTimesPerEventLoop <= 0 {
		conf.MaxConnReadTimesPerEventLoop = DefaultMaxConnReadTimesPerEventLoop
	}
	if conf.Listen == nil {
		conf.Listen = net.Listen
	}
	if conf.ListenUDP == nil {
		conf.ListenUDP = net.ListenUDP
	}

	g := &Engine{
		Config:    conf,
		Timer:     timer.New(conf.Name),
		listeners: make([]*poller, len(conf.Addrs))[0:0],
		pollers:   make([]*poller, conf.NPoller),
		connsUnix: make([]*Conn, MaxOpenFiles),
	}

	g.initHandlers()

	return g
}
