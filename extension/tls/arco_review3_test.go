// Copyright 2026 The nbio Authors.

// Adversarial review 3, glue-level regression test for extension/tls
// (an independent review). It failed before the fix.
//
//	go test -race -count=1 -run TestReview3 ./extension/tls/

package tls

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	stdtls "crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"io"
	"math/big"
	"net"
	"testing"
	"time"

	"github.com/lesismal/nbio"
	nbtls "github.com/lesismal/nbio/crypto/tls"
)

// R3-5: WrapClose did not close the *tls.Conn when the transport closes, so
// a client that sends part of a ClientHello and disconnects leaves its
// handshake goroutine (and handshake state) parked until
// NonBlockingHandshakeTimeout (10 s) instead of releasing it at once, as
// nbhttp's OnClose does. Connection churn of R/s keeps ~10*R goroutines alive.
func TestReview3WrapCloseLeavesHandshakeParked(t *testing.T) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "x"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour)}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	cfg := &Config{Certificates: []nbtls.Certificate{{Certificate: [][]byte{der}, PrivateKey: key}}}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	ln.Close()
	g := nbio.NewEngine(nbio.Config{Network: "tcp", Addrs: []string{addr}, NPoller: 1})
	g.OnOpen(WrapOpen(cfg, false, nil))
	g.OnData(WrapData(nil))
	g.OnClose(WrapClose(nil))
	if err := g.Start(); err != nil {
		t.Fatal(err)
	}
	defer g.Stop()

	// Capture a real ClientHello.
	a, b := net.Pipe()
	go stdtls.Client(a, &stdtls.Config{InsecureSkipVerify: true}).Handshake()
	hdr := make([]byte, 5)
	io.ReadFull(b, hdr)
	body := make([]byte, int(hdr[3])<<8|int(hdr[4]))
	io.ReadFull(b, body)
	a.Close()
	b.Close()
	hello := append(hdr, body...)

	before := nbtls.HandshakesInFlight()
	c, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	c.Write(hello[:len(hello)/2])
	deadline := time.Now().Add(2 * time.Second)
	for nbtls.HandshakesInFlight() == before && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if nbtls.HandshakesInFlight() == before {
		t.Fatal("handshake did not start")
	}
	c.Close()
	time.Sleep(1 * time.Second)
	if n := nbtls.HandshakesInFlight(); n != before {
		t.Fatalf("1 s after the client disconnected, %d handshake goroutine(s) still parked "+
			"(released only by NonBlockingHandshakeTimeout=%v)", n-before, nbtls.NonBlockingHandshakeTimeout)
	}
}
