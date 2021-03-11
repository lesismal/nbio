package nbhttp

import (
	"net"
	"net/http"
)

// Hijacker .
type Hijacker interface {
	Hijack() (net.Conn, error)
}

// Upgrader .
type Upgrader interface {
	Upgrade(w http.ResponseWriter, r *http.Request, responseHeader http.Header) error
	Read(p *Parser, data []byte) error
	OnClose(p *Parser, err error)
}
