package cdcprobe

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

func heartbeatWithAge(age time.Duration) Heartbeat {
	return Heartbeat{ID: 1, SourceTime: time.Now().Add(-age)}
}

func TestMonitorMeasuresEndToEndLag(t *testing.T) {
	m := NewMonitor(1000*time.Millisecond, nil, 100)
	s := m.Observe(heartbeatWithAge(120 * time.Millisecond))
	if s.Lag < 120*time.Millisecond {
		t.Fatalf("lag %v smaller than injected age 120ms", s.Lag)
	}
	if len(m.AlertLog()) != 0 {
		t.Fatalf("no alert expected below threshold")
	}
}

func TestMonitorAlertsAndThrottlesOnBreach(t *testing.T) {
	limiter := NewRateLimiter(100, 1)
	m := NewMonitor(1000*time.Millisecond, limiter, 100)

	// Lag of 1500ms breaches the 1000ms threshold.
	m.Observe(heartbeatWithAge(1500 * time.Millisecond))

	select {
	case a := <-m.Alerts():
		if a.Lag < 1500*time.Millisecond {
			t.Fatalf("alert lag %v too small", a.Lag)
		}
		if a.Threshold != 1000*time.Millisecond {
			t.Fatalf("wrong threshold in alert: %v", a.Threshold)
		}
	case <-time.After(time.Second):
		t.Fatalf("expected immediate alert on lag breach")
	}
	if got := limiter.Rate(); got != 50 {
		t.Fatalf("expected dynamic throttling to 50 hb/s, got %v", got)
	}
	if len(m.AlertLog()) != 1 {
		t.Fatalf("expected 1 alert in log, got %d", len(m.AlertLog()))
	}
}

func TestMonitorRestoresRateAfterRecovery(t *testing.T) {
	limiter := NewRateLimiter(100, 1)
	m := NewMonitor(1000*time.Millisecond, limiter, 100)

	m.Observe(heartbeatWithAge(2 * time.Second)) // breach -> throttle
	if !limiter.Throttled() {
		t.Fatalf("expected throttled limiter")
	}
	// recoverFor consecutive OK samples restore the rate.
	for i := 0; i < m.recoverFor; i++ {
		m.Observe(heartbeatWithAge(10 * time.Millisecond))
	}
	if limiter.Throttled() {
		t.Fatalf("expected limiter restored after recovery streak")
	}
	if got := limiter.Rate(); got != 100 {
		t.Fatalf("expected restored rate 100, got %v", got)
	}
}

func TestMonitorSlidingWindow(t *testing.T) {
	m := NewMonitor(1000*time.Millisecond, nil, 5)
	for i := 0; i < 20; i++ {
		m.Observe(heartbeatWithAge(time.Millisecond))
	}
	if got := len(m.Samples()); got != 5 {
		t.Fatalf("expected window to retain 5 samples, got %d", got)
	}
}

func TestRenderChart(t *testing.T) {
	m := NewMonitor(1000*time.Millisecond, nil, 100)
	for i := 0; i < 10; i++ {
		m.Observe(heartbeatWithAge(100 * time.Millisecond))
	}
	m.Observe(heartbeatWithAge(1500 * time.Millisecond)) // breach marker

	var buf bytes.Buffer
	m.RenderChart(&buf, 40, 8)
	out := buf.String()
	if !strings.Contains(out, "End-to-End Replication Lag") {
		t.Fatalf("chart missing title:\n%s", out)
	}
	if !strings.Contains(out, "!") {
		t.Fatalf("chart missing breach marker '!':\n%s", out)
	}
	if !strings.Contains(out, "-") {
		t.Fatalf("chart missing threshold line:\n%s", out)
	}
}
