package netsec

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestServeHoldsEarlyDataUntilSuccessResponseIsFlushed(t *testing.T) {
	client := newFakeConn()
	target := newFakeConn()

	var events eventLog
	client.onWrite = func(p []byte) {
		if bytes.Contains(p, []byte("200 Connection Established")) {
			events.add("response-flushed")
		}
	}
	target.onWrite = func(p []byte) {
		events.add("target-wrote:" + string(p))
	}

	authStarted := make(chan struct{})
	releaseAuth := make(chan struct{})
	dialCalled := make(chan struct{}, 1)
	responseBuffered := make(chan struct{})
	releaseResponse := make(chan struct{})

	g := &Gateway{
		MaxEarlyData:         128,
		EarlyDataIdleTimeout: 10 * time.Millisecond,
		Authorize: func(ctx context.Context, req Request) (Decision, error) {
			close(authStarted)
			select {
			case <-releaseAuth:
				return Decision{Allowed: true}, nil
			case <-ctx.Done():
				return Decision{}, ctx.Err()
			}
		},
		Dial: func(ctx context.Context, req Request) (net.Conn, error) {
			dialCalled <- struct{}{}
			return target, nil
		},
		Respond: func(ctx context.Context, w io.Writer) error {
			if err := WriteConnectionEstablished(w); err != nil {
				return err
			}
			close(responseBuffered)
			select {
			case <-releaseResponse:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		},
	}

	client.push([]byte("EARLY"))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan error, 1)
	go func() {
		done <- g.Serve(ctx, client, Request{Target: "upstream.example:443", PolicyVersion: "v1"})
	}()

	<-authStarted
	if got := target.writtenString(); got != "" {
		t.Fatalf("target received %q before authorization completed", got)
	}
	select {
	case <-dialCalled:
		t.Fatal("dial happened before authorization completed")
	default:
	}

	close(releaseAuth)
	<-dialCalled
	<-responseBuffered

	if got := target.writtenString(); got != "" {
		t.Fatalf("target received %q before success response was flushed", got)
	}

	close(releaseResponse)
	waitFor(t, time.Second, func() bool {
		return target.writtenString() == "EARLY"
	}, "early data was not delivered after the success response")

	order := events.snapshot()
	if len(order) < 2 || order[0] != "response-flushed" || order[1] != "target-wrote:EARLY" {
		t.Fatalf("response/data ordering = %v, want response flush before target write", order)
	}

	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("Serve error = %v, want context.Canceled", err)
	}
}

func TestServeDeniedTargetReceivesNoBytes(t *testing.T) {
	client := newFakeConn()
	target := newFakeConn()

	authStarted := make(chan struct{})
	releaseAuth := make(chan struct{})
	var dialCalls atomic.Int32
	var respondCalls atomic.Int32

	g := &Gateway{
		MaxEarlyData:         64,
		EarlyDataIdleTimeout: 10 * time.Millisecond,
		Authorize: func(ctx context.Context, req Request) (Decision, error) {
			close(authStarted)
			select {
			case <-releaseAuth:
				return Decision{Allowed: false, Reason: "denied by policy"}, nil
			case <-ctx.Done():
				return Decision{}, ctx.Err()
			}
		},
		Dial: func(ctx context.Context, req Request) (net.Conn, error) {
			dialCalls.Add(1)
			return target, nil
		},
		Respond: func(ctx context.Context, w io.Writer) error {
			respondCalls.Add(1)
			return WriteConnectionEstablished(w)
		},
	}

	client.push([]byte("EARLY"))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan error, 1)
	go func() {
		done <- g.Serve(ctx, client, Request{Target: "blocked.example:443", PolicyVersion: "v1"})
	}()

	<-authStarted
	close(releaseAuth)

	if err := <-done; !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("Serve error = %v, want ErrUnauthorized", err)
	}
	if got := dialCalls.Load(); got != 0 {
		t.Fatalf("dial calls = %d, want 0", got)
	}
	if got := respondCalls.Load(); got != 0 {
		t.Fatalf("respond calls = %d, want 0", got)
	}
	if got := target.writtenString(); got != "" {
		t.Fatalf("unauthorized target received %q, want zero bytes", got)
	}
}

