// Copyright 2020 lesismal. All rights reserved.
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

// +build windows

package nbio

import (
	"runtime"
	"strings"
	"time"

	"github.com/lesismal/nbio/loging"
)

// Start init and start pollers
func (g *Gopher) Start() error {
	var err error

	g.lfds = []int{}

	g.listeners = make([]*poller, len(g.addrs))
	for i := range g.addrs {
		g.listeners[i], err = newPoller(g, true, int(i))
		if err != nil {
			for j := 0; j < i; j++ {
				g.listeners[j].stop()
			}
			return err
		}
	}

	for i := 0; i < g.pollerNum; i++ {
		g.pollers[i], err = newPoller(g, false, int(i))
		if err != nil {
			for j := 0; j < len(g.addrs); j++ {
				g.listeners[j].stop()
			}

			for j := 0; j < int(i); j++ {
				g.pollers[j].stop()
			}
			return err
		}
	}

	for i := 0; i < g.pollerNum; i++ {
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
		loging.Info("Gopher[%v] start", g.Name)
	} else {
		loging.Info("Gopher[%v] start listen on: [\"%v\"]", g.Name, strings.Join(g.addrs, `", "`))
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
	if conf.ReadBufferSize <= 0 {
		conf.ReadBufferSize = DefaultReadBufferSize
	}
	if conf.MinConnCacheSize == 0 {
		conf.MinConnCacheSize = DefaultMinConnCacheSize
	}

	g := &Gopher{
		Name:               conf.Name,
		network:            conf.Network,
		addrs:              conf.Addrs,
		pollerNum:          conf.NPoller,
		readBufferSize:     conf.ReadBufferSize,
		maxWriteBufferSize: conf.MaxWriteBufferSize,
		minConnCacheSize:   conf.MinConnCacheSize,
		listeners:          make([]*poller, len(conf.Addrs)),
		pollers:            make([]*poller, conf.NPoller),
		connsStd:           map[*Conn]struct{}{},
		trigger:            time.NewTimer(timeForever),
		chTimer:            make(chan struct{}),
	}

	g.initHandlers()

	g.OnReadBufferAlloc(func(c *Conn) []byte {
		if c.ReadBuffer == nil {
			c.ReadBuffer = make([]byte, int(g.readBufferSize))
		}
		return c.ReadBuffer
	})

	return g
}
