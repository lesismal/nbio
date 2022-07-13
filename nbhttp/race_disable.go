package nbhttp

import "github.com/lesismal/nbio"
import "net/http"

//go:norace
// equal return (*nbhttp.Engine).([][]byte)[(*nbio.Conn).Hash % (*nbhttp.Engine).NParser]
func noRaceGetTlsBufferFromEngine(e *Engine, c *nbio.Conn) []byte {
	return e.tlsBuffers[uint64(c.Hash())%uint64(e.NParser)]
}

//go:norace
// equal (*nbhttp.Engine).([][]byte)[index] = make([]byte,(*nbhttp.Engine).ReadBufferSize)
func noRaceInitTlsBufferFormEngine(e *Engine, index int) {
	e.tlsBuffers[index] = make([]byte, e.ReadBufferSize)
}

//go:norace
// equal *(*Response) = rep
func noRaceSetResponse(ptr *http.Response, rep http.Response) {
	*ptr = rep
}
