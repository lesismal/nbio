//go:build windows
// +build windows

package nbio

//go:norace
func cohereConnOpOnEngine(g *Engine, index int, op string, c *Conn) {
	p := g.pollers[index]
	switch op {
	case "deleteConn":
		p.deleteConn(c)
	case "addConn":
		p.addConn(c)
	}
	return
}
