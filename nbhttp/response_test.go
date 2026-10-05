package nbhttp

import (
	"bufio"
	"bytes"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

type responseTestConn struct {
	bytes.Buffer
}

func (*responseTestConn) Close() error                     { return nil }
func (*responseTestConn) LocalAddr() net.Addr              { return nil }
func (*responseTestConn) RemoteAddr() net.Addr             { return nil }
func (*responseTestConn) SetDeadline(time.Time) error      { return nil }
func (*responseTestConn) SetReadDeadline(time.Time) error  { return nil }
func (*responseTestConn) SetWriteDeadline(time.Time) error { return nil }

type responseSendfileConn struct {
	responseTestConn
	called bool
	err    error
}

func (c *responseSendfileConn) Sendfile(f *os.File, remain int64) (int64, error) {
	c.called = true
	var r io.Reader = f
	if remain > 0 {
		r = io.LimitReader(r, remain)
	}
	if c.err != nil {
		r = io.LimitReader(r, 1)
	}
	n, err := io.Copy(&c.Buffer, r)
	if err != nil {
		return n, err
	}
	return n, c.err
}

func TestResponseReadFromLimitedReader(t *testing.T) {
	for _, disabled := range []bool{false, true} {
		for _, limit := range []int64{-1, 0, 3, 12} {
			t.Run(strconv.FormatBool(disabled)+"/"+strconv.FormatInt(limit, 10), func(t *testing.T) {
				conn := &responseTestConn{}
				engine := NewEngine(Config{DisableSendfile: disabled})
				res := NewResponse(&Parser{Conn: conn, Engine: engine}, httptest.NewRequest("GET", "/", nil))
				defer releaseResponse(res)
				res.WriteHeader(http.StatusOK)
				reader := &io.LimitedReader{R: strings.NewReader("abcdef"), N: limit}
				n, err := res.ReadFrom(reader)
				if err != nil {
					t.Fatal(err)
				}
				want := limit
				if want < 0 {
					want = 0
				}
				if want > 6 {
					want = 6
				}
				if n != want || reader.N != limit-want {
					t.Fatalf("ReadFrom = %d, remaining = %d; want %d, %d", n, reader.N, want, limit-want)
				}
				parts := strings.SplitN(conn.String(), "\r\n\r\n", 2)
				if len(parts) != 2 || parts[1] != "abcdef"[:want] {
					t.Fatalf("response = %q, want body %q", conn.String(), "abcdef"[:want])
				}
			})
		}
	}
}

func TestResponseReadFromFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "data")
	if err := os.WriteFile(path, []byte("abcdef"), 0600); err != nil {
		t.Fatal(err)
	}
	sendErr := errors.New("sendfile interrupted")
	for _, tc := range []struct {
		name     string
		fast     bool
		disabled bool
		limited  bool
		limit    int64
		want     int64
		err      error
	}{
		{name: "direct", fast: true, want: 4},
		{name: "limited", fast: true, limited: true, limit: 3, want: 3},
		{name: "past_eof", fast: true, limited: true, limit: 12, want: 4},
		{name: "zero", fast: true, limited: true},
		{name: "negative", fast: true, limited: true, limit: -1},
		{name: "partial_error", fast: true, limited: true, limit: 3, want: 1, err: sendErr},
		{name: "unsupported", limited: true, limit: 3, want: 3},
		{name: "disabled", fast: true, disabled: true, limited: true, limit: 3, want: 3},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f, err := os.Open(path)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = f.Close() }()
			if _, err := f.Seek(2, io.SeekStart); err != nil {
				t.Fatal(err)
			}
			fastConn := &responseSendfileConn{err: tc.err}
			var conn net.Conn = &fastConn.responseTestConn
			if tc.fast {
				conn = fastConn
			}
			engine := NewEngine(Config{DisableSendfile: tc.disabled})
			res := NewResponse(&Parser{Conn: conn, Engine: engine}, httptest.NewRequest("GET", "/", nil))
			defer releaseResponse(res)
			res.WriteHeader(http.StatusOK)
			var reader io.Reader = f
			limited := &io.LimitedReader{R: f, N: tc.limit}
			if tc.limited {
				reader = limited
			}
			n, err := res.ReadFrom(reader)
			if n != tc.want || !errors.Is(err, tc.err) {
				t.Fatalf("ReadFrom = (%d, %v), want (%d, %v)", n, err, tc.want, tc.err)
			}
			if tc.limited && limited.N != tc.limit-tc.want {
				t.Fatalf("remaining = %d, want %d", limited.N, tc.limit-tc.want)
			}
			wantCalled := tc.fast && !tc.disabled && (!tc.limited || tc.limit > 0)
			if fastConn.called != wantCalled {
				t.Fatalf("Sendfile called = %v, want %v", fastConn.called, wantCalled)
			}
			parts := strings.SplitN(fastConn.String(), "\r\n\r\n", 2)
			if len(parts) != 2 || parts[1] != "cdef"[:tc.want] {
				t.Fatalf("response = %q, want body %q", fastConn.String(), "cdef"[:tc.want])
			}
		})
	}
}

func TestResponseFileServerRange(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "data"), []byte("abcdef"), 0600); err != nil {
		t.Fatal(err)
	}
	conn := &responseTestConn{}
	req := httptest.NewRequest("GET", "/data", nil)
	req.Header.Set("Range", "bytes=2-4")
	res := NewResponse(&Parser{Conn: conn, Engine: NewEngine(Config{})}, req)
	defer releaseResponse(res)
	http.FileServer(http.Dir(dir)).ServeHTTP(res, req)
	raw := bytes.NewReader(conn.Bytes())
	reader := bufio.NewReader(raw)
	resp, err := http.ReadResponse(reader, req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusPartialContent || string(body) != "cde" {
		t.Fatalf("response = %d %q, want 206 cde", resp.StatusCode, body)
	}
	if reader.Buffered() != 0 || raw.Len() != 0 {
		t.Fatalf("extra bytes after range: buffered=%d, unread=%d", reader.Buffered(), raw.Len())
	}
}
