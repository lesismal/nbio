// Copyright 2020 lesismal. All rights reserved.
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

//go:build linux || darwin || netbsd || freebsd || openbsd || dragonfly
// +build linux darwin netbsd freebsd openbsd dragonfly

package nbio

import (
	"runtime"
	"strings"
	"syscall"
	"time"

	"github.com/lesismal/nbio/logging"
)

// Start init and start pollers
func (g *Gopher) Start() error {
	var err error

	for i := 0; i < len(g.addrs); i++ {
		g.listeners[i], err = newPoller(g, true, int(i))
		if err != nil {
			for j := 0; j < int(i); j++ {
				g.listeners[j].stop()
			}
			return err
		}
	}

	for i := 0; i < g.pollerNum; i++ {
		g.pollers[i], err = newPoller(g, false, int(i))
		if err != nil {
			for j := 0; j < int(len(g.lfds)); j++ {
				syscall.Close(g.lfds[j])
			}

			for j := 0; j < int(len(g.listeners)); j++ {
				g.listeners[j].stop()
			}

			for j := 0; j < int(i); j++ {
				g.pollers[j].stop()
			}
			return err
		}
	}

	for i := 0; i < g.pollerNum; i++ {
		g.pollers[i].ReadBuffer = make([]byte, g.readBufferSize)
		g.Add(1)

		SafeGo(g.pollers[i].start)
	}
	for _, l := range g.listeners {
		g.Add(1)
		SafeGo(l.start)
	}

	g.Add(1)
	SafeGo(g.timerLoop)

	// monitor tls file

	if len(g.addrs) == 0 {
		logging.Info("Gopher[%v] start", g.Name)
	} else {
		logging.Info("Gopher[%v] start listen on: [\"%v\"]", g.Name, strings.Join(g.addrs, `", "`))
	}
	return nil
}

// NewGopher is a factory impl
func NewGopher(conf Config) *Gopher {
	cpuNum := runtime.NumCPU()
	if conf.Name == "" {
		conf.Name = "NB"
	}
	if conf.NPoller <= 0 {
		conf.NPoller = cpuNum
	}
	if len(conf.Addrs) > 0 && conf.NListener <= 0 {
		conf.NListener = 1
	}
	if conf.Backlog <= 0 {
		conf.Backlog = 1024 * 64
	}
	if conf.ReadBufferSize <= 0 {
		conf.ReadBufferSize = DefaultReadBufferSize
	}
	if conf.MinConnCacheSize == 0 {
		conf.MinConnCacheSize = DefaultMinConnCacheSize
	}
	if conf.MaxReadTimesPerEventLoop <= 0 {
		conf.MaxReadTimesPerEventLoop = DefaultMaxReadTimesPerEventLoop
	}

	g := &Gopher{
		Name:                     conf.Name,
		network:                  conf.Network,
		addrs:                    conf.Addrs,
		pollerNum:                conf.NPoller,
		backlogSize:              conf.Backlog,
		readBufferSize:           conf.ReadBufferSize,
		maxWriteBufferSize:       conf.MaxWriteBufferSize,
		maxReadTimesPerEventLoop: conf.MaxReadTimesPerEventLoop,
		minConnCacheSize:         conf.MinConnCacheSize,
		epollMod:                 conf.EpollMod,
		lockListener:             conf.LockListener,
		lockPoller:               conf.LockPoller,
		listeners:                make([]*poller, len(conf.Addrs)),
		pollers:                  make([]*poller, conf.NPoller),
		connsUnix:                make([]*Conn, MaxOpenFiles),
		callings:                 []func(){},
		chCalling:                make(chan struct{}, 1),
		trigger:                  time.NewTimer(timeForever),
		chTimer:                  make(chan struct{}),
	}

	g.initHandlers()

	return g
}
