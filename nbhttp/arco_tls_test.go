// Copyright 2026 The nbio Authors.

package nbhttp

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	stdtls "crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/lesismal/nbio/crypto/tls"
)

func arcoTestCert(t *testing.T) tls.Certificate {
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
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}
}

// TestArcoTLSEngineLargeBodies runs the real nbhttp engine over this repo's
// non-blocking TLS (TLSDataHandler passes the poller buffer as both input and
// output of AppendAndRead) and sends request bodies large enough to span many
// records and poller reads, in TLS 1.2 and 1.3.
func TestArcoTLSEngineLargeBodies(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	ln.Close()

	mux := http.NewServeMux()
	mux.HandleFunc("/len", func(w http.ResponseWriter, r *http.Request) {
		b, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		fmt.Fprintf(w, "%d:%x", len(b), checksum(b))
	})
	engine := NewEngine(Config{
		Network:         "tcp",
		AddrsTLS:        []string{addr},
		TLSConfig:       &tls.Config{Certificates: []tls.Certificate{arcoTestCert(t)}},
		Handler:         mux,
		ReadBufferSize:  4096,
		MaxHTTPBodySize: 8 << 20,
	})
	if err := engine.Start(); err != nil {
		t.Fatal(err)
	}
	defer engine.Stop()

	for _, vers := range []uint16{stdtls.VersionTLS12, stdtls.VersionTLS13} {
		client := &http.Client{Transport: &http.Transport{TLSClientConfig: &stdtls.Config{
			InsecureSkipVerify: true, MinVersion: vers, MaxVersion: vers}}}
		for _, size := range []int{1, 16 << 10, 300 << 10, 2 << 20} {
			body := make([]byte, size)
			for i := range body {
				body[i] = byte(i*31 + size)
			}
			resp, err := client.Post("https://"+addr+"/len", "application/octet-stream", bytes.NewReader(body))
			if err != nil {
				t.Fatalf("TLS %x size %d: %v", vers, size, err)
			}
			got, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			if want := fmt.Sprintf("%d:%x", size, checksum(body)); string(got) != want {
				t.Fatalf("TLS %x size %d: got %q want %q", vers, size, got, want)
			}
		}
		client.CloseIdleConnections()
	}
}

func checksum(b []byte) uint32 {
	var h uint32 = 2166136261
	for _, c := range b {
		h = (h ^ uint32(c)) * 16777619
	}
	return h
}
