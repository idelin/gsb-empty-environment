package cdcprobe

import (
	"testing"
	"time"
)

func TestHeartbeatLagNanosecondPrecision(t *testing.T) {
	src := time.Now()
	hb := Heartbeat{ID: 1, SourceTime: src}
	lag := hb.Lag(src.Add(1500 * time.Nanosecond))
	if lag != 1500*time.Nanosecond {
		t.Fatalf("expected 1500ns lag, got %v", lag)
	}
}

func TestSourceInjectsHeartbeats(t *testing.T) {
	out := make(chan Heartbeat, 16)
	s := NewSource(10*time.Millisecond, nil, out)
	go s.Run()
	time.Sleep(105 * time.Millisecond)
	s.Stop()

	got := s.Injected()
	if got < 5 {
		t.Fatalf("expected continuous injection, got only %d heartbeats", got)
	}
	// IDs must be strictly increasing and timestamps non-decreasing.
	var prev Heartbeat
	for i := int64(0); i < got; i++ {
		hb := <-out
		if i > 0 {
			if hb.ID <= prev.ID {
				t.Fatalf("heartbeat IDs not increasing: %d then %d", prev.ID, hb.ID)
			}
			if hb.SourceTime.Before(prev.SourceTime) {
				t.Fatalf("heartbeat timestamps went backwards")
			}
		}
		prev = hb
	}
}

func TestSourceRespectsLimiter(t *testing.T) {
	out := make(chan Heartbeat, 64)
	// Limiter at 5 events/s, source interval 1ms: limiter must dominate.
	limiter := NewRateLimiter(5, 1)
	s := NewSource(1*time.Millisecond, limiter, out)
	go s.Run()
	time.Sleep(1 * time.Second)
	s.Stop()

	got := s.Injected()
	if got > 12 { // 5/s + burst headroom
		t.Fatalf("limiter not enforced: %d heartbeats in 1s", got)
	}
	if got == 0 {
		t.Fatalf("expected some heartbeats to pass the limiter")
	}
}
