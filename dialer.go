package sshchannel

import (
	"context"
	"net"
	"sync"
	"time"
)

// Dialer establishes the upstream connection for a channel. Implementations
// must honor the context and return once the dial is settled. The host passes
// the Target pinned at authorization time, so the dialer can never be steered
// to a different destination by a later policy change.
type Dialer interface {
	DialContext(ctx context.Context, target Target) (net.Conn, error)
}

// NetDialer adapts the standard library's net.Dialer.
type NetDialer struct {
	Dialer net.Dialer
}

func (n NetDialer) DialContext(ctx context.Context, target Target) (net.Conn, error) {
	return n.Dialer.DialContext(ctx, "tcp", net.JoinHostPort(target.Host(), itoaPort(target.Port())))
}

// DialCall is one in-flight paused dial. Tests keep the call suspended until
// they choose to release it, which makes asynchronous-dial races
// deterministic: revoke a grant, close a channel, or reuse an ID while the
// dial is still pending, then observe what the host does with the result.
type DialCall struct {
	Target Target

	once    sync.Once
	ready   chan struct{}
	release chan struct{}
	ctx     context.Context

	// ignoreCancel makes the suspended dial wait for Release even if its
	// context is cancelled. This models a dial that has already succeeded
	// downstream but whose callback lands after the channel was torn down.
	ignoreCancel bool

	resultOnce sync.Once
	conn       net.Conn
	err        error

	// ReleaseNanos records when Release was called, for ordering assertions.
	ReleaseNanos int64
}

func newDialCall(ctx context.Context, target Target) *DialCall {
	return &DialCall{
		Target:  target,
		ready:   make(chan struct{}),
		release: make(chan struct{}),
		ctx:     ctx,
	}
}

// Ready is closed when the host has invoked the dialer.
func (c *DialCall) Ready() <-chan struct{} { return c.ready }

// Context reports the dial context the host supplied, so tests can assert
// cancellation when a channel is torn down mid-dial.
func (c *DialCall) Context() context.Context { return c.ctx }

// Resolve pre-sets the outcome. It may be called before Release; a zero conn
// with a non-nil err models a dial failure.
func (c *DialCall) Resolve(conn net.Conn, err error) {
	c.resultOnce.Do(func() {
		c.conn, c.err = conn, err
	})
}

// Release lets the suspended dial return its pre-set outcome.
func (c *DialCall) Release() {
	c.once.Do(func() {
		c.ReleaseNanos = time.Now().UnixNano()
		close(c.release)
	})
}

func (c *DialCall) wait(ctx context.Context) (net.Conn, error) {
	close(c.ready)
	if c.ignoreCancel {
		<-c.release
		return c.conn, c.err
	}
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-c.release:
		return c.conn, c.err
	}
}

// PausableDialer is a test Dialer whose dials block until explicitly released.
// Every dial is also recorded in order on Calls.
type PausableDialer struct {
	mu    sync.Mutex
	calls []*DialCall
	// ignoreCancel, when true, makes newly created calls ignore context
	// cancellation and only return on Release.
	ignoreCancel bool
}

// SetIgnoreDetachCancellation toggles the late-callback mode for future
// dials, simulating a successful dial whose callback arrives after teardown.
func (d *PausableDialer) SetIgnoreDetachCancellation(ignore bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.ignoreCancel = ignore
}

// Calls returns a snapshot of all dial calls in invocation order.
func (d *PausableDialer) Calls() []*DialCall {
	d.mu.Lock()
	defer d.mu.Unlock()
	out := make([]*DialCall, len(d.calls))
	copy(out, d.calls)
	return out
}

// LastCall returns the most recent dial call, or nil.
func (d *PausableDialer) LastCall() *DialCall {
	d.mu.Lock()
	defer d.mu.Unlock()
	if len(d.calls) == 0 {
		return nil
	}
	return d.calls[len(d.calls)-1]
}

// DialContext implements Dialer.
func (d *PausableDialer) DialContext(ctx context.Context, target Target) (net.Conn, error) {
	call := newDialCall(ctx, target)
	d.mu.Lock()
	call.ignoreCancel = d.ignoreCancel
	d.calls = append(d.calls, call)
	d.mu.Unlock()
	return call.wait(ctx)
}

// halfCloseConn is implemented by real TCP connections and by pipes that
// support write-side shutdown. The relay uses it to propagate half-close.
type halfCloseConn interface {
	CloseWrite() error
}

// closeWrite best-effort shuts down the writing side of conn, falling back to
// a full close when half-close is unsupported.
func closeWrite(conn net.Conn) error {
	if hc, ok := conn.(halfCloseConn); ok {
		return hc.CloseWrite()
	}
	return conn.Close()
}
