package starttls

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"
)

func newTestEngine(t *testing.T, conn DuplexConn) *Engine {
	t.Helper()
	e, err := NewEngine(Config{Conn: conn})
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	t.Cleanup(func() { _ = e.Close() })
	return e
}

func TestReadWriteLineRoundTrip(t *testing.T) {
	conn := newScriptedConn()
	conn.enqueueString("EHLO mail.example\r\n")
	e := newTestEngine(t, conn)

	line, err := e.ReadLine()
	if err != nil || line != "EHLO mail.example" {
		t.Fatalf("ReadLine = %q, %v", line, err)
	}
	if err := e.WriteLine("250 OK"); err != nil {
		t.Fatalf("WriteLine: %v", err)
	}
	if got, want := string(conn.bytesWritten()), "250 OK\r\n"; got != want {
		t.Fatalf("written = %q, want %q", got, want)
	}
}

func TestReadLineRejectsBadFraming(t *testing.T) {
	cases := map[string]string{
		"bare LF":        "MAIL FROM:<a@b>\nBODY\r\n",
		"embedded CR":    "MAIL\rFROM:<a@b>\r\n",
		"non-printable":  "MAIL\tFROM\r\n",
		"partial at EOF": "MAIL FROM:<a@b>",
	}
	for name, input := range cases {
		t.Run(name, func(t *testing.T) {
			conn := newScriptedConn()
			conn.enqueueString(input)
			conn.enqueueEOF()
			e := newTestEngine(t, conn)
			if _, err := e.ReadLine(); !errors.Is(err, ErrBadLine) {
				t.Fatalf("err = %v, want ErrBadLine", err)
			}
			if e.Phase() != Closed {
				t.Fatalf("phase = %s, want closed", e.Phase())
			}
		})
	}
}

func TestCleanEOF(t *testing.T) {
	conn := newScriptedConn()
	conn.enqueueString("QUIT\r\n")
	conn.enqueueEOF()
	e := newTestEngine(t, conn)

	line, err := e.ReadLine()
	if err != nil || line != "QUIT" {
		t.Fatalf("ReadLine = %q, %v", line, err)
	}
	if _, err := e.ReadLine(); err != io.EOF {
		t.Fatalf("second ReadLine err = %v, want io.EOF", err)
	}
	// A clean EOF is a half-close, not a failure: the engine stays usable.
	if e.Phase() != Plain {
		t.Fatalf("phase after EOF = %s, want plain", e.Phase())
	}
	if err := e.WriteLine("221 bye"); err != nil {
		t.Fatalf("write after read-side EOF: %v", err)
	}
	// Explicit Close is terminal and reports ErrClosed afterwards.
	if err := e.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if _, err := e.ReadLine(); !errors.Is(err, ErrClosed) {
		t.Fatalf("ReadLine after Close err = %v, want ErrClosed", err)
	}
}

func TestWriteLineValidation(t *testing.T) {
	conn := newScriptedConn()
	e := newTestEngine(t, conn)

	for _, bad := range []string{"", "two lines\r\nINJECT", "tab\there", strings.Repeat("a", DefaultMaxLine+1)} {
		err := e.WriteLine(bad)
		if err == nil {
			t.Fatalf("WriteLine(%q) succeeded, want error", bad)
		}
	}
	if len(conn.bytesWritten()) != 0 {
		t.Fatalf("invalid writes must not reach the wire, got %q", conn.bytesWritten())
	}
}

// TestPipelinedSameChunkRejected is the core vulnerability regression: the
// STARTTLS command and the next plaintext command arrive in one read. The
// engine must reject the connection before the upgrade callback runs and
// never hand the pipelined plaintext to the TLS layer.
func TestPipelinedSameChunkRejected(t *testing.T) {
	raw := newScriptedConn()
	raw.enqueueString("STARTTLS\r\nMAIL FROM:<attacker@evil>\r\n")
	tap := &tapConn{DuplexConn: raw}
	e := newTestEngine(t, tap)

	if line, err := e.ReadLine(); err != nil || line != "STARTTLS" {
		t.Fatalf("ReadLine = %q, %v", line, err)
	}

	called := false
	err := e.BeginUpgrade(context.Background(), func(context.Context, DuplexConn) (DuplexConn, error) {
		called = true
		t.Fatal("upgrade callback must not run when data is pipelined")
		return nil, nil
	})
	if !errors.Is(err, ErrPipelined) {
		t.Fatalf("BeginUpgrade err = %v, want ErrPipelined", err)
	}
	if called {
		t.Fatal("upgrade callback ran despite pipelining")
	}
	if e.Phase() != Closed || !raw.isClosed() {
		t.Fatalf("phase=%s rawClosed=%v, want closed/true", e.Phase(), raw.isClosed())
	}

	// The decisive proof: the upgrade callback never ran, so no TLS
	// session was ever created and the pipelined command could not be
	// interpreted inside one. The bytes remain plaintext-only and are
	// discarded with the closed connection.
	if called {
		t.Fatal("tls layer must never be initialized with pipelined data")
	}
	if _, err := e.ReadLine(); !errors.Is(err, ErrClosed) {
		t.Fatalf("read after reject err = %v, want ErrClosed", err)
	}
}

