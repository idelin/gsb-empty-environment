package compaction

import (
	"context"
	"testing"
	"time"
)

func TestWriteStallThreeStateTransitions(t *testing.T) {
	cfg := testConfig()
	cfg.Stall.L0SoftDelayFiles = 3
	cfg.Stall.L0HardStopFiles = 5
	cfg.Stall.PendingSoftDelayBytes = 100
	cfg.Stall.PendingHardStopBytes = 200
	e := NewEngine(cfg)
	ctl := e.WriteController()

	var transitions []StallState
	ctl.OnTransition = func(from, to StallState) {
		transitions = append(transitions, to)
	}

	if ctl.State() != StateNormal {
		t.Fatalf("initial state = %s, want Normal", ctl.State())
	}

	// Two files: still normal (below soft watermark 3).
	e.Flush([]Entry{putEntry("a", 1, "v")}, nil)
	e.Flush([]Entry{putEntry("b", 2, "v")}, nil)
	if ctl.State() != StateNormal {
		t.Fatalf("state at 2 L0 files = %s, want Normal", ctl.State())
	}

	// Third file crosses the soft watermark -> Delayed.
	e.Flush([]Entry{putEntry("c", 3, "v")}, nil)
	if ctl.State() != StateDelayed {
		t.Fatalf("state at 3 L0 files = %s, want Delayed", ctl.State())
	}

	// A delayed write returns but pays the delay step.
	start := time.Now()
	if err := ctl.WaitWritable(context.Background()); err != nil {
		t.Fatalf("WaitWritable delayed: %v", err)
	}
	if time.Since(start) < cfg.Stall.DelayStep {
		t.Fatal("delayed write did not apply the delay step")
	}

	// Two more files reach the hard watermark -> Stopped.
	e.Flush([]Entry{putEntry("d", 4, "v")}, nil)
	e.Flush([]Entry{putEntry("e", 5, "v")}, nil)
	if ctl.State() != StateStopped {
		t.Fatalf("state at 5 L0 files = %s, want Stopped", ctl.State())
	}

	// A stopped write blocks until pressure is released asynchronously.
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- ctl.WaitWritable(ctx) }()

	select {
	case <-done:
		t.Fatal("WaitWritable returned while still Stopped")
	case <-time.After(20 * time.Millisecond):
	}

	// Simulate compaction draining L0 back under the soft watermark.
	ctl.Evaluate(StallMetrics{L0FileCount: 1, PendingCompactionBytes: 0})
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("WaitWritable after release: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("writer stayed blocked after hard-stop release")
	}
	if ctl.State() != StateNormal {
		t.Fatalf("state after drain = %s, want Normal", ctl.State())
	}

	// Verify the observed transition sequence contains all three states.
	seen := map[StallState]bool{}
	for _, s := range transitions {
		seen[s] = true
	}
	if !seen[StateDelayed] || !seen[StateStopped] || !seen[StateNormal] {
		t.Fatalf("did not observe all three states, transitions=%v", transitions)
	}
}

// TestWriteStallPendingBytes drives transitions purely from pending
// compaction bytes (the second pressure input).
func TestWriteStallPendingBytes(t *testing.T) {
	ctl := NewWriteController(StallThresholds{
		L0HardStopFiles:       0, // disabled
		L0SoftDelayFiles:      0,
		PendingSoftDelayBytes: 100,
		PendingHardStopBytes:  200,
		DelayStep:             time.Millisecond,
	})
	if s := ctl.Evaluate(StallMetrics{PendingCompactionBytes: 50}); s != StateNormal {
		t.Fatalf("50 bytes -> %s, want Normal", s)
	}
	if s := ctl.Evaluate(StallMetrics{PendingCompactionBytes: 100}); s != StateDelayed {
		t.Fatalf("100 bytes -> %s, want Delayed", s)
	}
	if s := ctl.Evaluate(StallMetrics{PendingCompactionBytes: 200}); s != StateStopped {
		t.Fatalf("200 bytes -> %s, want Stopped", s)
	}
	if s := ctl.Evaluate(StallMetrics{PendingCompactionBytes: 99}); s != StateNormal {
		t.Fatalf("99 bytes -> %s, want Normal (exact release)", s)
	}
}

// TestWriteStallHardPrecedence ensures a hard stop wins over a soft signal.
func TestWriteStallHardPrecedence(t *testing.T) {
	ctl := NewWriteController(StallThresholds{
		L0SoftDelayFiles:      2,
		L0HardStopFiles:       4,
		PendingSoftDelayBytes: 10,
		PendingHardStopBytes:  10,
		DelayStep:             time.Millisecond,
	})
	s := ctl.Evaluate(StallMetrics{L0FileCount: 4, PendingCompactionBytes: 50})
	if s != StateStopped {
		t.Fatalf("hard+soft -> %s, want Stopped", s)
	}
}

// TestWriteStallContextCancel verifies a blocked writer unblocks on cancel.
func TestWriteStallContextCancel(t *testing.T) {
	ctl := NewWriteController(DefaultStallThresholds())
	ctl.Evaluate(StallMetrics{L0FileCount: 999})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- ctl.WaitWritable(ctx) }()
	time.Sleep(20 * time.Millisecond)
	cancel()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("expected context cancellation error")
		}
	case <-time.After(time.Second):
		t.Fatal("cancelled writer never unblocked")
	}
}
