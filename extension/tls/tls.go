package tls

// deprecated.

import (
	"github.com/lesismal/nbio"
	"github.com/lesismal/nbio/crypto/tls"
	"github.com/lesismal/nbio/mempool"
)

// Conn .
type Conn = tls.Conn

// Config .
type Config = tls.Config

// Dial returns a net.Conn to be added to a Engine.
//
//go:norace
func Dial(network, addr string, config *Config) (*tls.Conn, error) {
	tlsConn, err := tls.Dial(network, addr, config)
	if err != nil {
		return nil, err
	}

	return tlsConn, nil
}

// WrapOpen returns an opening handler of nbio.Engine.
//
//go:norace
func WrapOpen(tlsConfig *Config, isClient bool, h func(c *nbio.Conn, tlsConn *Conn)) func(c *nbio.Conn) {
	return func(c *nbio.Conn) {
		var tlsConn *tls.Conn
		sesseion := c.Session()
		if sesseion != nil {
			tlsConn = sesseion.(*tls.Conn)
		}
		if tlsConn == nil && !isClient {
			tlsConn = tls.NewConn(c, tlsConfig, isClient, true, mempool.DefaultMemPool)
			c.SetSession(tlsConn)
		}
		if h != nil {
			h(c, tlsConn)
		}
	}
}

// WrapClose returns an closing handler of nbio.Engine.
//
//go:norace
func WrapClose(h func(c *nbio.Conn, tlsConn *Conn, err error)) func(c *nbio.Conn, err error) {
	return func(c *nbio.Conn, err error) {
		if c == nil {
			return
		}
		if session := c.Session(); session != nil {
			if tlsConn, ok := session.(*Conn); ok {
				if !tlsConn.HandshakeComplete() {
					// Wake a handshake goroutine waiting for input, so it
					// exits now rather than at its timeout (as nbhttp's OnClose).
					_ = tlsConn.Close()
				}
				if h != nil {
					h(c, tlsConn, err)
				}
			}
		}
	}
}

// WrapData returns a data handler of nbio.Engine.
//
//go:norace
func WrapData(h func(c *nbio.Conn, tlsConn *Conn, data []byte), args ...interface{}) func(c *nbio.Conn, data []byte) {
	getBuffer := func() []byte {
		return make([]byte, 2048)
	}
	if len(args) > 0 {
		if bh, ok := args[0].(func() []byte); ok {
			getBuffer = bh
		}
	}
	return func(c *nbio.Conn, data []byte) {
		if session := c.Session(); session != nil {
			if tlsConn, ok := session.(*Conn); ok {
				defer tlsConn.ResetOrFreeBuffer()
				buffer := getBuffer()
				for {
					_, n, err := tlsConn.AppendAndRead(data, buffer)
					data = nil
					// Use the plaintext before the error: Read returns
					// (n > 0, io.EOF) when a close_notify follows the data.
					if h != nil && n > 0 {
						h(c, tlsConn, buffer[:n])
					}
					if err != nil {
						_ = c.Close()
						return
					}
					if n == 0 {
						return
					}
				}
			}
		}
	}
}