// TestPipelinedAcrossReadsRejected covers pipelined bytes delivered in a
// later chunk (over-read across the boundary) rather than the first chunk.
func TestPipelinedAcrossReadsRejected(t *testing.T) {
	raw := newScriptedConn()
	raw.enqueueString("STARTTLS\r\n")
	raw.enqueueString("MAIL FROM:<attacker@evil>\r\n")
	e := newTestEngine(t, raw)

	if line, err := e.ReadLine(); err != nil || line != "STARTTLS" {
		t.Fatalf("ReadLine = %q, %v", line, err)
	}
	// Simulate a larger read that already pulled the next plaintext command
	// into the engine-owned parser.
	tmp := make([]byte, 64)
	n, err := e.reader.conn.Read(tmp)
	if err != nil || n == 0 {
		t.Fatalf("priming read: n=%d err=%v", n, err)
	}
	e.reader.buf = append(e.reader.buf, tmp[:n]...)

	err = e.BeginUpgrade(context.Background(), func(context.Context, DuplexConn) (DuplexConn, error) {
		t.Fatal("upgrade callback must not run when data is pipelined")
		return nil, nil
	})
	if !errors.Is(err, ErrPipelined) {
		t.Fatalf("BeginUpgrade err = %v, want ErrPipelined", err)
	}
	if !raw.isClosed() {
		t.Fatal("raw connection must be closed after pipelining rejection")
	}
}

// TestUpgradeSuccessIsolatesSessions proves the happy path: pre-upgrade
// plaintext is confined to the plaintext session, the callback receives the
// raw connection, and post-upgrade commands only exist on the new stream.
func TestUpgradeSuccessIsolatesSessions(t *testing.T) {
	raw := newScriptedConn()
	raw.enqueueString("STARTTLS\r\n")
	tap := &tapConn{DuplexConn: raw}
	e := newTestEngine(t, tap)

	if line, err := e.ReadLine(); err != nil || line != "STARTTLS" {
		t.Fatalf("ReadLine = %q, %v", line, err)
	}

	post := newScriptedConn()
	post.enqueueString("MAIL FROM:<ok@example>\r\n")
	var handed DuplexConn
	upgrader := func(_ context.Context, rawConn DuplexConn) (DuplexConn, error) {
		handed = rawConn
		return newFakeTLSConn(rawConn, post), nil
	}
	if err := e.BeginUpgrade(context.Background(), upgrader); err != nil {
		t.Fatalf("BeginUpgrade: %v", err)
	}
	if e.Phase() != TLS {
		t.Fatalf("phase = %s, want tls", e.Phase())
	}
	if handed != DuplexConn(tap) {
		t.Fatal("upgrade callback did not receive the original raw connection")
	}

	// A fresh parser backs the TLS phase; no stale plaintext state survives.
	if e.reader.buffered() != 0 {
		t.Fatalf("tls reader starts with %d buffered bytes, want 0", e.reader.buffered())
	}
	line, err := e.ReadLine()
	if err != nil || line != "MAIL FROM:<ok@example>" {
		t.Fatalf("post-upgrade ReadLine = %q, %v", line, err)
	}
	if err := e.WriteLine("250 2.1.0 OK"); err != nil {
		t.Fatalf("post-upgrade WriteLine: %v", err)
	}
	if got := string(post.bytesWritten()); got != "250 2.1.0 OK\r\n" {
		t.Fatalf("post conn written = %q", got)
	}
	// The raw/tap layer never saw the post-upgrade command.
	if bytes.Contains(tap.readBytes(), []byte("MAIL FROM")) {
		t.Fatalf("post-upgrade command leaked to raw layer: %q", tap.readBytes())
	}
	if len(raw.bytesWritten()) != 0 {
		t.Fatalf("raw conn received post-upgrade bytes: %q", raw.bytesWritten())
	}
}

