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
//	equal if (*Conn) == (*poller).(*Engine).connsUnix[fd]
//	Is true equal (*poller).(*Engine).connsUnix[fd] = nil;(*poller).deleteEvent(fd)
func cohereDeleteConnElemOnPoller(p *poller, fd int, c *Conn) {
	if c == p.g.connsUnix[fd] {
		p.g.connsUnix[fd] = nil
		p.deleteEvent(fd)
	}
}

//go:norace
// equal return (*poller).(*Engine).connsUnix[fd]
func cohereGetConnOnPoller(p *poller, fd int) *Conn {
	return p.g.connsUnix[fd]
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
// equal return (*Engine).([]*poller)[c.Hash()%(*Engine).pollerNum].ReadBuffer
func cohereGetReadBufferFromPoller(g *Engine, c *Conn) []byte {
	return g.pollers[uint32(c.Hash())%uint32(g.pollerNum)].ReadBuffer
}

//go:norace
// equal (*Engine).([]*poller)[(*Conn).Hash() % (*Engine).pollerNum].addConn(c)
func cohereAddConnOnEngine(g *Engine, c *Conn) {
	g.pollers[uint32(c.Hash())%uint32(g.pollerNum)].addConn(c)
}
