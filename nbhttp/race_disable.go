package nbhttp

import (
	"net/http"

	"github.com/lesismal/nbio"
)

//go:norace
func noRaceGetTlsBufferFromEngine(e *Engine, c *nbio.Conn) []byte {
	return e.tlsBuffers[uint64(c.Hash())%uint64(e.NParser)]
}

//go:norace
func noRaceInitTlsBufferFormEngine(e *Engine, index int) {
	e.tlsBuffers[index] = make([]byte, e.ReadBufferSize)
}

//go:norace
func noRaceSetResponse(ptr *http.Response, rep http.Response) {
	*ptr = rep
}
