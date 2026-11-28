package lsm

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestPrescreenWriteStallThreeStateTransitions(t *testing.T) {
	c := NewWriteStallController(WriteStallConfig{
		L0SlowdownTrigger:              4,
		L0StopTrigger:                  8,
		PendingCompactionSlowdownBytes: 100,
		PendingCompactionStopBytes:     200,
		SoftDelay:                      2 * time.Millisecond,
	})
	cases := []struct {
		l0      int
		pending uint64
		want    WriteStallState
	}{
		{0, 0, StallNormal},
		{3, 99, StallNormal},
		{4, 0, StallDelayed},
		{0, 100, StallDelayed},
		{7, 199, StallDelayed},
		{8, 0, StallStopped},
		{0, 200, StallStopped},
		{5, 150, StallDelayed},
		{0, 0, StallNormal},
	}
	for i, tc := range cases {
		if got := c.Update(tc.l0, tc.pending); got != tc.want {
			t.Fatalf("case %d: state = %s, want %s", i, got, tc.want)
		}
	}
	if got := len(c.History(0)); got != 4 {
		t.Fatalf("history length = %d, want 4 transitions", got)
	}
	c.Close()
	if c.Update(0, 0) != StallNormal {
		t.Fatal("state changed after Close")
	}
}

func TestPrescreenWriteStallDelayedAndStoppedWait(t *testing.T) {
	c := NewWriteStallController(WriteStallConfig{
		L0SoftLimit:       2,
		L0HardLimit:       4,
		PendingSoftLimit:  1 << 20,
		PendingHardLimit:  2 << 20,
		DelayedWriteDelay: 30 * time.Millisecond,
	})
	c.Update(2, 0)
	start := time.Now()
	if err := c.WaitForWrite(context.Background()); err != nil {
		t.Fatalf("delayed wait: %v", err)
	}
	if elapsed := time.Since(start); elapsed < 20*time.Millisecond {
		t.Fatalf("delayed wait returned after %v, want >= 20ms", elapsed)
	}

	c.Update(4, 0)
	done := make(chan error, 1)
	go func() { done <- c.WaitForWrite(context.Background()) }()
	select {
	case err := <-done:
		t.Fatalf("stopped wait returned early: %v", err)
	case <-time.After(20 * time.Millisecond):
	}
	c.Update(0, 0)
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("wait after recovery: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("wait did not wake after hard pressure cleared")
	}
}

func TestPrescreenWriteStallContextCancellationAndClose(t *testing.T) {
	c := NewWriteStallController(WriteStallConfig{
		L0SoftLimit: 1, L0HardLimit: 1,
		PendingSoftLimit: 1, PendingHardLimit: 1,
	})
	c.Update(10, 0)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if err := c.WaitForWrite(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("wait error = %v, want deadline exceeded", err)
	}
	done := make(chan error, 1)
	go func() { done <- c.WaitForWrite(context.Background()) }()
	time.Sleep(5 * time.Millisecond)
	c.Close()
	if err := <-done; !errors.Is(err, ErrWriteStallClosed) {
		t.Fatalf("wait after Close = %v, want ErrWriteStallClosed", err)
	}
}

func TestPrescreenWriteStallUnconfiguredDimensionIsDisabled(t *testing.T) {
	l0Only := NewWriteStallController(WriteStallConfig{L0SoftLimit: 2, L0HardLimit: 4})
	if got := l0Only.Update(0, ^uint64(0)); got != StallNormal {
		t.Fatalf("pending-only pressure changed L0-only config to %s", got)
	}
	if got := l0Only.Update(4, ^uint64(0)); got != StallStopped {
		t.Fatalf("L0 hard limit did not stop writes: %s", got)
	}

	pendingOnly := NewWriteStallController(WriteStallConfig{PendingSoftLimit: 10, PendingHardLimit: 20})
	if got := pendingOnly.Update(1<<30, 0); got != StallNormal {
		t.Fatalf("L0 pressure changed pending-only config to %s", got)
	}
	if got := pendingOnly.Update(1<<30, 20); got != StallStopped {
		t.Fatalf("pending hard limit did not stop writes: %s", got)
	}
}
