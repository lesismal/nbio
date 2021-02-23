package tls

import (
	"github.com/lesismal/llib/std/crypto/tls"
	"github.com/lesismal/nbio"
)

// Conn .
type Conn = tls.Conn

// Config .
type Config = tls.Config

// Dial returns a net.Conn to be added to a Gopher
func Dial(network, addr string, config *Config) (*tls.Conn, error) {
	tlsConn, err := tls.Dial(network, addr, config)
	if err != nil {
		return nil, err
	}

	return tlsConn, nil
}

// WrapOpen returns an opening handler of nbio.Gopher
func WrapOpen(tlsConfig *Config, isClient bool, readBufferSize int, h func(c *nbio.Conn, tlsConn *Conn)) func(c *nbio.Conn) {
	return func(c *nbio.Conn) {
		if !isClient {
			tlsConn := tls.NewConn(c, tlsConfig, isClient, true, readBufferSize)
			c.SetSession((*Conn)(tlsConn))
		}
		if h != nil {
			var tlsConn *tls.Conn
			sesseion := c.Session()
			if sesseion != nil {
				tlsConn = sesseion.(*tls.Conn)
			}
			h(c, tlsConn)
		}
	}
}

// WrapClose returns an closing handler of nbio.Gopher
func WrapClose(h func(c *nbio.Conn, tlsConn *Conn, err error)) func(c *nbio.Conn, err error) {
	return func(c *nbio.Conn, err error) {
		if h != nil && c != nil {
			if session := c.Session(); session != nil {
				if tlsConn, ok := session.(*Conn); ok {
					h(c, tlsConn, err)
				}
			}
		}
	}
}

// WrapData returns a data handler of nbio.Gopher
func WrapData(h func(c *nbio.Conn, tlsConn *Conn, data []byte)) func(c *nbio.Conn, data []byte) {
	return func(c *nbio.Conn, data []byte) {
		if session := c.Session(); session != nil {
			if tlsConn, ok := session.(*Conn); ok {
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