func TestServeCancellationDuringAuthorization(t *testing.T) {
	client := newFakeConn()
	target := newFakeConn()

	authStarted := make(chan struct{})
	var dialCalls atomic.Int32

	g := &Gateway{
		EarlyDataIdleTimeout: 10 * time.Millisecond,
		Authorize: func(ctx context.Context, req Request) (Decision, error) {
			close(authStarted)
			<-ctx.Done()
			return Decision{}, ctx.Err()
		},
		Dial: func(ctx context.Context, req Request) (net.Conn, error) {
			dialCalls.Add(1)
			return target, nil
		},
		Respond: func(ctx context.Context, w io.Writer) error {
			return WriteConnectionEstablished(w)
		},
	}

	client.push([]byte("EARLY"))
	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan error, 1)
	go func() {
		done <- g.Serve(ctx, client, Request{Target: "upstream.example:443", PolicyVersion: "v1"})
	}()

	<-authStarted
	cancel()

	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("Serve error = %v, want context.Canceled", err)
	}
	if got := dialCalls.Load(); got != 0 {
		t.Fatalf("dial calls = %d, want 0", got)
	}
	if got := target.writtenString(); got != "" {
		t.Fatalf("target received %q after cancellation, want zero bytes", got)
	}
}

func TestServeCancellationBeforeEarlyDataRelease(t *testing.T) {
	client := newFakeConn()
	target := newFakeConn()

	responseBuffered := make(chan struct{})
	ctx, cancel := context.WithCancel(context.Background())

	g := &Gateway{
		MaxEarlyData:         64,
		EarlyDataIdleTimeout: 10 * time.Millisecond,
		Authorize: func(ctx context.Context, req Request) (Decision, error) {
			return Decision{Allowed: true}, nil
		},
		Dial: func(ctx context.Context, req Request) (net.Conn, error) {
			return target, nil
		},
		Respond: func(ctx context.Context, w io.Writer) error {
			if err := WriteConnectionEstablished(w); err != nil {
				return err
			}
			close(responseBuffered)
			<-ctx.Done()
			return ctx.Err()
		},
	}

	client.push([]byte("EARLY"))
	done := make(chan error, 1)
	go func() {
		done <- g.Serve(ctx, client, Request{Target: "upstream.example:443", PolicyVersion: "v1"})
	}()

	<-responseBuffered
	cancel()

	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("Serve error = %v, want context.Canceled", err)
	}
	if got := target.writtenString(); got != "" {
		t.Fatalf("target received %q after cancellation, want zero bytes", got)
	}
}

func TestServeResponseFailureReceivesNoBytes(t *testing.T) {
	client := newFakeConn()
	target := newFakeConn()
	responseErr := errors.New("response write failed")

	g := &Gateway{
		MaxEarlyData:         64,
		EarlyDataIdleTimeout: 10 * time.Millisecond,
		Authorize: func(ctx context.Context, req Request) (Decision, error) {
			return Decision{Allowed: true}, nil
		},
		Dial: func(ctx context.Context, req Request) (net.Conn, error) {
			return target, nil
		},
		Respond: func(ctx context.Context, w io.Writer) error {
			if _, err := io.WriteString(w, "HTTP/1.1 200 Connection Established\r\n\r\n"); err != nil {
				return err
			}
			return responseErr
		},
	}

	client.push([]byte("EARLY"))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	err := g.Serve(ctx, client, Request{Target: "upstream.example:443", PolicyVersion: "v1"})
	if !errors.Is(err, ErrResponseWrite) {
		t.Fatalf("Serve error = %v, want ErrResponseWrite", err)
	}
	if !errors.Is(err, responseErr) {
		t.Fatalf("Serve error = %v, want wrapped response error", err)
	}
	if got := target.writtenString(); got != "" {
		t.Fatalf("target received %q after response failure, want zero bytes", got)
	}
	select {
	case <-target.closedCh:
	case <-time.After(time.Second):
		t.Fatal("target connection was not closed after response failure")
	}
}

