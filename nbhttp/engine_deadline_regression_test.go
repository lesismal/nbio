package nbhttp

import (
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/lesismal/nbio"
)

func TestInitialReadDeadlineAfterHijack(t *testing.T) {
	const keepalive = 250 * time.Millisecond
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = listener.Close() }()
	client, err := net.DialTimeout("tcp", listener.Addr().String(), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = client.Close() }()
	server, err := listener.Accept()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = server.Close() }()

	var hijacked net.Conn
	var handlerErr, parseErr error
	engine := NewEngine(Config{
		NPoller:           1,
		SupportServerOnly: true,
		KeepaliveTime:     keepalive,
		ServerExecutor:    func(f func()) { f() },
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			hijacked, _, handlerErr = w.(http.Hijacker).Hijack()
			if handlerErr == nil {
				handlerErr = hijacked.SetReadDeadline(time.Time{})
			}
		}),
	})
	engine.Engine.OnOpen(func(c *nbio.Conn) {
		// Run the handler after poller assignment, before AddConn returns.
		parser := c.Session().(*Parser)
		parseErr = parser.Parse([]byte("GET / HTTP/1.1\r\nHost: localhost\r\n\r\n"))
	})
	if err := engine.Start(); err != nil {
		t.Fatal(err)
	}
	defer engine.Stop()
	engine.AddConnNonTLSNonBlocking(&Conn{Conn: server}, nil, func() {})
	if parseErr != nil || handlerErr != nil || hijacked == nil {
		t.Fatalf("hijack failed: parse=%v, handler=%v, conn=%v", parseErr, handlerErr, hijacked)
	}

	if err := client.SetReadDeadline(time.Now().Add(2 * keepalive)); err != nil {
		t.Fatal(err)
	}
	_, err = client.Read(make([]byte, 1))
	if timeout, ok := err.(net.Error); !ok || !timeout.Timeout() {
		t.Fatalf("cleared read deadline was overwritten: got %v, want client observation timeout", err)
	}
}
