package cdcprobe

import (
	"testing"
	"time"
)

// TestEndToEndPipeline wires source -> congested link -> monitor and
// verifies the full acceptance path: continuous heartbeat injection,
// end-to-end lag measurement, alert on lag > 1000ms, and dynamic
// throttling of the source.
func TestEndToEndPipeline(t *testing.T) {
	stream := make(chan Heartbeat, 256)
	limiter := NewRateLimiter(100, 10)
	source := NewSource(5*time.Millisecond, limiter, stream)
	link := NewLink(stream, 10*time.Millisecond, 5*time.Millisecond)
	monitor := NewMonitor(1000*time.Millisecond, limiter, 500)

	go source.Run()
	go link.Run()
	go monitor.Run(link.Out())

	// Phase 1: healthy pipeline, lag well below threshold.
	time.Sleep(300 * time.Millisecond)
	if source.Injected() == 0 {
		t.Fatalf("source did not inject heartbeats")
	}

	// Phase 2: transient congestion pushes lag beyond 1000ms.
	link.SetCongestion(1200 * time.Millisecond)
	deadline := time.Now().Add(3 * time.Second)
	var alerted bool
	for time.Now().Before(deadline) && !alerted {
		select {
		case a := <-monitor.Alerts():
			if a.Lag <= 1000*time.Millisecond {
				t.Fatalf("alert fired below threshold: %v", a.Lag)
			}
			alerted = true
		case <-time.After(100 * time.Millisecond):
		}
	}
	if !alerted {
		t.Fatalf("no alert raised during congestion")
	}
	if !limiter.Throttled() {
		t.Fatalf("expected dynamic throttling after alert")
	}
	if got := limiter.Rate(); got >= 100 {
		t.Fatalf("expected reduced rate, got %v", got)
	}

	// Phase 3: congestion clears; lag recovers and rate is restored.
	link.SetCongestion(0)
	deadline = time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) && limiter.Throttled() {
		time.Sleep(50 * time.Millisecond)
	}
	if limiter.Throttled() {
		t.Fatalf("limiter not restored after congestion cleared")
	}

	source.Stop()
	close(stream) // no more heartbeats: let the link drain
	<-link.Done()
	<-monitor.Done()

	// Every sample's lag must cover at least the link's base+apply cost.
	samples := monitor.Samples()
	if len(samples) == 0 {
		t.Fatalf("monitor recorded no samples")
	}
	for _, s := range samples {
		if s.Lag < 15*time.Millisecond {
			t.Fatalf("lag %v below link base+apply latency", s.Lag)
		}
	}
	t.Logf("samples=%d alerts=%d maxLag=%v",
		len(samples), len(monitor.AlertLog()), maxLag(samples))
}

func maxLag(samples []Sample) time.Duration {
	var max time.Duration
	for _, s := range samples {
		if s.Lag > max {
			max = s.Lag
		}
	}
	return max
}
