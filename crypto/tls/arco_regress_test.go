// Copyright 2026 The nbio Authors. BSD-style license (same as Go).

// Regression tests for the findings of the llib TLS audit
// (a security audit of the previous TLS layer), run against the non-blocking mode.

package tls

import (
	"bytes"
	stdtls "crypto/tls"
	"encoding/binary"
	"errors"
	"io"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// sendKeyUpdates makes c send n KeyUpdate messages (each followed by the key
// change it announces), optionally interleaved with application data.
func sendKeyUpdates(c *Conn, n int, requested bool, data []byte) error {
	c.out.Lock()
	defer c.out.Unlock()
	cs := cipherSuiteTLS13ByID(c.cipherSuite)
	ku, err := (&keyUpdateMsg{updateRequested: requested}).marshal()
	if err != nil {
		return err
	}
	for i := 0; i < n; i++ {
		if _, err := c.writeRecordLocked(recordTypeHandshake, ku); err != nil {
			return err
		}
		c.setWriteTrafficSecret(cs, QUICEncryptionLevelInitial, cs.nextTrafficSecret(c.out.trafficSecret))
		if data != nil {
			if _, err := c.writeRecordLocked(recordTypeApplicationData, data); err != nil {
				return err
			}
		}
	}
	return nil
}

// TestAuditKeyUpdateFlood: GO-2026-6090 / CVE-2026-56862. llib accepted
// 5,000 consecutive KeyUpdates; the non-blocking server must stop after
// maxUselessRecords consecutive ones, like patched Go.
func TestAuditKeyUpdateFlood(t *testing.T) {
	s := startNonBlockServer(t, nbServerConfig(VersionTLS13), 4096, nil)
	c, err := Dial("tcp", s.ln.Addr().String(), testConfigClient.Clone())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	go sendKeyUpdates(c, 5000, false, nil)
	select {
	case err := <-s.done:
		if err == nil || err.Error() != "tls: too many non-advancing records" {
			t.Fatalf("server stopped with %v, want too many non-advancing records", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("server accepted the KeyUpdate flood")
	}
}

// TestAuditKeyUpdateInterleavedAccepted: KeyUpdates separated by application
// data are legitimate and must keep working (the counter resets).
func TestAuditKeyUpdateInterleavedAccepted(t *testing.T) {
	var got atomic.Int64
	s := startNonBlockServer(t, nbServerConfig(VersionTLS13), 4096, func(s *nbServer, b []byte) { got.Add(int64(len(b))) })
	c, err := Dial("tcp", s.ln.Addr().String(), testConfigClient.Clone())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if err := sendKeyUpdates(c, 200, false, []byte("x")); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for got.Load() != 200 {
		if time.Now().After(deadline) {
			t.Fatalf("server got %d bytes, want 200", got.Load())
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// TestAuditKeyUpdateWriteRace: llib finding 1. The poller answers a
// KeyUpdate(update_requested) by rekeying the write side while other
// goroutines broadcast and close. llib did this without c.out and -race
// reported 5 races. Here every write goes through Go's c.out lock: this test
// must be clean under -race, and every broadcast record must decrypt on the
// client (a torn cipher state or a reused nonce would fail the MAC).
func TestAuditKeyUpdateWriteRace(t *testing.T) {
	for _, suite := range []uint16{TLS_AES_128_GCM_SHA256, TLS_CHACHA20_POLY1305_SHA256} {
		t.Run(CipherSuiteName(suite), func(t *testing.T) {
			// TLS 1.3 suites are picked by AES hardware support, not by
			// Config.CipherSuites: pretend there is none to get ChaCha20.
			defer func(v bool) { hasAESGCMHardwareSupport = v }(hasAESGCMHardwareSupport)
			hasAESGCMHardwareSupport = suite != TLS_CHACHA20_POLY1305_SHA256
			testKeyUpdateWriteRace(t, suite)
		})
	}
}

func testKeyUpdateWriteRace(t *testing.T, suite uint16) {
	const writers, perWriter, keyUpdates = 4, 300, 300
	// The poller answers each attacker byte with a 64-byte "pong" frame
	// (writer id = writers). Without data between them, the server's own
	// KeyUpdate replies would trip the client's limit of consecutive
	// non-advancing records, as in real Go.
	var pongs atomic.Int64
	pong := func(s *nbServer, b []byte) {
		for range b {
			f := make([]byte, 64)
			binary.BigEndian.PutUint32(f, writers)
			binary.BigEndian.PutUint32(f[4:], uint32(pongs.Add(1)-1))
			if _, err := s.conn.Write(f); err != nil {
				t.Errorf("pong: %v", err)
			}
		}
	}
	s := startNonBlockServer(t, nbServerConfig(VersionTLS13), 4096, pong)
	c, err := Dial("tcp", s.ln.Addr().String(), testConfigClient.Clone())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	<-s.ready
	if c.cipherSuite != suite {
		t.Fatalf("negotiated %s", CipherSuiteName(c.cipherSuite))
	}

	msg := func(w, i int) []byte {
		b := make([]byte, 64)
		binary.BigEndian.PutUint32(b, uint32(w))
		binary.BigEndian.PutUint32(b[4:], uint32(i))
		return b
	}
	var wg sync.WaitGroup
	start := make(chan struct{})
	for w := 0; w < writers; w++ {
		wg.Add(1)
		go func(w int) { // the hub broadcasting
			defer wg.Done()
			<-start
			for i := 0; i < perWriter; i++ {
				if _, err := s.conn.Write(msg(w, i)); err != nil {
					t.Errorf("broadcast: %v", err)
					return
				}
			}
		}(w)
	}
	kuDone := make(chan error, 1)
	go func() { // the attacker: KeyUpdate(update_requested) + data, repeatedly
		<-start
		kuDone <- sendKeyUpdates(c, keyUpdates, true, []byte("k"))
	}()

	// The client reads all broadcasts; each 64-byte message must arrive intact.
	readErr := make(chan error, 1)
	go func() {
		next := make([]int, writers+1)
		buf := make([]byte, 64)
		for n := 0; n < writers*perWriter+keyUpdates; n++ {
			if _, err := io.ReadFull(c, buf); err != nil {
				readErr <- err
				return
			}
			w, i := int(binary.BigEndian.Uint32(buf)), int(binary.BigEndian.Uint32(buf[4:]))
			if w > writers || i != next[w] || !bytes.Equal(buf[8:], make([]byte, 56)) {
				readErr <- errors.New("corrupted or reordered broadcast")
				return
			}
			next[w]++
		}
		readErr <- nil
	}()
	close(start)
	wg.Wait()
	if err := <-kuDone; err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-readErr:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(20 * time.Second):
		t.Fatal("client did not receive all broadcasts")
	}
}

// TestAuditCloseDuringBroadcast: llib's closeNotify wrote without c.out, so
// a Close racing a broadcast could seal two records with the same nonce.
// Close here must interlock with Write (run under -race).
func TestAuditCloseDuringBroadcast(t *testing.T) {
	for i := 0; i < 20; i++ {
		s := startNonBlockServer(t, nbServerConfig(VersionTLS13), 4096, nil)
		c, err := Dial("tcp", s.ln.Addr().String(), testConfigClient.Clone())
		if err != nil {
			t.Fatal(err)
		}
		<-s.ready
		go io.Copy(io.Discard, c)
		var wg sync.WaitGroup
		for w := 0; w < 3; w++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for j := 0; j < 200; j++ {
					if _, err := s.conn.Write([]byte("broadcast")); err != nil {
						return
					}
				}
			}()
		}
		time.Sleep(time.Duration(i%5) * 100 * time.Microsecond)
		s.conn.Close()
		wg.Wait()
		c.Close()
	}
}

// TestAuditTicketAgeAdd: GO-2022-0531 / CVE-2022-30629. TLS 1.3 tickets
// must carry a random ticket_age_add (llib sent 0).
func TestAuditTicketAgeAdd(t *testing.T) {
	seen := map[uint32]bool{}
	for i := 0; i < 4; i++ {
		s := startNonBlockServer(t, nbServerConfig(VersionTLS13), 4096, func(s *nbServer, b []byte) { s.conn.Write(b) })
		cfg := testConfigClient.Clone()
		cache := NewLRUClientSessionCache(1)
		cfg.ClientSessionCache = cache
		c, err := Dial("tcp", s.ln.Addr().String(), cfg)
		if err != nil {
			t.Fatal(err)
		}
		c.Write([]byte("x")) // the echo makes the client read the ticket
		io.ReadFull(c, make([]byte, 1))
		cs, ok := cache.Get(cfg.ServerName)
		if !ok || cs.session == nil {
			t.Fatal("no session ticket")
		}
		if cs.session.ageAdd == 0 {
			t.Fatal("ticket_age_add is 0")
		}
		seen[cs.session.ageAdd] = true
		c.Close()
	}
	if len(seen) < 2 {
		t.Fatalf("ticket_age_add not random: %v", seen)
	}
}

// TestAuditDefaults: llib finding 5 (2021 defaults) and finding 4 (CPU
// feature detection never initialised). With a zero Config the server must
// refuse TLS 1.0/1.1 and RSA key exchange, use Extended Master Secret in TLS
// 1.2, negotiate X25519MLKEM768 with a current client, and prefer AES-GCM
// on CPUs with AES instructions.
func TestAuditDefaults(t *testing.T) {
	base := testConfigServer.Clone()
	base.MinVersion, base.MaxVersion, base.CipherSuites, base.CurvePreferences = 0, 0, nil, nil

	dial := func(t *testing.T, cc *stdtls.Config) (stdtls.ConnectionState, error) {
		s := startNonBlockServer(t, base, 4096, nil)
		c, err := stdtls.Dial("tcp", s.ln.Addr().String(), cc)
		if err != nil {
			return stdtls.ConnectionState{}, err
		}
		defer c.Close()
		return c.ConnectionState(), nil
	}

	t.Run("no TLS 1.1", func(t *testing.T) {
		cc := stdClientConfig()
		cc.MinVersion, cc.MaxVersion = stdtls.VersionTLS10, stdtls.VersionTLS11
		if _, err := dial(t, cc); err == nil {
			t.Fatal("TLS 1.1 accepted")
		}
	})
	t.Run("no RSA kex", func(t *testing.T) {
		cc := stdClientConfig()
		cc.MaxVersion = stdtls.VersionTLS12
		cc.CipherSuites = []uint16{stdtls.TLS_RSA_WITH_AES_128_GCM_SHA256}
		t.Setenv("GODEBUG", "tlsrsakex=1") // let the std client offer it
		if _, err := dial(t, cc); err == nil {
			t.Fatal("RSA key exchange accepted")
		}
	})
	t.Run("EMS in TLS 1.2", func(t *testing.T) {
		cc := stdClientConfig()
		cc.MaxVersion = stdtls.VersionTLS12
		st, err := dial(t, cc)
		if err != nil {
			t.Fatal(err)
		}
		if st.Version != stdtls.VersionTLS12 {
			t.Fatalf("version %x", st.Version)
		}
		// The server side of EMS is checked in TestAuditEMSServerSide.
	})
	t.Run("X25519MLKEM768", func(t *testing.T) {
		st, err := dial(t, stdClientConfig())
		if err != nil {
			t.Fatal(err)
		}
		if st.CurveID != stdtls.X25519MLKEM768 {
			t.Fatalf("curve %v, want X25519MLKEM768", st.CurveID)
		}
	})
	t.Run("AES-GCM preferred with AES hardware", func(t *testing.T) {
		if runtime.GOARCH != "arm64" && runtime.GOARCH != "amd64" {
			t.Skip("only checked on arm64/amd64")
		}
		// Go's TestMain sets hasAESGCMHardwareSupport = false for its recorded
		// transcripts, so check the detected values it is computed from.
		if !hasGCMAsmARM64 && !hasGCMAsmAMD64 {
			t.Fatal("no AES-GCM hardware support detected on " + runtime.GOARCH)
		}
		defer func(v bool) { hasAESGCMHardwareSupport = v }(hasAESGCMHardwareSupport)
		hasAESGCMHardwareSupport = true
		cc := stdClientConfig()
		cc.MaxVersion = stdtls.VersionTLS12
		cc.CipherSuites = []uint16{stdtls.TLS_ECDHE_ECDSA_WITH_CHACHA20_POLY1305, stdtls.TLS_ECDHE_RSA_WITH_CHACHA20_POLY1305,
			stdtls.TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256, stdtls.TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256}
		st, err := dial(t, cc)
		if err != nil {
			t.Fatal(err)
		}
		if name := stdtls.CipherSuiteName(st.CipherSuite); !bytes.Contains([]byte(name), []byte("AES")) {
			t.Fatalf("negotiated %s", name)
		}
	})
}

// TestAuditEMSServerSide checks that the TLS 1.2 server negotiates Extended
// Master Secret (RFC 7627), missing in llib.
func TestAuditEMSServerSide(t *testing.T) {
	s := startNonBlockServer(t, nbServerConfig(VersionTLS12), 4096, nil)
	c, err := Dial("tcp", s.ln.Addr().String(), testConfigClient.Clone())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	<-s.ready
	if !c.extMasterSecret {
		t.Fatal("client did not get EMS")
	}
	waitFor(t, func() bool { return s.conn.isHandshakeComplete.Load() })
	if !s.conn.extMasterSecret {
		t.Fatal("server did not negotiate EMS")
	}
}

func waitFor(t testing.TB, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("timed out")
		}
		time.Sleep(2 * time.Millisecond)
	}
}

// TestAuditKeyUpdateWithTrailingHandshakeData: GO-2026-4340 / CVE-2025-61730
// and its follow-up GO-2026-4870. Handshake bytes that arrive in the same
// record as a KeyUpdate were encrypted under the old key; the server must
// reject them instead of reading them at the new key (llib had no check), and
// must not deadlock on several KeyUpdates in one record.
func TestAuditKeyUpdateWithTrailingHandshakeData(t *testing.T) {
	s := startNonBlockServer(t, nbServerConfig(VersionTLS13), 4096, nil)
	c, err := Dial("tcp", s.ln.Addr().String(), testConfigClient.Clone())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	ku, err := (&keyUpdateMsg{updateRequested: true}).marshal()
	if err != nil {
		t.Fatal(err)
	}
	c.out.Lock()
	_, err = c.writeRecordLocked(recordTypeHandshake, append(append([]byte{}, ku...), ku...))
	c.out.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-s.done:
		if err == nil || err.Error() != "tls: handshake buffer not empty before setting read traffic secret" {
			t.Fatalf("server stopped with %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("server accepted handshake data spanning a key change (or deadlocked)")
	}
}
