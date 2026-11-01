// Copyright 2026 The nbio Authors. BSD-style license (same as Go).

// This file is not part of Go's crypto/tls. It adds the non-blocking API that
// nbio uses (the same names as github.com/lesismal/llib/std/crypto/tls) on top
// of the unmodified Go code in this package. See crypto/tls/README.md.
//
// How it works, in short:
//
//   - A non-blocking Conn's transport is wrapped in an *nbConn. The poller
//     hands it input with AppendAndRead; nbConn.Read serves that input to
//     Go's record layer and returns errWouldBlock when it runs out.
//     errWouldBlock is a temporary net.Error, which Go's readRecordOrCCS does
//     not make sticky, so the next AppendAndRead resumes where Go stopped.
//     Partial records stay in Go's own c.rawInput.
//   - The handshake cannot be resumed that way (Go stores the handshake error
//     for good), so it runs on its own goroutine, used as a coroutine: the
//     poller passes it input and waits until it either needs more input or
//     finishes. Only one of the two runs at a time, so there is no concurrency
//     inside the handshake. When the handshake is done the goroutine exits and
//     the poller reads any input left over (e.g. the request that the client
//     sent together with its Finished) itself.
//   - After the handshake, reads run on the poller and writes on any goroutine,
//     both through Go's own locked paths (c.in, c.out). Nothing here removes or
//     bypasses a Go lock.
//   - ReleaseBuffers drops the read buffers when they hold no data, so an idle
//     connection keeps no input buffer.

package tls

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net"
	"runtime/debug"
	"sync"
	"sync/atomic"
	"time"
	"unsafe"
)

// Allocator is accepted for API compatibility with llib and ignored: buffers
// here are ordinary Go memory managed by the garbage collector.
type Allocator interface {
	Malloc(size int) *[]byte
	Realloc(buf *[]byte, size int) *[]byte
	Append(buf *[]byte, more ...byte) *[]byte
	AppendString(buf *[]byte, more string) *[]byte
	Free(buf *[]byte)
}

// NonBlockingHandshakeTimeout bounds the whole non-blocking handshake, from
// its first input until it completes: waiting for the peer and running config
// callbacks both count. When it expires the handshake fails and the transport
// is closed. It is read once when each handshake starts, on the poller; set it
// at startup. It is a backstop: servers should also set a read deadline on the
// transport.
var NonBlockingHandshakeTimeout = 10 * time.Second

// errWouldBlock is returned by nbConn.Read when a non-blocking connection has
// no more buffered input. It is temporary, so Go's record layer keeps its state.
var errWouldBlock net.Error = wouldBlockError{}

type wouldBlockError struct{}

func (wouldBlockError) Error() string   { return "tls: no buffered input (would block)" }
func (wouldBlockError) Timeout() bool   { return true }
func (wouldBlockError) Temporary() bool { return true }

var errHandshakeNotDriven = errors.New("tls: handshake started outside AppendAndRead on a non-blocking connection")

var errHandshakeTimeout = errors.New("tls: non-blocking handshake timed out waiting for the peer")

// handshakesInFlight counts handshake goroutines that exist right now.
var handshakesInFlight atomic.Int64

// HandshakesInFlight returns the number of non-blocking handshakes in
// progress, i.e. handshake goroutines currently alive.
func HandshakesInFlight() int64 { return handshakesInFlight.Load() }

// nbConn is the transport of a Conn created by NewConn. In non-blocking mode
// its Read serves input handed over by the poller; in blocking mode it is a
// plain pass-through to the transport.
type nbConn struct {
	net.Conn // the real transport, e.g. *nbio.Conn

	c        *Conn
	nonBlock bool
	hsErr    error // set by the poller when the handshake goroutine failed
	session  any

	// pending is input handed over by the poller and not yet read by Go's
	// record layer. The poller and the handshake goroutine take turns on it
	// (the coroutine hand-off); mu also covers the case where the poller
	// gives up on a handshake goroutine that is still running (abandon).
	mu      sync.Mutex
	pending []byte
	// owned reports whether pending points into memory we allocated (true)
	// or into the poller's buffer, which is only valid during the call (false).
	owned bool

	hs atomic.Pointer[handshaker] // set while a handshake goroutine exists

	closeOnce sync.Once
	closed    chan struct{} // closed by Close
}