func TestHandshakeFailureCloses(t *testing.T) {
	raw := newScriptedConn()
	raw.enqueueString("STARTTLS\r\n")
	e := newTestEngine(t, raw)

	if _, err := e.ReadLine(); err != nil {
		t.Fatalf("ReadLine: %v", err)
	}
	err := e.BeginUpgrade(context.Background(), func(context.Context, DuplexConn) (DuplexConn, error) {
		return nil, errHandshake
	})
	if !errors.Is(err, errHandshake) {
		t.Fatalf("BeginUpgrade err = %v, want errHandshake", err)
	}
	if e.Phase() != Closed || !raw.isClosed() {
		t.Fatalf("phase=%s rawClosed=%v, want closed/true", e.Phase(), raw.isClosed())
	}

	// A half-built connection returned alongside the error is closed too.
	raw2 := newScriptedConn()
	raw2.enqueueString("STARTTLS\r\n")
	e2 := newTestEngine(t, raw2)
	if _, err := e2.ReadLine(); err != nil {
		t.Fatalf("ReadLine: %v", err)
	}
	post := newScriptedConn()
	half := newFakeTLSConn(raw2, post)
	err = e2.BeginUpgrade(context.Background(), func(context.Context, DuplexConn) (DuplexConn, error) {
		return half, errHandshake
	})
	if !errors.Is(err, errHandshake) {
		t.Fatalf("BeginUpgrade err = %v", err)
	}
	if !post.isClosed() || e2.Phase() != Closed {
		t.Fatal("half-built upgraded connection must be closed")
	}
}

func TestServerRejectsUpgrade(t *testing.T) {
	raw := newScriptedConn()
	// Server says 554 instead of 220, then keeps the plaintext dialogue.
	raw.enqueueString("554 TLS not available\r\nNOOP\r\n")
	e := newTestEngine(t, raw)

	err := e.ClientStartTLS(context.Background(), func(context.Context, DuplexConn) (DuplexConn, error) {
		t.Fatal("upgrade must not be attempted after refusal")
		return nil, nil
	})
	rejected, ok := AsUpgradeRejected(err)
	if !ok {
		t.Fatalf("err = %v, want *UpgradeRejectedError", err)
	}
	if rejected.Code != 554 || !strings.Contains(rejected.Reply, "554") {
		t.Fatalf("rejected = %+v", rejected)
	}
	if e.Phase() != Plain {
		t.Fatalf("phase = %s, want plain after refusal", e.Phase())
	}
	// Plaintext session remains usable and buffered continuation lines work.
	line, err := e.ReadLine()
	if err != nil || line != "NOOP" {
		t.Fatalf("ReadLine after refusal = %q, %v", line, err)
	}
	if got := string(raw.bytesWritten()); got != "STARTTLS\r\n" {
		t.Fatalf("wire = %q, want only STARTTLS command", got)
	}
}

// noHalfCloseConn is a DuplexConn without CloseWrite/CloseRead.
type noHalfCloseConn struct{}

func (noHalfCloseConn) Read([]byte) (int, error)    { return 0, io.EOF }
func (noHalfCloseConn) Write(p []byte) (int, error) { return len(p), nil }
func (noHalfCloseConn) Close() error                { return nil }

func TestHalfClose(t *testing.T) {
	raw := newScriptedConn()
	e := newTestEngine(t, raw)

	if err := e.CloseWrite(); err != nil {
		t.Fatalf("CloseWrite: %v", err)
	}
	if err := e.CloseRead(); err != nil {
		t.Fatalf("CloseRead: %v", err)
	}
	if raw.closeWriteCalled != 1 || raw.closeReadCalled != 1 {
		t.Fatalf("half-close calls: write=%d read=%d", raw.closeWriteCalled, raw.closeReadCalled)
	}

	// Read side still drains after the write side is half-closed.
	raw.enqueueString("250 OK\r\n")
	line, err := e.ReadLine()
	if err != nil || line != "250 OK" {
		t.Fatalf("ReadLine after CloseWrite = %q, %v", line, err)
	}

	// Upgraded stream supports half-close independently of raw.
	raw.enqueueString("STARTTLS\r\n")
	if _, err := e.ReadLine(); err != nil {
		t.Fatalf("ReadLine: %v", err)
	}
	post := newScriptedConn()
	if err := e.BeginUpgrade(context.Background(), func(context.Context, DuplexConn) (DuplexConn, error) {
		return newFakeTLSConn(raw, post), nil
	}); err != nil {
		t.Fatalf("BeginUpgrade: %v", err)
	}
	if err := e.CloseWrite(); err != nil {
		t.Fatalf("tls CloseWrite: %v", err)
	}
	if post.closeWriteCalled != 1 {
		t.Fatalf("post CloseWrite calls = %d", post.closeWriteCalled)
	}

	// A connection without half-close support reports ErrUnsupported.
	e2, err := NewEngine(Config{Conn: &noHalfCloseConn{}})
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	t.Cleanup(func() { _ = e2.Close() })
	if err := e2.CloseWrite(); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("CloseWrite err = %v, want ErrUnsupported", err)
	}
}

