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

//go:norace
// equal (*poller).shutdown = b
func noRaceSetShutdown(p *poller, b bool) {
	p.shutdown = b
}

//go:norace
// equal returns (*poller).shutdown
func noRaceLoadShutdown(p *poller) bool {
	return p.shutdown
}

//go:norace
// equal return (*poller).(*Engine).connsUnix[fd]
func noRaceGetConnOnPoller(p *poller, fd int) *Conn {
	return p.g.connsUnix[fd]
}

//go:norace
// equal return g.pollers[index]
func noRaceGetPollerOnEngine(g *Engine, index int) *poller {
	return g.pollers[index]
}

//go:norace
// equal (*poller).(*Engine).connsUnix[fd] = c
func noRaceAddConnOnPoller(p *poller, fd int, c *Conn) {
	p.g.connsUnix[fd] = c
}

//go:norace
// equal return timerHeap.Len()
func noRaceLenTimers(ts timerHeap) int {
	return ts.Len()
}

//go:norace
// equal timerHeap[start:end]
func noRaceModifyLittleHeap(ts timerHeap, start, end int) timerHeap {
	return ts[start:end]
}

//go:norace
// equal *(*timerHeap) = ts
func noRaceUpdateLittleHeap(ptr *timerHeap, ts timerHeap) {
	*ptr = ts
}

//go:norace
// equal return (*Engine).([]*poller)[index].ReadBuffer
func noRaceGetReadBufferFromPoller(g *Engine, index int) []byte {
	return g.pollers[index].ReadBuffer
}
