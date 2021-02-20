package tls

import (
	"github.com/lesismal/llib/std/crypto/tls"
	"github.com/lesismal/nbio"
)

// WrapOpen returns an opening handler of nbio.Gopher
func WrapOpen(tlsConfig *tls.Config, isClient bool, readBufferSize int, h func(c *nbio.Conn, tlsConn *tls.Conn)) func(c *nbio.Conn) {
	return func(c *nbio.Conn) {
		tlsConn := tls.NewConn(c, tlsConfig, isClient, true, readBufferSize)
		c.SetSession(tlsConn)
		if h != nil {
			h(c, tlsConn)
		}
	}
}

// WrapClose returns an closing handler of nbio.Gopher
func WrapClose(h func(c *nbio.Conn, tlsConn *tls.Conn, err error)) func(c *nbio.Conn, err error) {
	return func(c *nbio.Conn, err error) {
		if h != nil && c != nil {
			if session := c.Session(); session != nil {
				if tlsConn, ok := session.(*tls.Conn); ok {
					h(c, tlsConn, err)
				}
			}
		}
	}
}

// WrapData returns a data handler of nbio.Gopher
func WrapData(h func(c *nbio.Conn, tlsConn *tls.Conn, data []byte)) func(c *nbio.Conn, data []byte) {
	return func(c *nbio.Conn, data []byte) {
		if session := c.Session(); session != nil {
			if tlsConn, ok := session.(*tls.Conn); ok {
				tlsConn.Append(data)
				for {
					n, err := tlsConn.Read(tlsConn.ReadBuffer)
					if err != nil {
						c.Close()
						return
					}
					if n <= 0 {
						return
					}
					if h != nil {
						h(c, tlsConn, tlsConn.ReadBuffer[:n])
					}
					if n <= len(tlsConn.ReadBuffer) {
						return
					}
				}
			}
		}
	}
}
