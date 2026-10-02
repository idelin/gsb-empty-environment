package cdcprobe

import (
	"strings"
	"testing"
	"time"
)

func TestHeartbeatLag(t *testing.T) {
	h := Heartbeat{Seq: 1, SrcUnixNano: 1_000_000_000}
	if got := h.Lag(1_500_000_000); got != 500*time.Millisecond {
		t.Fatalf("lag = %v, want 500ms", got)
	}
}

func TestStoreAppendSubscribe(t *testing.T) {
	s := NewStore()
	ch := s.Subscribe()
	s.Append(Heartbeat{Seq: 1, SrcUnixNano: time.Now().UnixNano()})
	select {
	case h := <-ch:
		if h.Seq != 1 {
			t.Fatalf("seq = %d, want 1", h.Seq)
		}
	case <-time.After(time.Second):
		t.Fatal("subscriber did not receive heartbeat")
	}
	if s.Len() != 1 {
		t.Fatalf("len = %d, want 1", s.Len())
	}
}

func TestRateLimiter(t *testing.T) {
	r := NewRateLimiter(10) // burst capacity of 10
	for i := 0; i < 10; i++ {
		if !r.Allow() {
			t.Fatalf("burst token %d denied", i)
		}
	}
	if r.Allow() {
		t.Fatal("expected denial after burst exhausted")
	}
	time.Sleep(150 * time.Millisecond) // refills ~1.5 tokens at 10/s
	if !r.Allow() {
		t.Fatal("expected allow after refill")
	}
	r.SetRate(100)
	if r.Rate() != 100 {
		t.Fatalf("rate = %v, want 100", r.Rate())
	}
}

func TestInjectorInjectsHeartbeats(t *testing.T) {
	src := NewStore()
	inj := NewInjector(src, NewRateLimiter(1000), 5*time.Millisecond)
	inj.Start()
	time.Sleep(120 * time.Millisecond)
	inj.Stop()

	n := src.Len()
	if n < 5 {
		t.Fatalf("injected %d heartbeats, want >= 5", n)
	}
	ch := src.Subscribe()
	_ = ch
	prev := int64(-1)
	src.mu.Lock()
	rows := append([]Heartbeat(nil), src.rows...)
	src.mu.Unlock()
	for i, h := range rows {
		if h.Seq != uint64(i+1) {
			t.Fatalf("row %d seq = %d, want %d", i, h.Seq, i+1)
		}
		if h.SrcUnixNano <= prev {
			t.Fatalf("row %d timestamp not increasing", i)
		}
		prev = h.SrcUnixNano
	}
}

func TestReplicatorAppliesWithLatency(t *testing.T) {
	src, dst := NewStore(), NewStore()
	rep := NewReplicator(src, dst, func() time.Duration { return 20 * time.Millisecond })
	rep.Start()
	src.Append(Heartbeat{Seq: 1, SrcUnixNano: time.Now().UnixNano()})
	deadline := time.After(2 * time.Second)
	for dst.Len() == 0 {
		select {
		case <-deadline:
			t.Fatal("row not replicated in time")
		default:
			time.Sleep(5 * time.Millisecond)
		}
	}
	rep.Stop()
	if rep.Applied() != 1 {
		t.Fatalf("applied = %d, want 1", rep.Applied())
	}
}