func TestServeRejectsOversizedEarlyDataBeforeDial(t *testing.T) {
	client := newFakeConn()
	target := newFakeConn()
	var dialCalls atomic.Int32

	g := &Gateway{
		MaxEarlyData:         8,
		EarlyDataIdleTimeout: 10 * time.Millisecond,
		Authorize: func(ctx context.Context, req Request) (Decision, error) {
			return Decision{Allowed: true}, nil
		},
		Dial: func(ctx context.Context, req Request) (net.Conn, error) {
			dialCalls.Add(1)
			return target, nil
		},
		Respond: func(ctx context.Context, w io.Writer) error {
			return WriteConnectionEstablished(w)
		},
	}

	client.push([]byte("123456789"))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	err := g.Serve(ctx, client, Request{Target: "upstream.example:443", PolicyVersion: "v1"})
	if !errors.Is(err, ErrEarlyDataTooLarge) {
		t.Fatalf("Serve error = %v, want ErrEarlyDataTooLarge", err)
	}
	if got := dialCalls.Load(); got != 0 {
		t.Fatalf("dial calls = %d, want 0", got)
	}
	if got := target.writtenString(); got != "" {
		t.Fatalf("target received %q, want zero bytes", got)
	}
}

func TestServeAcceptsEarlyDataAtTheLimit(t *testing.T) {
	client := newFakeConn()
	target := newFakeConn()

	g := &Gateway{
		MaxEarlyData:         8,
		EarlyDataIdleTimeout: 10 * time.Millisecond,
		Authorize: func(ctx context.Context, req Request) (Decision, error) {
			return Decision{Allowed: true}, nil
		},
		Dial: func(ctx context.Context, req Request) (net.Conn, error) {
			return target, nil
		},
		Respond: func(ctx context.Context, w io.Writer) error {
			return WriteConnectionEstablished(w)
		},
	}

	client.push([]byte("12345678"))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan error, 1)
	go func() {
		done <- g.Serve(ctx, client, Request{Target: "upstream.example:443", PolicyVersion: "v1"})
	}()

	waitFor(t, time.Second, func() bool {
		return target.writtenString() == "12345678"
	}, "early data at the limit was not delivered")

	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("Serve error = %v, want context.Canceled", err)
	}
}

func TestServeReauthorizesWhenPolicyChanges(t *testing.T) {
	client := newFakeConn()
	target := newFakeConn()

	var policy atomic.Value
	policy.Store("v1")
	var authCalls atomic.Int32
	firstAuthStarted := make(chan struct{})
	releaseFirstAuth := make(chan struct{})
	var dialCalls atomic.Int32

	g := &Gateway{
		Policy: func() string {
			return policy.Load().(string)
		},
		EarlyDataIdleTimeout: 10 * time.Millisecond,
		Authorize: func(ctx context.Context, req Request) (Decision, error) {
			n := authCalls.Add(1)
			switch n {
			case 1:
				if req.PolicyVersion != "v1" {
					t.Errorf("first authorization policy = %q, want v1", req.PolicyVersion)
				}
				close(firstAuthStarted)
				<-releaseFirstAuth
				return Decision{Allowed: true}, nil
			default:
				if req.PolicyVersion != "v2" {
					t.Errorf("reauthorization policy = %q, want v2", req.PolicyVersion)
				}
				return Decision{Allowed: false, Reason: "v2 revoked access"}, nil
			}
		},
		Dial: func(ctx context.Context, req Request) (net.Conn, error) {
			dialCalls.Add(1)
			return target, nil
		},
		Respond: func(ctx context.Context, w io.Writer) error {
			return WriteConnectionEstablished(w)
		},
	}

	client.push([]byte("EARLY"))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan error, 1)
	go func() {
		done <- g.Serve(ctx, client, Request{Target: "upstream.example:443", PolicyVersion: "v1"})
	}()

	<-firstAuthStarted
	policy.Store("v2")
	close(releaseFirstAuth)

	if err := <-done; !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("Serve error = %v, want ErrUnauthorized", err)
	}
	if got := authCalls.Load(); got != 2 {
		t.Fatalf("authorization calls = %d, want 2 after policy change", got)
	}
	if got := dialCalls.Load(); got != 0 {
		t.Fatalf("dial calls = %d, want 0", got)
	}
	if got := target.writtenString(); got != "" {
		t.Fatalf("target received %q, want zero bytes", got)
	}
}

