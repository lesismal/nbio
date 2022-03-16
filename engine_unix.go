// Copyright 2020 lesismal. All rights reserved.
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

//go:build linux || darwin || netbsd || freebsd || openbsd || dragonfly
// +build linux darwin netbsd freebsd openbsd dragonfly

package nbio

import (
	"runtime"
	"strings"
	"time"

	"github.com/lesismal/nbio/logging"
)

// Start init and start pollers.
func (g *Engine) Start() error {
	var err error

	for i := 0; i < len(g.addrs); i++ {
		g.listeners[i], err = newPoller(g, true, i)
		if err != nil {
			for j := 0; j < i; j++ {
				g.listeners[j].stop()
			}
			return err
		}
	}

	for i := 0; i < g.pollerNum; i++ {
		g.pollers[i], err = newPoller(g, false, i)
		if err != nil {
			for j := 0; j < len(g.listeners); j++ {
				g.listeners[j].stop()
			}

			for j := 0; j < i; j++ {
				g.pollers[j].stop()
			}
			return err
		}
	}

	for i := 0; i < g.pollerNum; i++ {
		g.pollers[i].ReadBuffer = make([]byte, g.readBufferSize)
		g.Add(1)
		go g.pollers[i].start()
	}
	for _, l := range g.listeners {
		g.Add(1)
		go l.start()
	}

	g.Add(1)
	go g.timerLoop()

	if len(g.addrs) == 0 {
		logging.Info("NBIO[%v] start", g.Name)
	} else {
		logging.Info("NBIO[%v] start listen on: [\"%v\"]", g.Name, strings.Join(g.addrs, `", "`))
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

	g := &Engine{
		Name:                         conf.Name,
		network:                      conf.Network,
		addrs:                        conf.Addrs,
		pollerNum:                    conf.NPoller,
		readBufferSize:               conf.ReadBufferSize,
		maxWriteBufferSize:           conf.MaxWriteBufferSize,
		maxConnReadTimesPerEventLoop: conf.MaxConnReadTimesPerEventLoop,
		epollMod:                     conf.EpollMod,
		lockListener:                 conf.LockListener,
		lockPoller:                   conf.LockPoller,
		listeners:                    make([]*poller, len(conf.Addrs)),
		pollers:                      make([]*poller, conf.NPoller),
		connsUnix:                    make([]*Conn, MaxOpenFiles),
		callings:                     []func(){},
		chCalling:                    make(chan struct{}, 1),
		trigger:                      time.NewTimer(timeForever),
		chTimer:                      make(chan struct{}),
	}

	g.initHandlers()

	return g
}
