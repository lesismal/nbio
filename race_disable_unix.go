//go:build linux || darwin || netbsd || freebsd || openbsd || dragonfly
// +build linux darwin netbsd freebsd openbsd dragonfly

package nbio

//go:norace
func noRaceConnOperation(g *Engine, c *Conn, op int) {
	p := g.pollers[c.Hash()%len(g.pollers)]
	switch op {
	case noRaceConnOpAdd:
		c.p = p
		p.addConn(c)
	case noRaceConnOpMod:
		p.modWrite(c.fd)
	case noRaceConnOpDel:
		p.deleteConn(c)
	}
}

//go:norace
func noRaceGetFdOnConn(c *Conn) int {
	return c.fd
}

//go:norace
func noRaceDeleteConnElemOnPoller(p *poller, fd int, c *Conn) {
	if c == p.g.connsUnix[fd] {
		p.g.connsUnix[fd] = nil
	}
}
