package nbio

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

// equal (*poller).shutdown = b
//go:norace
func noRaceSetShutdown(p *poller, b bool) {
	p.shutdown = b
}

// equal returns (*poller).shutdown
//go:norace
func noRaceLoadShutdown(p *poller) bool {
	return p.shutdown
}

// equal return (*poller).(*Engine).connsUnix[fd]
//go:norace
func noRaceGetConnOnPoller(p *poller, fd int) *Conn {
	return p.g.connsUnix[fd]
}

// equal return g.pollers[index]
//go:norace
func noRaceGetPollerOnEngine(g *Engine, index int) *poller {
	return g.pollers[index]
}

// equal (*poller).(*Engine).connsUnix[fd] = c
//go:norace
func noRaceAddConnOnPoller(p *poller, fd int, c *Conn) {
	p.g.connsUnix[fd] = c
}

// equal return timerHeap.Len()
//go:norace
func noRaceLenTimers(ts timerHeap) int {
	return ts.Len()
}

// equal timerHeap[start:end]
//go:norace
func noRaceModifyLittleHeap(ts timerHeap, start, end int) timerHeap {
	return ts[start:end]
}

// equal *(*timerHeap) = ts
//go:norace
func noRaceUpdateLittleHeap(ptr *timerHeap, ts timerHeap) {
	*ptr = ts
}

// equal return (*Engine).([]*poller)[index].ReadBuffer
//go:norace
func noRaceGetReadBufferFromPoller(g *Engine, index int) []byte {
	return g.pollers[index].ReadBuffer
}
