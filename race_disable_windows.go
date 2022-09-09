//go:build windows
// +build windows

package nbio

//go:norace
func noRaceConnOperation(g *Engine, c *Conn, op int) {
	p := g.pollers[c.Hash()%len(g.pollers)]
	switch op {
	case noRaceConnOpAdd:
		p.addConn(c)
	case noRaceConnOpDel:
		p.deleteConn(c)
	}
	return
}