func TestMonitorAlertThrottleAndRecover(t *testing.T) {
	limiter := NewRateLimiter(100)
	m := NewMonitor(NewStore(), limiter, 100*time.Millisecond, 100, 5)

	alerted := make(chan Alert, 1)
	recovered := make(chan struct{}, 1)
	m.OnAlert(func(a Alert) { alerted <- a })
	m.OnRecover(func() { recovered <- struct{}{} })

	// Lag below threshold: no alert, no throttle.
	m.Record(Sample{Seq: 1, At: time.Now(), Lag: 10 * time.Millisecond})
	if m.Throttled() {
		t.Fatal("throttled below threshold")
	}

	// Lag breaks the threshold: alert fires and limiter is throttled.
	m.Record(Sample{Seq: 2, At: time.Now(), Lag: 1500 * time.Millisecond})
	select {
	case a := <-alerted:
		if a.Lag != 1500*time.Millisecond {
			t.Fatalf("alert lag = %v, want 1.5s", a.Lag)
		}
	case <-time.After(time.Second):
		t.Fatal("no alert raised")
	}
	if !m.Throttled() {
		t.Fatal("expected throttled state")
	}
	if limiter.Rate() != 5 {
		t.Fatalf("limiter rate = %v, want 5", limiter.Rate())
	}

	// Further breaches while throttled must not re-alert.
	m.Record(Sample{Seq: 3, At: time.Now(), Lag: 2 * time.Second})
	if len(m.Alerts()) != 1 {
		t.Fatalf("alerts = %d, want 1", len(m.Alerts()))
	}

	// Lag recovers below half the threshold: limiter restored.
	m.Record(Sample{Seq: 4, At: time.Now(), Lag: 10 * time.Millisecond})
	select {
	case <-recovered:
	case <-time.After(time.Second):
		t.Fatal("no recovery callback")
	}
	if m.Throttled() {
		t.Fatal("still throttled after recovery")
	}
	if limiter.Rate() != 100 {
		t.Fatalf("limiter rate = %v, want 100", limiter.Rate())
	}
	if m.MaxLag() != 2*time.Second {
		t.Fatalf("max lag = %v, want 2s", m.MaxLag())
	}
}

// TestEndToEndCongestion runs the full pipeline with a transient
// congestion episode and verifies alert + dynamic throttling.
func TestEndToEndCongestion(t *testing.T) {
	src, dst := NewStore(), NewStore()
	limiter := NewRateLimiter(200)
	inj := NewInjector(src, limiter, 2*time.Millisecond)

	start := time.Now()
	latency := func() time.Duration {
		if el := time.Since(start); el > 100*time.Millisecond && el < 300*time.Millisecond {
			return 250 * time.Millisecond // congestion
		}
		return time.Millisecond
	}
	rep := NewReplicator(src, dst, latency)
	mon := NewMonitor(dst, limiter, 100*time.Millisecond, 200, 20)

	mon.Start()
	rep.Start()
	inj.Start()
	time.Sleep(900 * time.Millisecond)
	inj.Stop()
	rep.Stop()
	mon.Stop()

	if len(mon.Alerts()) == 0 {
		t.Fatal("expected at least one lag alert during congestion")
	}
	if mon.MaxLag() <= 100*time.Millisecond {
		t.Fatalf("max lag = %v, want > 100ms", mon.MaxLag())
	}
	if mon.Throttled() {
		t.Fatal("expected throttle released after congestion cleared")
	}
	if limiter.Rate() != 200 {
		t.Fatalf("limiter rate = %v, want restored 200", limiter.Rate())
	}
	if len(mon.Samples()) == 0 {
		t.Fatal("no lag samples recorded")
	}
}

func TestPlot(t *testing.T) {
	if got := Plot(nil, DefaultLagThreshold, 40, 8); got != "(no samples)" {
		t.Fatalf("empty plot = %q", got)
	}
	var samples []Sample
	base := time.Now()
	for i := 0; i < 100; i++ {
		lag := 10 * time.Millisecond
		if i >= 40 && i < 60 {
			lag = 1500 * time.Millisecond // congestion spike
		}
		samples = append(samples, Sample{Seq: uint64(i + 1), At: base, Lag: lag})
	}
	out := Plot(samples, DefaultLagThreshold, 60, 10)
	for _, want := range []string{"lag curve", "#", "-", "max=1.5s"} {
		if !strings.Contains(out, want) {
			t.Fatalf("plot output missing %q:\n%s", want, out)
		}
	}
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(lines) != 13 { // header + 10 rows + 2 axis lines
		t.Fatalf("plot has %d lines, want 13:\n%s", len(lines), out)
	}
}
