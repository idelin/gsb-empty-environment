package capnet

import (
	"io"
	"net"
	"sync"
	"time"
)

type endpoint struct {
	id      uint64
	gen     uint64
	address string
	token   [tokenLen]byte
	script  Script
	clock   *fakeClock

	mu   sync.Mutex
	conn *simConn
}

func (ep *endpoint) connWrites() [][]byte {
	ep.mu.Lock()
	defer ep.mu.Unlock()
	if ep.conn == nil {
		return nil
	}
	ep.conn.mu.Lock()
	defer ep.conn.mu.Unlock()
	out := make([][]byte, len(ep.conn.writes))
	copy(out, ep.conn.writes)
	return out
}

// simConn is a fully in-process, deterministic net.Conn. Read and Write never
// touch the network poller; deadlines are measured on the trial's fake clock.
type simConn struct {
	ep *endpoint

	mu            sync.Mutex
	readIdx       int
	writeIdx      int
	writes        [][]byte
	closed        bool
	readDeadline  time.Time
	writeDeadline time.Time
	local         simAddr
	remote        simAddr
}

var (
	_ net.Conn  = (*simConn)(nil)
	_ net.Addr  = simAddr{}
	_ net.Error = (*simError)(nil)
)

func newSimConn(ep *endpoint) *simConn {
	return &simConn{
		ep:     ep,
		local:  simAddr{label: "sim://plug-in"},
		remote: simAddr{label: "sim://endpoint"},
	}
}

func (c *simConn) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return 0, net.ErrClosed
	}
	if !c.readDeadline.IsZero() && !c.ep.clock.nowTime().Before(c.readDeadline) {
		return 0, &simError{op: "read", msg: ErrDeadlineExceeded.Error(), timeout: true, wrapped: ErrDeadlineExceeded}
	}
	if c.readIdx >= len(c.ep.script.Inbox) {
		if c.ep.script.CloseAfterScript {
			return 0, io.EOF
		}
		return 0, ErrScriptExhausted
	}
	chunk := c.ep.script.Inbox[c.readIdx]
	n := copy(p, chunk)
	if n == len(chunk) {
		c.readIdx++
	} else {
		c.ep.script.Inbox[c.readIdx] = chunk[n:]
	}
	return n, nil
}

func (c *simConn) Write(p []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return 0, net.ErrClosed
	}
	now := c.ep.clock.nowTime()
	if !c.writeDeadline.IsZero() && !now.Before(c.writeDeadline) {
		return 0, &simError{op: "write", msg: ErrDeadlineExceeded.Error(), timeout: true, wrapped: ErrDeadlineExceeded}
	}
	if c.writeIdx < len(c.ep.script.ExpectWrites) {
		want := c.ep.script.ExpectWrites[c.writeIdx]
		if string(want) != string(p) {
			return 0, ErrUnexpectedWrite
		}
		c.writeIdx++
	}
	payload := append([]byte(nil), p...)
	c.writes = append(c.writes, payload)
	if reply, ok := c.ep.script.AutoReply[string(p)]; ok {
		c.ep.script.Inbox = append(c.ep.script.Inbox, append([]byte(nil), reply...))
	}
	return len(p), nil
}

func (c *simConn) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.closed = true
	return nil
}

func (c *simConn) LocalAddr() net.Addr  { return c.local }
func (c *simConn) RemoteAddr() net.Addr { return c.remote }

func (c *simConn) SetDeadline(t time.Time) error {
	return c.setDeadline(t, true, true)
}

func (c *simConn) SetReadDeadline(t time.Time) error {
	return c.setDeadline(t, true, false)
}

func (c *simConn) SetWriteDeadline(t time.Time) error {
	return c.setDeadline(t, false, true)
}

func (c *simConn) setDeadline(t time.Time, read, write bool) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return net.ErrClosed
	}
	if read {
		c.readDeadline = t
	}
	if write {
		c.writeDeadline = t
	}
	return nil
}

type simError struct {
	op      string
	msg     string
	timeout bool
	wrapped error
}

func (e *simError) Error() string   { return e.op + ": " + e.msg }
func (e *simError) Timeout() bool   { return e.timeout }
func (e *simError) Temporary() bool { return e.timeout }
func (e *simError) Unwrap() error   { return e.wrapped }
