package netsec

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type testConn struct {
	addr   string
	closes atomic.Int32
}

func (c *testConn) Close() error {
	c.closes.Add(1)
	return nil
}

// pauseDialer blocks every dial until the test releases it, then returns the
// configured result regardless of ctx — simulating success callbacks that
// arrive after their round was canceled.
type pauseDialer struct {
	mu      sync.Mutex
	started map[string]chan struct{}
	release map[string]chan struct{}
	conns   map[string]*testConn
	errs    map[string]error
	dials   map[string]int
}

func newPauseDialer() *pauseDialer {
	return &pauseDialer{
		started: make(map[string]chan struct{}),
		release: make(map[string]chan struct{}),
		conns:   make(map[string]*testConn),
		errs:    make(map[string]error),
		dials:   make(map[string]int),
	}
}

func (d *pauseDialer) gates(addr string) (started, release chan struct{}) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if _, ok := d.started[addr]; !ok {
		d.started[addr] = make(chan struct{})
		d.release[addr] = make(chan struct{})
	}
	return d.started[addr], d.release[addr]
}

func (d *pauseDialer) Dial(ctx context.Context, addr string) (Conn, error) {
	started, release := d.gates(addr)
	d.mu.Lock()
	d.dials[addr]++
	conn := d.conns[addr]
	err := d.errs[addr]
	d.mu.Unlock()
	close(started)
	<-release
	if err != nil {
		return nil, err
	}
	return conn, nil
}

func (d *pauseDialer) waitStarted(t *testing.T, addr string) {
	t.Helper()
	started, _ := d.gates(addr)
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatalf("dial for %s did not start", addr)
	}
}

func (d *pauseDialer) releaseAddr(addr string) {
	_, release := d.gates(addr)
	close(release)
}

func (d *pauseDialer) dialCount(addr string) int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.dials[addr]
}

func allowAll(string, string) bool { return true }

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("condition not met: %s", what)
}

type outcome struct {
	conn Conn
	res  []AddressResult
	err  error
}

func connectAsync(r *Racer, ctx context.Context, cfg Round) <-chan outcome {
	ch := make(chan outcome, 1)
	go func() {
		c, res, err := r.Connect(ctx, cfg)
		ch <- outcome{c, res, err}
	}()
	return ch
}

func TestDoubleSuccessSingleWinner(t *testing.T) {
	d := newPauseDialer()
	d.conns["a"] = &testConn{addr: "a"}
	d.conns["b"] = &testConn{addr: "b"}
	r := NewRacer(d, PolicyFunc(allowAll))

	done := connectAsync(r, context.Background(), Round{
		Addrs:         []string{"a", "b"},
		PolicyVersion: "v1",
		Deadline:      time.Now().Add(5 * time.Second),
	})
	d.waitStarted(t, "a")
	d.waitStarted(t, "b")

	d.releaseAddr("a")
	o := <-done
	if o.err != nil {
		t.Fatalf("Connect: %v", o.err)
	}
	if o.conn != d.conns["a"] {
		t.Fatalf("winner = %v, want conn a", o.conn)
	}

	// Late success from the same round must be closed, exactly once.
	d.releaseAddr("b")
	waitFor(t, "loser closed once", func() bool { return d.conns["b"].closes.Load() == 1 })
	if got := d.conns["a"].closes.Load(); got != 0 {
		t.Fatalf("winner closed %d times by racer", got)
	}

	o.conn.Close()
	if got := d.conns["a"].closes.Load(); got != 1 {
		t.Fatalf("winner closed %d times, want 1", got)
	}
}

func TestRevocationCancelsRound(t *testing.T) {
	d := newPauseDialer()
	d.conns["a"] = &testConn{addr: "a"}
	d.conns["b"] = &testConn{addr: "b"}
	r := NewRacer(d, PolicyFunc(allowAll))

	done := connectAsync(r, context.Background(), Round{
		Addrs:         []string{"a", "b"},
		PolicyVersion: "v1",
		Deadline:      time.Now().Add(5 * time.Second),
	})
	d.waitStarted(t, "a")
	d.waitStarted(t, "b")

	r.Revoke("v1")
	o := <-done
	if !errors.Is(o.err, ErrPolicyRevoked) {
		t.Fatalf("err = %v, want ErrPolicyRevoked", o.err)
	}
	if o.conn != nil {
		t.Fatalf("canceled round delivered a connection")
	}

	// Success callbacks from the canceled round arrive late: both closed once.
	d.releaseAddr("a")
	d.releaseAddr("b")
	waitFor(t, "late conns closed", func() bool {
		return d.conns["a"].closes.Load() == 1 && d.conns["b"].closes.Load() == 1
	})

	// New rounds on the revoked version fail fast without dialing.
	_, res, err := r.Connect(context.Background(), Round{
		Addrs:         []string{"c"},
		PolicyVersion: "v1",
		Deadline:      time.Now().Add(time.Second),
	})
	if !errors.Is(err, ErrPolicyRevoked) {
		t.Fatalf("err = %v, want ErrPolicyRevoked", err)
	}
	if !errors.Is(res[0].Err, ErrPolicyRevoked) || res[0].Dialed {
		t.Fatalf("unexpected per-address result: %+v", res[0])
	}
	if got := d.dialCount("c"); got != 0 {
		t.Fatalf("dialed %d times on revoked version", got)
	}
}

