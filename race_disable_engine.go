package nbio

//go:norace
func (g *Engine) _OnOpen(fn func(c *Conn)) {
	g.onOpen = fn
}

//go:norace
func (g *Engine) _OnClose(fn func(c *Conn, err error)) {
	g.onClose = fn
}

//go:norace
func (g *Engine) _OnRead(fn func(c *Conn)) {
	g.onRead = fn
}

//go:norace
func (g *Engine) _OnData(fn func(c *Conn, data []byte)) {
	g.onData = fn
}

//go:norace
func (g *Engine) _OnReadBufferAlloc(fn func(c *Conn) []byte) {
	g.onReadBufferAlloc = fn
}

//go:norace
func (g *Engine) _OnReadBufferFree(fn func(c *Conn, buffer []byte)) {
	g.onReadBufferFree = fn
}

//go:norace
func (g *Engine) _BeforeRead(fn func(c *Conn)) {
	g.beforeRead = fn
}

//go:norace
func (g *Engine) _AfterRead(fn func(c *Conn)) {
	g.afterRead = fn
}

//go:norace
func (g *Engine) _BeforeWrite(fn func(c *Conn)) {
	g.beforeWrite = fn
}

//go:norace
func (g *Engine) _OnStop(fn func()) {
	g.onStop = fn
}