func TestServeReauthorizesPolicyChangeBeforeDataRelease(t *testing.T) {
	client := newFakeConn()
	target := newFakeConn()

	var policy atomic.Value
	policy.Store("v1")
	var authCalls atomic.Int32
	responseBuffered := make(chan struct{})
	releaseResponse := make(chan struct{})

	g := &Gateway{
		Policy: func() string {
			return policy.Load().(string)
		},
		MaxEarlyData:         64,
		EarlyDataIdleTimeout: 10 * time.Millisecond,
		Authorize: func(ctx context.Context, req Request) (Decision, error) {
			if authCalls.Add(1) == 1 {
				return Decision{Allowed: true}, nil
			}
			if req.PolicyVersion != "v2" {
				t.Errorf("reauthorization policy = %q, want v2", req.PolicyVersion)
			}
			return Decision{Allowed: false, Reason: "revoked while responding"}, nil
		},
		Dial: func(ctx context.Context, req Request) (net.Conn, error) {
			return target, nil
		},
		Respond: func(ctx context.Context, w io.Writer) error {
			if err := WriteConnectionEstablished(w); err != nil {
				return err
			}
			close(responseBuffered)
			select {
			case <-releaseResponse:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		},
	}

	client.push([]byte("EARLY"))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan error, 1)
	go func() {
		done <- g.Serve(ctx, client, Request{Target: "upstream.example:443", PolicyVersion: "v1"})
	}()

	<-responseBuffered
	policy.Store("v2")
	close(releaseResponse)

	if err := <-done; !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("Serve error = %v, want ErrUnauthorized", err)
	}
	if got := authCalls.Load(); got != 2 {
		t.Fatalf("authorization calls = %d, want 2", got)
	}
	if got := target.writtenString(); got != "" {
		t.Fatalf("target received %q after policy revocation, want zero bytes", got)
	}
}

func TestServeAuthorizesEachTargetIndependently(t *testing.T) {
	allowedTarget := "allowed.example:443"
	blockedTarget := "blocked.example:443"

	var authCalls atomic.Int32
	var dialCalls atomic.Int32

	newGateway := func(target *fakeConn) *Gateway {
		return &Gateway{
			EarlyDataIdleTimeout: 10 * time.Millisecond,
			Authorize: func(ctx context.Context, req Request) (Decision, error) {
				authCalls.Add(1)
				if req.Target == allowedTarget {
					return Decision{Allowed: true}, nil
				}
				return Decision{Allowed: false, Reason: "target not in allowlist"}, nil
			},
			Dial: func(ctx context.Context, req Request) (net.Conn, error) {
				dialCalls.Add(1)
				return target, nil
			},
			Respond: func(ctx context.Context, w io.Writer) error {
				return WriteConnectionEstablished(w)
			},
		}
	}

	firstTarget := newFakeConn()
	firstClient := newFakeConn()
	firstResponse := make(chan struct{})
	waitForWrite := func(p []byte) {
		if bytes.Contains(p, []byte("200 Connection Established")) {
			select {
			case <-firstResponse:
			default:
				close(firstResponse)
			}
		}
	}
	firstClient.onWrite = waitForWrite

	firstGateway := newGateway(firstTarget)
	firstCtx, cancelFirst := context.WithCancel(context.Background())
	firstDone := make(chan error, 1)
	go func() {
		firstDone <- firstGateway.Serve(firstCtx, firstClient, Request{Target: allowedTarget})
	}()

	select {
	case <-firstResponse:
	case <-time.After(time.Second):
		t.Fatal("first target was not established")
	}
	cancelFirst()
	if err := <-firstDone; !errors.Is(err, context.Canceled) {
		t.Fatalf("first Serve error = %v, want context.Canceled", err)
	}

	secondTarget := newFakeConn()
	secondClient := newFakeConn()
	secondGateway := newGateway(secondTarget)
	secondCtx, cancelSecond := context.WithCancel(context.Background())
	defer cancelSecond()

	if err := secondGateway.Serve(secondCtx, secondClient, Request{Target: blockedTarget}); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("second Serve error = %v, want ErrUnauthorized", err)
	}
	if got := authCalls.Load(); got != 2 {
		t.Fatalf("authorization calls = %d, want 2", got)
	}
	if got := dialCalls.Load(); got != 1 {
		t.Fatalf("dial calls = %d, want only the allowed target to be dialed", got)
	}
	if got := secondTarget.writtenString(); got != "" {
		t.Fatalf("blocked target received %q, want zero bytes", got)
	}
}

