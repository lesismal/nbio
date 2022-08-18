package nbio

//go:norace
func (g *Engine) onOpenOnNoRace(fn func(c *Conn)) {
	g.onOpen = fn
}

//go:norace
func (g *Engine) onCloseOnNoRace(fn func(c *Conn, err error)) {
	g.onClose = fn
}

//go:norace
func (g *Engine) onReadOnNoRace(fn func(c *Conn)) {
	g.onRead = fn
}

//go:norace
func (g *Engine) onDataOnNoRace(fn func(c *Conn, data []byte)) {
	g.onData = fn
}

//go:norace
func (g *Engine) onReadBufferAllocOnNoRace(fn func(c *Conn) []byte) {
	g.onReadBufferAlloc = fn
}

//go:norace
func (g *Engine) onReadBufferFreeOnNoRace(fn func(c *Conn, buffer []byte)) {
	g.onReadBufferFree = fn
}

//go:norace
func (g *Engine) beforeReadOnNoRace(fn func(c *Conn)) {
	g.beforeRead = fn
}

//go:norace
func (g *Engine) afterReadOnNoRace(fn func(c *Conn)) {
	g.afterRead = fn
}

//go:norace
func (g *Engine) beforeWriteOnNoRace(fn func(c *Conn)) {
	g.beforeWrite = fn
}

//go:norace
func (g *Engine) onStopOnNoRace(fn func()) {
	g.onStop = fn
}
