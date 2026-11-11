package starttls

import (
	"bytes"
	"errors"
	"io"
	"sync"
)

// scriptedConn is a controllable, chunked in-memory DuplexConn.
//
// Reads return exactly the queued chunks one at a time (a chunk of n bytes
// always yields n bytes), which lets tests reproduce the "STARTTLS and the
// next command arrive in the same read" condition deterministically. When no
// chunk is queued Read blocks until one is enqueued or the connection is
// closed. Writes are captured intact for assertions.
type scriptedConn struct {
	mu      sync.Mutex
	cond    *sync.Cond
	chunks  [][]byte
	closed  bool
	writeMu sync.Mutex
	written bytes.Buffer

	closeWriteErr error
	closeReadErr  error
	writeErr      error

	closeWriteCalled int
	closeReadCalled  int
	closeCalled      int
}

func newScriptedConn() *scriptedConn {
	c := &scriptedConn{}
	c.cond = sync.NewCond(&c.mu)
	return c
}

// enqueue appends a chunk that Read will return whole.
func (c *scriptedConn) enqueue(chunk []byte) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.chunks = append(c.chunks, append([]byte(nil), chunk...))
	c.cond.Broadcast()
}

// enqueueString is the string form of enqueue.
func (c *scriptedConn) enqueueString(s string) {
	c.enqueue([]byte(s))
}

// enqueueEOF schedules a zero-byte read that reports io.EOF, modelling the
// peer half-closing its write side (FIN) while the connection stays open.
func (c *scriptedConn) enqueueEOF() {
	c.enqueue(nil)
}

// Read implements DuplexConn.
func (c *scriptedConn) Read(p []byte) (int, error) {
	c.mu.Lock()
	for len(c.chunks) == 0 && !c.closed {
		c.cond.Wait()
	}
	if len(c.chunks) > 0 {
		chunk := c.chunks[0]
		c.chunks = c.chunks[1:]
		c.mu.Unlock()
		n := copy(p, chunk)
		if n == 0 {
			return 0, io.EOF
		}
		return n, nil
	}
	c.mu.Unlock()
	return 0, io.EOF
}

// Write implements DuplexConn and captures every byte.
func (c *scriptedConn) Write(p []byte) (int, error) {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	if c.writeErr != nil {
		return 0, c.writeErr
	}
	c.written.Write(append([]byte(nil), p...))
	return len(p), nil
}

// Close implements DuplexConn.
func (c *scriptedConn) Close() error {
	c.mu.Lock()
	if !c.closed {
		c.closed = true
		c.closeCalled++
	}
	c.cond.Broadcast()
	c.mu.Unlock()
	return nil
}

// CloseWrite implements CloseWriter.
func (c *scriptedConn) CloseWrite() error {
	c.mu.Lock()
	c.closeWriteCalled++
	err := c.closeWriteErr
	c.mu.Unlock()
	return err
}

// CloseRead implements CloseReader.
func (c *scriptedConn) CloseRead() error {
	c.mu.Lock()
	c.closeReadCalled++
	err := c.closeReadErr
	c.mu.Unlock()
	return err
}

// bytesWritten returns a copy of everything written so far.
func (c *scriptedConn) bytesWritten() []byte {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	return append([]byte(nil), c.written.Bytes()...)
}

// isClosed reports the closed flag.
func (c *scriptedConn) isClosed() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.closed
}

// tapConn wraps a DuplexConn and records every raw byte handed to Read and
// Write. Tests use it to prove the TLS layer never receives pre-upgrade
// plaintext: anything visible to the upgrade callback through tap is what
// the TLS stack would consume.
type tapConn struct {
	DuplexConn
	readMu   sync.Mutex
	readLog  bytes.Buffer
	writeMu  sync.Mutex
	writeLog bytes.Buffer
}

func (t *tapConn) Read(p []byte) (int, error) {
	n, err := t.DuplexConn.Read(p)
	if n > 0 {
		t.readMu.Lock()
		t.readLog.Write(p[:n])
		t.readMu.Unlock()
	}
	return n, err
}

func (t *tapConn) Write(p []byte) (int, error) {
	t.writeMu.Lock()
	t.writeLog.Write(append([]byte(nil), p...))
	t.writeMu.Unlock()
	return t.DuplexConn.Write(p)
}

func (t *tapConn) CloseWrite() error {
	if hw, ok := t.DuplexConn.(CloseWriter); ok {
		return hw.CloseWrite()
	}
	return ErrUnsupported
}

func (t *tapConn) CloseRead() error {
	if hr, ok := t.DuplexConn.(CloseReader); ok {
		return hr.CloseRead()
	}
	return ErrUnsupported
}

func (t *tapConn) readBytes() []byte {
	t.readMu.Lock()
	defer t.readMu.Unlock()
	return append([]byte(nil), t.readLog.Bytes()...)
}

// fakeTLSConn is the "upgraded" connection used in unit tests. It does not
// run crypto; it models the post-handshake stream and records the raw
// connection it was built from so tests can assert the upgrade callback got
// the original, unwrapped conn.
type fakeTLSConn struct {
	raw       DuplexConn
	post      *scriptedConn
	handshake func() error
	closed    bool
	mu        sync.Mutex
}

func newFakeTLSConn(raw DuplexConn, post *scriptedConn) *fakeTLSConn {
	return &fakeTLSConn{raw: raw, post: post}
}

func (f *fakeTLSConn) Read(p []byte) (int, error)  { return f.post.Read(p) }
func (f *fakeTLSConn) Write(p []byte) (int, error) { return f.post.Write(p) }

func (f *fakeTLSConn) Close() error {
	f.mu.Lock()
	f.closed = true
	f.mu.Unlock()
	return f.post.Close()
}

// CloseWrite keeps half-close semantics on the upgraded stream without
// touching the raw plaintext connection again.
func (f *fakeTLSConn) CloseWrite() error { return f.post.CloseWrite() }
func (f *fakeTLSConn) CloseRead() error  { return f.post.CloseRead() }

var errHandshake = errors.New("starttls test: simulated handshake failure")
