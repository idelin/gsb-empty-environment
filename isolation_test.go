package netsec

import (
	"context"
	"errors"
	"testing"
	"time"
)

// Old stream-number callbacks must never reach a new connection that reuses
// the same ConnID, and a reused stream number on the same connection must get
// a fresh generation that stale tickets cannot touch.
func TestStaleTicketCannotAffectNewConnection(t *testing.T) {
	c := New(Config{MaxInFlight: 4, MaxActivePerConn: 1, MaxQueuedPerConn: 4})

	// --- Old connection: one draining, one queued. ---
	c.OpenConn(1)
	oldDraining, err := c.Admit(context.Background(), 1, 1)
	if err != nil {
		t.Fatal(err)
	}
	queuedErr := make(chan error, 1)
	go func() {
		_, e := c.Admit(context.Background(), 1, 3)
		queuedErr <- e
	}()
	waitFor(t, func() bool { return c.Stats().Queued == 1 }, "old stream queued")

	// Close the old connection while work drains and a waiter exists.
	c.CloseConn(1)
	select {
	case err := <-queuedErr:
		if !errors.Is(err, ErrConnectionClosed) {
			t.Fatalf("queued want closed, got %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("queued stream not unblocked by close")
	}

	// The old connection's in-flight work drains and returns its token before
	// the identifier is recycled.
	if err := c.MarkReset(oldDraining); err != nil {
		t.Fatalf("old reset: %v", err)
	}
	if err := c.WorkFinished(oldDraining); err != nil {
		t.Fatalf("old work finish: %v", err)
	}

	// A brand-new connection reuses ConnID 1 and even the same stream number.
	c.OpenConn(1)
	fresh, err := c.Admit(context.Background(), 1, 1)
	if err != nil {
		t.Fatalf("fresh admit: %v", err)
	}
	if fresh == oldDraining {
		t.Fatal("fresh ticket must differ from old ticket")
	}

	// Every late callback carrying the old ticket is stale and changes nothing.
	for _, call := range []func() error{
		func() error { return c.MarkBackendStarting(oldDraining) },
		func() error { return c.WorkFinished(oldDraining) },
	} {
		if err := call(); !errors.Is(err, ErrStaleTicket) {
			t.Fatalf("old callback want stale, got %v", err)
		}
	}
	if err := c.MarkReset(oldDraining); !errors.Is(err, ErrStaleTicket) {
		t.Fatalf("old reset want stale, got %v", err)
	}
	if err := c.StreamEnd(oldDraining); !errors.Is(err, ErrStaleTicket) {
		t.Fatalf("old stream end want stale, got %v", err)
	}

	// The fresh stream is unaffected and walks the normal lifecycle.
	if st, _ := c.State(fresh); st != StateActive {
		t.Fatalf("fresh state = %s, want active", st)
	}
	if err := c.Abandon(fresh); err != nil {
		t.Fatalf("abandon fresh: %v", err)
	}

	// Now the old draining work finally completes; its callbacks are stale and
	// cannot double-release the fresh generation's capacity.
	if err := c.MarkReset(oldDraining); !errors.Is(err, ErrStaleTicket) {
		t.Fatalf("old reset want stale, got %v", err)
	}
	if err := c.WorkFinished(oldDraining); !errors.Is(err, ErrStaleTicket) {
		t.Fatalf("old finish want stale, got %v", err)
	}
	if got := c.Stats().InFlight; got != 0 {
		t.Fatalf("inflight = %d, want 0", got)
	}
}

// A single attack connection must not starve a well-behaved low-frequency
// connection: the scheduler skips per-connection-blocked queue heads and keeps
// granting eligible streams behind them.
func TestAttackConnectionCannotStarveNormalTraffic(t *testing.T) {
	const (
		maxInFlight = 6
		perConn     = 2
		normalTotal = 12
	)
	c := New(Config{
		MaxInFlight:      maxInFlight,
		MaxActivePerConn: perConn,
		MaxQueuedPerConn: normalTotal + 4,
	})
	c.OpenConn(1) // attacker
	c.OpenConn(2) // normal client

	// Saturate the attacker's per-connection quota and keep the tokens held
	// with long-running "backend work".
	attackTickets := make([]Ticket, 0, perConn)
	for i := 0; i < perConn; i++ {
		tk, err := c.Admit(context.Background(), 1, StreamID(2*i+1))
		if err != nil {
			t.Fatal(err)
		}
		attackTickets = append(attackTickets, tk)
	}

	// The attacker keeps trying to open more streams; they all queue.
	attackBlocked := make(chan struct{})
	go func() {
		for i := 0; i < normalTotal; i++ {
			tk, err := c.Admit(context.Background(), 1, StreamID(100+i))
			if err != nil {
				return
			}
			_ = c.MarkReset(tk)
			time.Sleep(30 * time.Millisecond)
			_ = c.WorkFinished(tk)
		}
		close(attackBlocked)
	}()
	waitFor(t, func() bool { return c.Stats().Queued >= 1 }, "attacker queueing")

	// Normal low-frequency requests must all complete promptly even though the
	// attacker's blocked queue head sits ahead of them.
	done := make(chan StreamID, normalTotal)
	start := time.Now()
	for i := 0; i < normalTotal; i++ {
		go func(n int) {
			tk, err := c.Admit(context.Background(), 2, StreamID(2*n+1))
			if err != nil {
				t.Errorf("normal admit: %v", err)
				return
			}
			_ = c.StreamEnd(tk)
			_ = c.WorkFinished(tk)
			done <- tk.Stream()
		}(i)
	}

	completed := 0
	deadline := time.After(3 * time.Second)
	for completed < normalTotal {
		select {
		case <-done:
			completed++
		case <-deadline:
			t.Fatalf("only %d/%d normal streams completed; attacker starved them", completed, normalTotal)
		}
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("normal traffic took %v, indicates starvation", elapsed)
	}

	// Release the attacker's long-held tokens and let its queue drain.
	for _, tk := range attackTickets {
		if err := c.Abandon(tk); err != nil {
			t.Fatal(err)
		}
	}
	select {
	case <-attackBlocked:
	case <-time.After(3 * time.Second):
		t.Fatal("attacker backlog never drained")
	}
	c.WaitDrained()
	if got := c.Stats().InFlight; got != 0 {
		t.Fatalf("inflight = %d, want 0", got)
	}
}