type fakeConn struct {
	mu              sync.Mutex
	pending         bytes.Buffer
	writes          bytes.Buffer
	readDeadline    time.Time
	writeDeadline   time.Time
	closed          bool
	closeOnce       sync.Once
	dataReady       chan struct{}
	deadlineChanged chan struct{}
	closedCh        chan struct{}
	onWrite         func([]byte)
}

func newFakeConn() *fakeConn {
	return &fakeConn{
		dataReady:       make(chan struct{}, 1),
		deadlineChanged: make(chan struct{}, 1),
		closedCh:        make(chan struct{}),
	}
}

func (c *fakeConn) push(p []byte) {
	c.mu.Lock()
	if !c.closed {
		c.pending.Write(append([]byte(nil), p...))
	}
	c.mu.Unlock()
	select {
	case c.dataReady <- struct{}{}:
	default:
	}
}

func (c *fakeConn) writtenString() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.writes.String()
}

func (c *fakeConn) Read(p []byte) (int, error) {
	for {
		c.mu.Lock()
		if c.closed {
			c.mu.Unlock()
			return 0, io.EOF
		}
		if c.pending.Len() > 0 {
			n, _ := c.pending.Read(p)
			c.mu.Unlock()
			return n, nil
		}
		deadline := c.readDeadline
		c.mu.Unlock()

		var timer *time.Timer
		var timerC <-chan time.Time
		if !deadline.IsZero() {
			wait := time.Until(deadline)
			if wait <= 0 {
				return 0, os.ErrDeadlineExceeded
			}
			timer = time.NewTimer(wait)
			timerC = timer.C
		}

		select {
		case <-c.dataReady:
		case <-c.deadlineChanged:
		case <-c.closedCh:
		case <-timerC:
			if timer != nil {
				timer.Stop()
			}
			return 0, os.ErrDeadlineExceeded
		}
		if timer != nil {
			timer.Stop()
		}
	}
}

func (c *fakeConn) Write(p []byte) (int, error) {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return 0, net.ErrClosed
	}
	_, _ = c.writes.Write(p)
	onWrite := c.onWrite
	c.mu.Unlock()

	if onWrite != nil {
		onWrite(append([]byte(nil), p...))
	}
	return len(p), nil
}

func (c *fakeConn) Close() error {
	c.closeOnce.Do(func() {
		c.mu.Lock()
		c.closed = true
		c.mu.Unlock()
		close(c.closedCh)
	})
	return nil
}

func (c *fakeConn) LocalAddr() net.Addr  { return testAddr("local") }
func (c *fakeConn) RemoteAddr() net.Addr { return testAddr("remote") }

func (c *fakeConn) SetDeadline(t time.Time) error {
	if err := c.SetReadDeadline(t); err != nil {
		return err
	}
	return c.SetWriteDeadline(t)
}

func (c *fakeConn) SetReadDeadline(t time.Time) error {
	c.mu.Lock()
	c.readDeadline = t
	c.mu.Unlock()
	select {
	case c.deadlineChanged <- struct{}{}:
	default:
	}
	return nil
}

func (c *fakeConn) SetWriteDeadline(t time.Time) error {
	c.mu.Lock()
	c.writeDeadline = t
	c.mu.Unlock()
	return nil
}

type testAddr string

func (a testAddr) Network() string { return "test" }
func (a testAddr) String() string  { return string(a) }

type eventLog struct {
	mu     sync.Mutex
	events []string
}

func (l *eventLog) add(event string) {
	l.mu.Lock()
	l.events = append(l.events, event)
	l.mu.Unlock()
}

func (l *eventLog) snapshot() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]string(nil), l.events...)
}

func waitFor(t *testing.T, timeout time.Duration, condition func() bool, message string) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal(message)
}
