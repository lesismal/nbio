package nbio

const (
	noRaceConnOpAdd = iota
	noRaceConnOpMod
	noRaceConnOpDel
)

//go:norace
func noRacePollerRun(g *Engine) {
	for i := 0; i < g.pollerNum; i++ {
		g.pollers[i].ReadBuffer = make([]byte, g.readBufferSize)
		g.Add(1)
		go g.pollers[i].start()
	}
}

//go:norace
func noRaceListenerRun(g *Engine) {
	for _, l := range g.listeners {
		g.Add(1)
		go l.start()
	}
}

//go:norace
func noRaceSetShutdown(p *poller, b bool) {
	p.shutdown = b
}

//go:norace
func noRaceLoadShutdown(p *poller) bool {
	return p.shutdown
}

//go:norace
func noRaceGetConnOnPoller(p *poller, fd int) *Conn {
	return p.g.connsUnix[fd]
}

//go:norace
func noRaceAddConnOnPoller(p *poller, fd int, c *Conn) {
	p.g.connsUnix[fd] = c
}

//go:norace
func noRaceGetReadBufferFromPoller(c *Conn) []byte {
	return c.p.ReadBuffer
}