func TestPhaseGuardsAndScrub(t *testing.T) {
	raw := newScriptedConn()
	raw.enqueueString("STARTTLS\r\n")
	e := newTestEngine(t, raw)

	if e.Phase() != Plain {
		t.Fatalf("initial phase = %s", e.Phase())
	}
	if _, err := e.ReadLine(); err != nil {
		t.Fatalf("ReadLine: %v", err)
	}

	// Plant a recognizable plaintext payload in the parser, then force the
	// rejection path and prove the backing bytes were zeroed, not just
	// logically dropped.
	secret := []byte("SECRET-PLAINTEXT")
	e.reader.buf = append(e.reader.buf, secret...)
	backing := e.reader.buf[:cap(e.reader.buf)]
	if err := e.BeginUpgrade(context.Background(), func(context.Context, DuplexConn) (DuplexConn, error) {
		t.Fatal("pipelined data must block the upgrade")
		return nil, nil
	}); !errors.Is(err, ErrPipelined) {
		t.Fatalf("BeginUpgrade err = %v", err)
	}
	for _, b := range backing[:len(secret)] {
		if b != 0 {
			t.Fatalf("plaintext buffer not scrubbed: %q", backing[:len(secret)])
		}
	}

	// Operations on a closed engine fail with ErrClosed.
	if err := e.WriteLine("250 OK"); !errors.Is(err, ErrClosed) {
		t.Fatalf("WriteLine err = %v, want ErrClosed", err)
	}
	if _, err := e.ReadLine(); !errors.Is(err, ErrClosed) {
		t.Fatalf("ReadLine err = %v, want ErrClosed", err)
	}
	if err := e.BeginUpgrade(context.Background(), func(context.Context, DuplexConn) (DuplexConn, error) {
		return nil, nil
	}); !errors.Is(err, ErrClosed) {
		t.Fatalf("re-upgrade err = %v, want ErrClosed", err)
	}
	if err := e.Close(); !errors.Is(err, ErrClosed) {
		t.Fatalf("second Close err = %v, want ErrClosed", err)
	}
}

func TestWrongPhaseDuringHandshake(t *testing.T) {
	raw := newScriptedConn()
	raw.enqueueString("STARTTLS\r\n")
	e := newTestEngine(t, raw)
	if _, err := e.ReadLine(); err != nil {
		t.Fatalf("ReadLine: %v", err)
	}

	release := make(chan struct{})
	started := make(chan struct{})
	upErr := make(chan error, 1)
	go func() {
		upErr <- e.BeginUpgrade(context.Background(), func(context.Context, DuplexConn) (DuplexConn, error) {
			close(started)
			<-release
			return newFakeTLSConn(raw, newScriptedConn()), nil
		})
	}()
	<-started
	if e.Phase() != Upgrading {
		t.Fatalf("phase during handshake = %s, want upgrading", e.Phase())
	}
	if _, err := e.ReadLine(); !errors.Is(err, ErrWrongPhase) {
		t.Fatalf("ReadLine during handshake err = %v, want ErrWrongPhase", err)
	}
	if err := e.WriteLine("noop"); !errors.Is(err, ErrWrongPhase) {
		t.Fatalf("WriteLine during handshake err = %v, want ErrWrongPhase", err)
	}
	close(release)
	if err := <-upErr; err != nil {
		t.Fatalf("BeginUpgrade: %v", err)
	}
	if e.Phase() != TLS {
		t.Fatalf("phase after handshake = %s, want tls", e.Phase())
	}
}

func TestMaxLineEnforced(t *testing.T) {
	raw := newScriptedConn()
	raw.enqueueString(strings.Repeat("x", 100) + "\r\n")
	e, err := NewEngine(Config{Conn: raw, MaxLine: 16})
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	t.Cleanup(func() { _ = e.Close() })
	if _, err := e.ReadLine(); !errors.Is(err, ErrLineTooLong) {
		t.Fatalf("ReadLine err = %v, want ErrLineTooLong", err)
	}
}

func TestSMTPHelpers(t *testing.T) {
	if !IsSTARTTLS("starttls") || !IsSTARTTLS("STARTTLS") || IsSTARTTLS("STARTTLS X") {
		t.Fatal("IsSTARTTLS mismatch")
	}
	if ResponseCode("220 Go") != 220 || ResponseCode("554-Bad") != 554 || ResponseCode("junk") != 0 {
		t.Fatal("ResponseCode mismatch")
	}
	if !IsPositiveCompletion(250) || IsPositiveCompletion(450) {
		t.Fatal("IsPositiveCompletion mismatch")
	}
	if _, err := NewEngine(Config{}); !errors.Is(err, ErrNoConn) {
		t.Fatalf("nil conn err = %v, want ErrNoConn", err)
	}
}
