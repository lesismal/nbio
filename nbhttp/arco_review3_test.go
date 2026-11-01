// Copyright 2026 The nbio Authors.

// Adversarial review 3, glue-level regression tests (an independent review). Each failed before its fix.
//
//	go test -race -count=1 -run TestReview3 ./nbhttp/

package nbhttp

import (
	stdtls "crypto/tls"
	"io"
	"net"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/lesismal/nbio/crypto/tls"
)

// R3-2 at engine level: a GetCertificate that blocks (here 1.5 s, e.g. a
// cache miss that hits a database or ACME, keyed on the SNI the attacker
// chooses) runs on the handshake coroutine while the poller waits for it, so
// an unrelated, established connection on the same poller waits too. Before
// the fix the wait lasted as long as the callback; it is now bounded by
// NonBlockingHandshakeTimeout (here 300 ms). Callbacks must still be
// cache-only (crypto/tls/README.md).
func TestReview3SlowCallbackStallIsBounded(t *testing.T) {
	const stall = 1500 * time.Millisecond
	old := tls.NonBlockingHandshakeTimeout
	tls.NonBlockingHandshakeTimeout = 300 * time.Millisecond
	defer func() { tls.NonBlockingHandshakeTimeout = old }()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	ln.Close()

	cert := arcoTestCert(t)
	release := make(chan struct{})
	var once sync.Once
	defer once.Do(func() { close(release) })
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "ok") })
	engine := NewEngine(Config{
		Network:  "tcp",
		AddrsTLS: []string{addr},
		NPoller:  1,
		TLSConfig: &tls.Config{GetCertificate: func(h *tls.ClientHelloInfo) (*tls.Certificate, error) {
			if h.ServerName == "slow.example" {
				select {
				case <-release:
				case <-time.After(stall):
				}
			}
			return &cert, nil
		}},
		Handler: mux,
	})
	if err := engine.Start(); err != nil {
		t.Fatal(err)
	}
	defer engine.Stop()

	// Victim: an established keep-alive connection.
	victim := &http.Client{Transport: &http.Transport{TLSClientConfig: &stdtls.Config{
		InsecureSkipVerify: true, ServerName: "fast.example"}}}
	get := func() time.Duration {
		start := time.Now()
		resp, err := victim.Get("https://" + addr + "/")
		if err != nil {
			t.Fatal(err)
		}
		io.ReadAll(resp.Body)
		resp.Body.Close()
		return time.Since(start)
	}
	get() // handshake + first request

	// Attacker: one ClientHello with the slow SNI, then nothing.
	go func() {
		c, err := stdtls.Dial("tcp", addr, &stdtls.Config{InsecureSkipVerify: true, ServerName: "slow.example"})
		if err == nil {
			c.Close()
		}
	}()
	time.Sleep(100 * time.Millisecond)

	if d := get(); d > 800*time.Millisecond {
		t.Fatalf("a request on an established connection took %v while another connection's "+
			"GetCertificate was blocked: the stall is not bounded by NonBlockingHandshakeTimeout", d.Round(time.Millisecond))
	}
}
