package nbhttp

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	stdtls "crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/lesismal/llib/std/crypto/tls"
)

func testTLSCert(t *testing.T) tls.Certificate {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "localhost"},
		DNSNames:     []string{"localhost"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	cert, err := tls.X509KeyPair(
		pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}),
		pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}),
	)
	if err != nil {
		t.Fatal(err)
	}
	return cert
}

// holdConn buffers writes while hold is set, so that several TLS records
// reach the server in one TCP segment.
type holdConn struct {
	net.Conn
	mu   sync.Mutex
	hold bool
	buf  bytes.Buffer
}

func (c *holdConn) Write(b []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.hold {
		return c.buf.Write(b)
	}
	return c.Conn.Write(b)
}

func (c *holdConn) flush() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.hold = false
	// crypto/tls sets a past write deadline after sending close_notify.
	_ = c.SetWriteDeadline(time.Time{})
	_, err := c.Conn.Write(c.buf.Bytes())
	return err
}

// TestTLSRequestWithCloseNotify sends a request and a close_notify in the
// same read: the server must still handle the request. Read returns
// (n > 0, io.EOF) in that case, and the plaintext must be used before the
// error is checked.
func TestTLSRequestWithCloseNotify(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()

	handled := make(chan struct{}, 1)
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		select {
		case handled <- struct{}{}:
		default:
		}
	})
	engine := NewEngine(Config{
		Network:   "tcp",
		AddrsTLS:  []string{addr},
		TLSConfig: &tls.Config{Certificates: []tls.Certificate{testTLSCert(t)}},
		Handler:   mux,
	})
	if err := engine.Start(); err != nil {
		t.Fatal(err)
	}
	defer engine.Stop()

	for _, vers := range []uint16{stdtls.VersionTLS12, stdtls.VersionTLS13} {
		raw, err := net.Dial("tcp", addr)
		if err != nil {
			t.Fatal(err)
		}
		hc := &holdConn{Conn: raw}
		c := stdtls.Client(hc, &stdtls.Config{InsecureSkipVerify: true, MinVersion: vers, MaxVersion: vers})
		if err := c.Handshake(); err != nil {
			t.Fatalf("TLS %x: handshake: %v", vers, err)
		}
		hc.mu.Lock()
		hc.hold = true
		hc.mu.Unlock()
		if _, err := c.Write([]byte("GET / HTTP/1.1\r\nHost: localhost\r\n\r\n")); err != nil {
			t.Fatal(err)
		}
		if err := c.CloseWrite(); err != nil {
			t.Fatal(err)
		}
		if err := hc.flush(); err != nil {
			t.Fatal(err)
		}
		select {
		case <-handled:
		case <-time.After(3 * time.Second):
			t.Fatalf("TLS %x: request sent together with close_notify was not handled", vers)
		}
		_ = c.Close()
	}
}
