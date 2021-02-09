// +build darwin netbsd freebsd openbsd dragonfly windows

package nbio

import (
	"log"
	"strings"
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
		}
	}

	for i := uint32(0); i < g.pollerNum; i++ {
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

	for i := uint32(0); i < g.pollerNum; i++ {
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