func (f *nbConn) Read(p []byte) (int, error) {
	if !f.nonBlock {
		return f.Conn.Read(p)
	}
	if f.hs.Load() == nil && !f.handshakeDone() {
		// Handshake, Read or Write was called directly before AppendAndRead
		// started the handshake. Fail the handshake cleanly rather than
		// block the caller, and before touching pending (the poller's).
		return 0, errHandshakeNotDriven
	}
	f.mu.Lock()
	for len(f.pending) == 0 {
		f.mu.Unlock()
		hs := f.hs.Load()
		if hs == nil {
			// Post-handshake read on the poller: nothing more for now.
			return 0, errWouldBlock
		}
		if err := hs.waitForInput(f); err != nil {
			return 0, err
		}
		f.mu.Lock()
	}
	n := copy(p, f.pending)
	f.pending = f.pending[n:]
	if len(f.pending) == 0 {
		f.pending, f.owned = nil, false
	}
	f.mu.Unlock()
	return n, nil
}

// handshakeDone is set by the owning Conn; see Conn.nb.
func (f *nbConn) handshakeDone() bool { return f.c != nil && f.c.isHandshakeComplete.Load() }

func (f *nbConn) Close() error {
	f.closeOnce.Do(func() { close(f.closed) })
	return f.Conn.Close()
}

// feed appends poller input. The slice is only borrowed: it must be consumed
// or copied before the poller's callback returns (see keepPending).
func (f *nbConn) feed(b []byte) {
	if len(b) == 0 {
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.pending) == 0 {
		f.pending, f.owned = b, false
		return
	}
	merged := make([]byte, 0, len(f.pending)+len(b))
	merged = append(append(merged, f.pending...), b...)
	f.pending, f.owned = merged, true
}

// keepPending copies borrowed input that was not consumed, so that nothing
// keeps pointing into the poller's buffer after its callback returns.
func (f *nbConn) keepPending() {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.pending) > 0 && !f.owned {
		f.pending, f.owned = bytes.Clone(f.pending), true
	}
}

// handshaker is the coroutine state of one non-blocking handshake.
type handshaker struct {
	resume chan struct{} // poller → goroutine: more input is in pending
	yield  chan struct{} // goroutine → poller: pending is used up, need more
	done   chan struct{} // closed when the goroutine exits
	err    error         // handshake result, valid once done is closed

	deadline time.Time // NonBlockingHandshakeTimeout after the handshake started
}

// waitForInput runs on the handshake goroutine: it hands control back to the
// poller and waits for the next input, the connection's close, or the
// handshake deadline.
func (hs *handshaker) waitForInput(f *nbConn) error {
	select {
	case hs.yield <- struct{}{}:
	case <-f.closed:
		return net.ErrClosed
	}
	t := time.NewTimer(time.Until(hs.deadline))
	defer t.Stop()
	select {
	case <-hs.resume:
		return nil
	case <-f.closed:
		return net.ErrClosed
	case <-t.C:
		_ = f.Conn.Close() // the timeout error is what matters
		return errHandshakeTimeout
	}
}

// stepHandshake runs on the poller. It gives the handshake goroutine the
// input in f.pending (starting the goroutine on first use) and waits until
// the goroutine needs more input (done=false) or has finished (done=true).
func (c *Conn) stepHandshake(f *nbConn) (done bool, err error) {
	hs := f.hs.Load()
	if hs == nil {
		hs = &handshaker{
			resume:   make(chan struct{}),
			yield:    make(chan struct{}),
			done:     make(chan struct{}),
			deadline: time.Now().Add(NonBlockingHandshakeTimeout),
		}
		f.hs.Store(hs)
		handshakesInFlight.Add(1)
		go func() {
			defer handshakesInFlight.Add(-1)
			defer close(hs.done)
			defer func() {
				// A panicking callback (GetCertificate, GetConfigForClient,
				// VerifyConnection, ...) must fail this connection only, as
				// it did when llib ran the handshake under nbio's recover.
				// Go's handshakeContext releases its locks in defers.
				if r := recover(); r != nil {
					hs.err = fmt.Errorf("tls: panic during handshake: %v\n%s", r, debug.Stack())
					_ = f.Conn.Close() // hs.err already says why
				}
			}()
			hs.err = c.HandshakeContext(context.Background())
		}()
	} else {
		select {
		case hs.resume <- struct{}{}:
		case <-hs.done:
		case <-f.closed:
			return true, net.ErrClosed
		}
	}
	// The goroutine normally yields or finishes at once. It keeps the poller
	// waiting only while the handshake's crypto or a config callback
	// (GetCertificate, ...) runs, and never past the handshake deadline or
	// the connection's close. Callbacks must still be fast (cache-only): the
	// poller's other connections wait meanwhile, as with llib.
	t := time.NewTimer(time.Until(hs.deadline))
	defer t.Stop()
	select {
	case <-hs.yield:
		return false, nil
	case <-hs.done:
		return f.finish(hs)
	case <-f.closed:
		return true, f.abandon(net.ErrClosed)
	case <-t.C:
		return true, f.abandon(errHandshakeTimeout)
	}
}

