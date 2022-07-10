package nbhttp

import (
	"github.com/lesismal/nbio"
	"net/http"
)

//go:norace
// equal return (*nbhttp.Engine).([][]byte)[(*nbio.Conn).Hash % (*nbhttp.Engine).NParser]
func cohereGetTlsBufferFromEngine(e *Engine, c *nbio.Conn) []byte {
	return e.tlsBuffers[uint64(c.Hash())%uint64(e.NParser)]
}

//go:norace
// equal (*nbhttp.Engine).([][]byte)[index] = make([]byte,(*nbhttp.Engine).ReadBufferSize)
func cohereInitTlsBufferFormEngine(e *Engine, index int) {
	e.tlsBuffers[index] = make([]byte, e.ReadBufferSize)
}

//go:norace
// equal *(*Response) = rep
func cohereSetResponse(ptr *http.Response, rep http.Response) {
	*ptr = rep
}
