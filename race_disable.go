package nbio

//go:norace
func cohereSetShutdown(p *poller, b bool) {
	p.shutdown = b
}

//go:norace
func coherePollerRun(g *Engine) {
	for i := 0; i < g.pollerNum; i++ {
		g.pollers[i].ReadBuffer = make([]byte, g.readBufferSize)
		g.Add(1)
		go g.pollers[i].start()
	}
}

//go:norace
func cohereLoadShutdown(p *poller) bool {
	return p.shutdown
}

//go:norace
func cohereDeleteConnElem(p *poller, fd int, c *Conn) {
	if c == p.g.connsUnix[fd] {
		p.g.connsUnix[fd] = nil
		p.deleteEvent(fd)
	}
}

//go:norace
func cohereGetConn(p *poller, fd int) *Conn {
	return p.g.connsUnix[fd]
}

//go:norace
func cohereAddConn(p *poller, fd int, c *Conn) {
	p.g.connsUnix[fd] = c
}

//go:norace
func cohereLenTimers(ts timerHeap) int {
	return ts.Len()
}

//go:norace
func cohereModifyLittleHeap(ts timerHeap, start, end int) timerHeap {
	return ts[start:end]
}

//go:norace
func cohereUpdateLittleHeap(ptr *timerHeap, ts timerHeap) {
	*ptr = ts
}
