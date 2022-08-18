package nbhttp

import "github.com/lesismal/nbio"
import "net/http"

// equal return (*nbhttp.Engine).([][]byte)[(*nbio.Conn).Hash % (*nbhttp.Engine).NParser]
//go:norace
func noRaceGetTlsBufferFromEngine(e *Engine, c *nbio.Conn) []byte {
	return e.tlsBuffers[uint64(c.Hash())%uint64(e.NParser)]
}

// equal (*nbhttp.Engine).([][]byte)[index] = make([]byte,(*nbhttp.Engine).ReadBufferSize)
//go:norace
func noRaceInitTlsBufferFormEngine(e *Engine, index int) {
	e.tlsBuffers[index] = make([]byte, e.ReadBufferSize)
}

// equal *(*Response) = rep
//go:norace
func noRaceSetResponse(ptr *http.Response, rep http.Response) {
	*ptr = rep
}
