package retry

import (
	"testing"
	"time"
)

func TestDeadlineEnoughFor(t *testing.T) {
	start := time.Unix(0, 0)
	dl := NewDeadline(start, 100*time.Millisecond)
	// remaining 100 > minProc 10 + wait 20 = 30 -> allowed.
	if !dl.EnoughFor(start, 10*time.Millisecond, 20*time.Millisecond) {
		t.Fatal("plenty of deadline should pass")
	}
	// remaining 100, minProc 80 + wait 20 = 100: strict inequality rejects.
	if dl.EnoughFor(start, 80*time.Millisecond, 20*time.Millisecond) {
		t.Fatal("equality must not pass (strict greater-than required)")
	}
	// remaining 100 < minProc 90 + wait 20 = 110 -> rejected.
	if dl.EnoughFor(start, 90*time.Millisecond, 20*time.Millisecond) {
		t.Fatal("insufficient deadline must be rejected")
	}
	if !dl.Expired(start.Add(101 * time.Millisecond)) {
		t.Fatal("deadline should be expired past its end")
	}
}

func TestDeadlinePropagation(t *testing.T) {
	start := time.Unix(0, 0)
	parent := NewDeadline(start, time.Second)
	// Child receives a tighter remaining budget at hop time.
	hopAt := start.Add(400 * time.Millisecond)
	child := parent.Set(hopAt, 300*time.Millisecond)
	if got := child.Remaining(hopAt); got != 300*time.Millisecond {
		t.Fatalf("child remaining = %v, want 300ms", got)
	}
}

func TestBackoffDeterministicJitter(t *testing.T) {
	cfg := BackoffConfig{
		BaseDelay: 10 * time.Millisecond, Multiplier: 2,
		MaxDelay: time.Second, Jitter: 0.2, Seed: 99,
	}
	d1 := cfg.Delay(7, 2)
	d2 := cfg.Delay(7, 2)
	if d1 != d2 {
		t.Fatalf("deterministic jitter mismatch: %v vs %v", d1, d2)
	}
	other := cfg.Delay(8, 2)
	if other == d1 {
		t.Fatal("different call IDs should usually get different jitter")
	}
	// Exponential growth with jitter stays within +/-20% of 40ms base.
	base := 40 * time.Millisecond
	if d1 < time.Duration(float64(base)*0.8) || d1 > time.Duration(float64(base)*1.2) {
		t.Fatalf("jittered delay %v outside [0.8,1.2]*%v", d1, base)
	}
	// Max delay clamps the series.
	cfg.MaxDelay = 50 * time.Millisecond
	if got := cfg.Delay(1, 10); got > time.Duration(float64(50*time.Millisecond)*1.2) {
		t.Fatalf("delay %v not clamped near max", got)
	}
}
