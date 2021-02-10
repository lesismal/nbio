// +build linux

package nbio

import (
	"log"
	"runtime"
	"strings"
	"syscall"
)

// Start init and start pollers
func (g *Gopher) Start() error {
	var err error

	g.lfds = []int{}

	for _, addr := range g.addrs {
		fd, err := listen(g.network, addr, g.maxLoad)
		if err != nil {
			return err
		}

		g.lfds = append(g.lfds, fd)
	}

	for i := uint32(0); i < g.listenerNum; i++ {
		g.listeners[i], err = newPoller(g, true, int(i))
		if err != nil {
			for j := 0; j < int(i); j++ {
				syscall.Close(g.lfds[j])
				g.listeners[j].stop()
			}
			return err
		}
	}

	for i := uint32(0); i < g.pollerNum; i++ {
		g.pollers[i], err = newPoller(g, false, int(i))
		if err != nil {
			for j := 0; j < int(len(g.lfds)); j++ {
				syscall.Close(g.lfds[j])
				g.listeners[j].stop()
			}

			for j := 0; j < int(i); j++ {
				g.pollers[j].stop()
			}
			return err
		}
	}

	for i := uint32(0); i < g.pollerNum; i++ {
		if runtime.GOOS == "linux" {
			g.pollers[i].readBuffer = make([]byte, g.readBufferSize)
		}
		g.Add(1)
		go g.pollers[i].start()
	}
	for _, l := range g.listeners {
		g.Add(1)
		go l.start()
	}

	if len(g.addrs) == 0 {
		log.Printf("gopher start")
	} else {
		log.Printf("gopher start listen on: [\"%v\"]", strings.Join(g.addrs, `", "`))
	}
	return nil
}
