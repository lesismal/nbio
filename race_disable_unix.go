//go:build linux || darwin || netbsd || freebsd || openbsd || dragonfly
// +build linux darwin netbsd freebsd openbsd dragonfly

package nbio

//go:norace
func noRaceConnOpOnEngine(g *Engine, index int, op string, c *Conn) {
	p := g.pollers[index]
	switch op {
	case "deleteConn":
		p.deleteConn(c)
	case "addConn":
		p.addConn(c)
	case "modWrite":
		p.modWrite(c.fd)
	}
}

//go:norace
func noRaceGetFdOnConn(c *Conn) int {
	return c.fd
}

//go:norace
//	equal if (*Conn) == (*poller).(*Engine).connsUnix[fd]
//	Is true equal (*poller).(*Engine).connsUnix[fd] = nil;(*poller).deleteEvent(fd)
func noRaceDeleteConnElemOnPoller(p *poller, fd int, c *Conn) {
	if c == p.g.connsUnix[fd] {
		p.g.connsUnix[fd] = nil
		p.deleteEvent(fd)
	}
}