// finish runs on the poller once the handshake goroutine has exited.
func (f *nbConn) finish(hs *handshaker) (bool, error) {
	f.hs.Store(nil)
	f.hsErr = hs.err
	return true, hs.err
}

// abandon runs on the poller when it gives up on a handshake whose goroutine
// may still be running: the connection fails with err, its input is dropped
// (the goroutine may still read pending; it then finds none and sees the
// close), and the transport is closed so that the goroutine ends as soon as
// its current step returns.
func (f *nbConn) abandon(err error) error {
	f.hsErr = err
	f.mu.Lock()
	f.pending, f.owned = nil, false
	f.mu.Unlock()
	_ = f.Close() // err is what matters
	return err
}

// NewConn returns a TLS connection over conn, with the same signature as
// llib's NewConn. isNonBlock selects the poller-driven mode, in which input
// is handed over with AppendAndRead (or Append) instead of being read from
// conn. The optional argument (an Allocator) is ignored.
func NewConn(conn net.Conn, config *Config, isClient bool, isNonBlock bool, v ...interface{}) *Conn {
	f := &nbConn{Conn: conn, nonBlock: isNonBlock, closed: make(chan struct{})}
	var c *Conn
	if isClient {
		c = Client(f, config)
	} else {
		c = Server(f, config)
	}
	f.c = c
	return c
}

// nb returns the wrapper of a connection made by NewConn, wrapping the
// transport now if the connection was made by Server or Client.
func (c *Conn) nb() *nbConn {
	if f, ok := c.conn.(*nbConn); ok {
		return f
	}
	c.in.Lock()
	defer c.in.Unlock()
	c.out.Lock()
	defer c.out.Unlock()
	if f, ok := c.conn.(*nbConn); ok {
		return f
	}
	f := &nbConn{Conn: c.conn, closed: make(chan struct{}), c: c}
	c.conn = f
	return f
}

// Conn returns the underlying transport.
func (c *Conn) Conn() net.Conn {
	if f, ok := c.conn.(*nbConn); ok {
		return f.Conn
	}
	return c.conn
}

// IsNonBlock reports whether input is handed over with AppendAndRead.
func (c *Conn) IsNonBlock() bool {
	f, ok := c.conn.(*nbConn)
	return ok && f.nonBlock
}

// Session returns the value stored by SetSession.
func (c *Conn) Session() interface{} {
	if f, ok := c.conn.(*nbConn); ok {
		return f.session
	}
	return nil
}

// SetSession stores a user value on the connection.
func (c *Conn) SetSession(session interface{}) {
	c.nb().session = session
}

// ResetConn replaces the transport and selects blocking or non-blocking mode,
// e.g. to move a connection that finished its handshake in blocking mode onto
// a poller. It must not be called while a non-blocking handshake is in
// progress (it would wait for the handshake, which waits for the poller).
//
// Only server connections and clients that finished their handshake in
// blocking mode can be non-blocking: a non-blocking client cannot start a
// handshake, since it has nothing to read first.
func (c *Conn) ResetConn(conn net.Conn, nonBlock bool, v ...interface{}) {
	f := c.nb()
	f.keepPending()
	c.in.Lock()
	defer c.in.Unlock()
	c.out.Lock()
	defer c.out.Unlock()
	f.Conn = conn
	f.nonBlock = nonBlock
}

// Append hands input to a non-blocking connection without reading plaintext.
// If the handshake is in progress the input is processed right away. Call
// Read or AppendAndRead(nil, buf) afterwards, before the poller's buffer is
// reused, to consume the rest.
func (c *Conn) Append(b []byte) (int, error) {
	_, _, err := c.appendAndRead(b, nil)
	return len(b), err
}

