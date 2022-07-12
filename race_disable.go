package nbio

//go:norace
func coherePollerRun(g *Engine) {
	for i := 0; i < g.pollerNum; i++ {
		g.pollers[i].ReadBuffer = make([]byte, g.readBufferSize)
		g.Add(1)
		go g.pollers[i].start()
	}
}

//go:norace
func cohereListenerRun(g *Engine) {
	for _, l := range g.listeners {
		g.Add(1)
		go l.start()
	}
}

//go:norace
// equal (*poller).shutdown = b
func cohereSetShutdown(p *poller, b bool) {
	p.shutdown = b
}

//go:norace
// equal returns (*poller).shutdown
func cohereLoadShutdown(p *poller) bool {
	return p.shutdown
}

//go:norace
// equal return (*poller).(*Engine).connsUnix[fd]
func cohereGetConnOnPoller(p *poller, fd int) *Conn {
	return p.g.connsUnix[fd]
}

//go:norace
// equal return g.pollers[index]
func cohereGetPollerOnEngine(g *Engine, index int) *poller {
	return g.pollers[index]
}

//go:norace
// equal (*poller).(*Engine).connsUnix[fd] = c
func cohereAddConnOnPoller(p *poller, fd int, c *Conn) {
	p.g.connsUnix[fd] = c
}

//go:norace
// equal return timerHeap.Len()
func cohereLenTimers(ts timerHeap) int {
	return ts.Len()
}

//go:norace
// equal timerHeap[start:end]
func cohereModifyLittleHeap(ts timerHeap, start, end int) timerHeap {
	return ts[start:end]
}

//go:norace
// equal *(*timerHeap) = ts
func cohereUpdateLittleHeap(ptr *timerHeap, ts timerHeap) {
	*ptr = ts
}

//go:norace
// equal return (*Engine).([]*poller)[index].ReadBuffer
func cohereGetReadBufferFromPoller(g *Engine, index int) []byte {
	return g.pollers[index].ReadBuffer
}