func TestAllFailedReturnsPerAddressResults(t *testing.T) {
	d := newPauseDialer()
	boom := errors.New("boom")
	d.errs["a"] = boom
	r := NewRacer(d, PolicyFunc(func(_, addr string) bool { return addr != "b" }))

	done := connectAsync(r, context.Background(), Round{
		Addrs:         []string{"a", "b"},
		PolicyVersion: "v1",
		Deadline:      time.Now().Add(5 * time.Second),
	})
	d.waitStarted(t, "a")
	d.releaseAddr("a")

	o := <-done
	if !errors.Is(o.err, ErrAllFailed) {
		t.Fatalf("err = %v, want ErrAllFailed", o.err)
	}
	if o.conn != nil {
		t.Fatalf("failed round delivered a connection")
	}
	if len(o.res) != 2 {
		t.Fatalf("got %d results, want 2", len(o.res))
	}
	if !o.res[0].Dialed || !errors.Is(o.res[0].Err, boom) {
		t.Fatalf("result a = %+v", o.res[0])
	}
	if o.res[1].Dialed || !errors.Is(o.res[1].Err, ErrPolicyRejected) {
		t.Fatalf("result b = %+v", o.res[1])
	}
	if got := d.dialCount("b"); got != 0 {
		t.Fatalf("policy-rejected address dialed %d times", got)
	}
}

func TestCancelWinSingleOrdering(t *testing.T) {
	for i := 0; i < 50; i++ {
		d := newPauseDialer()
		d.conns["a"] = &testConn{addr: "a"}
		d.conns["b"] = &testConn{addr: "b"}
		r := NewRacer(d, PolicyFunc(allowAll))

		ctx, cancel := context.WithCancel(context.Background())
		done := connectAsync(r, ctx, Round{
			Addrs:         []string{"a", "b"},
			PolicyVersion: "v1",
		})
		d.waitStarted(t, "a")
		d.waitStarted(t, "b")

		go cancel()
		go d.releaseAddr("a")
		go d.releaseAddr("b")

		o := <-done
		if o.err == nil {
			// Win ordered before cancel: winner live, loser closed exactly once.
			var loser *testConn
			switch o.conn {
			case d.conns["a"]:
				loser = d.conns["b"]
			case d.conns["b"]:
				loser = d.conns["a"]
			default:
				t.Fatalf("unknown winner %v", o.conn)
			}
			waitFor(t, "loser closed once", func() bool { return loser.closes.Load() == 1 })
			winner := o.conn.(*testConn)
			if got := winner.closes.Load(); got != 0 {
				t.Fatalf("winner closed %d times by racer", got)
			}
		} else {
			// Cancel ordered before win: no delivery, both closed exactly once.
			if !errors.Is(o.err, context.Canceled) {
				t.Fatalf("err = %v, want context.Canceled", o.err)
			}
			if o.conn != nil {
				t.Fatalf("canceled round delivered a connection")
			}
			waitFor(t, "both closed once", func() bool {
				return d.conns["a"].closes.Load() == 1 && d.conns["b"].closes.Load() == 1
			})
		}
		cancel()
	}
}

func TestDeadlineCancelsRound(t *testing.T) {
	d := newPauseDialer()
	d.conns["a"] = &testConn{addr: "a"}
	r := NewRacer(d, PolicyFunc(allowAll))

	done := connectAsync(r, context.Background(), Round{
		Addrs:         []string{"a"},
		PolicyVersion: "v1",
		Deadline:      time.Now().Add(20 * time.Millisecond),
	})
	d.waitStarted(t, "a")

	o := <-done
	if !errors.Is(o.err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want context.DeadlineExceeded", o.err)
	}

	d.releaseAddr("a")
	waitFor(t, "late conn closed once", func() bool { return d.conns["a"].closes.Load() == 1 })
}