// AppendAndRead hands input to a non-blocking connection and reads up to
// len(out) bytes of plaintext. It returns len(in), the number of plaintext
// bytes read, and an error. Zero bytes and a nil error mean that more input
// is needed. Call it again with in == nil until it returns 0 bytes, so that
// all plaintext in the input is consumed.
func (c *Conn) AppendAndRead(in []byte, out []byte) (int, int, error) {
	return c.appendAndRead(in, out)
}

func (c *Conn) appendAndRead(in []byte, out []byte) (int, int, error) {
	f := c.nb()
	if !f.nonBlock {
		return 0, 0, errors.New("tls: AppendAndRead on a blocking connection")
	}
	if f.hsErr != nil {
		return len(in), 0, f.hsErr // the handshake failed: keep reporting it, buffer nothing
	}
	if overlaps(in, out) {
		// nbio passes the poller's buffer as both in and out: plaintext
		// written to out would overwrite ciphertext not yet consumed.
		in = bytes.Clone(in)
		f.feed(in)
		f.mu.Lock()
		f.owned = true
		f.mu.Unlock()
	} else {
		f.feed(in)
	}
	if !c.isHandshakeComplete.Load() {
		f.mu.Lock()
		idle := len(f.pending) == 0
		f.mu.Unlock()
		if idle && f.hs.Load() == nil {
			return len(in), 0, nil
		}
		done, err := c.stepHandshake(f)
		if err != nil {
			return len(in), 0, err
		}
		if !done {
			return len(in), 0, nil
		}
	}
	if out == nil {
		f.keepPending()
		return len(in), 0, nil
	}
	n, err := c.Read(out)
	if err != nil && errors.Is(err, errWouldBlock) {
		err = nil
	}
	return len(in), n, err
}

// ResetOrFreeBuffer keeps any unconsumed input (copying it out of the
// poller's buffer) and releases empty read buffers. nbio calls it after each
// read event.
func (c *Conn) ResetOrFreeBuffer() {
	if f, ok := c.conn.(*nbConn); ok {
		f.keepPending()
	}
	c.ReleaseBuffers()
}

// ResetRawInput keeps unconsumed input (copying it out of the caller's
// buffer) and releases empty read buffers. Unlike llib, it never discards
// buffered input.
func (c *Conn) ResetRawInput() { c.ResetOrFreeBuffer() }

// overlaps reports whether a and b share any memory.
func overlaps(a, b []byte) bool {
	if len(a) == 0 || len(b) == 0 {
		return false
	}
	a0, a1 := uintptr(unsafe.Pointer(&a[0])), uintptr(unsafe.Pointer(&a[len(a)-1]))
	b0 := uintptr(unsafe.Pointer(&b[0]))
	b1 := b0 + uintptr(cap(b)) - 1 // Read may write up to cap via out[:len]; be conservative
	return a0 <= b1 && b0 <= a1
}

// ReleaseBuffers drops the read-side buffers (raw records, decrypted data,
// handshake messages) when they hold no unread bytes, so that an idle
// connection keeps no input buffer. It is safe to call at any time; the
// buffers are reallocated on demand. It waits for a handshake in progress.
func (c *Conn) ReleaseBuffers() {
	if DisableBufferRelease || !c.isHandshakeComplete.Load() {
		return // the handshake goroutine owns c.in; buffers are freed afterwards
	}
	c.in.Lock()
	defer c.in.Unlock()
	if c.input.Len() != 0 {
		return
	}
	// c.input points into c.rawInput (records are decrypted in place).
	c.input.Reset(nil)
	if c.rawInput.Len() == 0 {
		c.rawInput = bytes.Buffer{}
	}
	if c.hand.Len() == 0 {
		c.hand = bytes.Buffer{}
	}
}

// HandshakeComplete reports whether the handshake has finished, without
// taking the handshake lock (ConnectionState waits for a handshake in
// progress, which would block a poller).
func (c *Conn) HandshakeComplete() bool { return c.isHandshakeComplete.Load() }

// DisableBufferRelease turns ReleaseBuffers into a no-op. It exists only to
// measure what releasing idle buffers saves (it approximates running the
// same adapter over the standard library's crypto/tls, which keeps them).
var DisableBufferRelease bool
