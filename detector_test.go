package phiaccrual

import (
	"math/rand"
	"testing"
	"time"
)

// testConfig mirrors a node heartbeating every 200ms over a jittery link.
// MinStdDeviation of 100ms follows the Akka/Cassandra convention of flooring
// the variance estimate so a cold or very steady window does not make the
// detector hypersensitive.
func testConfig() Config {
	return Config{
		WindowSize:               200,
		Threshold:                8.0,
		MinStdDeviation:          100 * time.Millisecond,
		AcceptableHeartbeatPause: 0,
		FirstHeartbeatMean:       200 * time.Millisecond,
		FirstHeartbeatStdDev:     100 * time.Millisecond,
	}
}

// feedHeartbeats drives the detector through a jittery-but-healthy phase:
// base interval 200ms with +/-40ms uniform jitter, plus a 450ms spike every
// 10th heartbeat to emulate short network delay bursts. It returns the
// heartbeat timestamps and the maximum phi observed in flight (evaluated
// just before each subsequent report landed).
func feedHeartbeats(d *Detector, start time.Time, count int, rng *rand.Rand) ([]time.Time, float64) {
	ts := []time.Time{start}
	d.Report(start)
	maxPhi := 0.0
	for i := 1; i < count; i++ {
		interval := 200*time.Millisecond + time.Duration(rng.Intn(81)-40)*time.Millisecond
		if i%10 == 0 {
			interval = 450 * time.Millisecond // short jitter burst
		}
		next := ts[i-1].Add(interval)
		// Worst-case instant of this interval: right before the heartbeat lands.
		if phi, err := d.Phi(next.Add(-time.Millisecond)); err == nil && phi > maxPhi {
			maxPhi = phi
		}
		ts = append(ts, next)
		d.Report(next)
	}
	return ts, maxPhi
}

func TestPhiStaysLowDuringNormalJitter(t *testing.T) {
	d := NewDetector(testConfig())
	rng := rand.New(rand.NewSource(42))
	start := time.Unix(0, 0)
	ts, maxPhi := feedHeartbeats(d, start, 300, rng)

	if maxPhi >= d.Threshold() {
		t.Fatalf("false alarm: phi peaked at %.2f during normal jitter (threshold %.0f)", maxPhi, d.Threshold())
	}
	if maxPhi > 4.0 {
		t.Fatalf("phi %.2f suspiciously high for healthy jitter, want well below threshold", maxPhi)
	}
	// One nominal interval after the last heartbeat, suspicion is minimal.
	phi, err := d.Phi(ts[len(ts)-1].Add(201 * time.Millisecond))
	if err != nil {
		t.Fatal(err)
	}
	if phi > 1.0 {
		t.Fatalf("phi %.2f too high one nominal interval after a heartbeat", phi)
	}
	if !d.Available(ts[len(ts)-1].Add(201 * time.Millisecond)) {
		t.Fatal("node wrongly unavailable during healthy jitter")
	}
}

func TestFixedTimeoutWouldFlapWherePhiDoesNot(t *testing.T) {
	d := NewDetector(testConfig())
	rng := rand.New(rand.NewSource(7))
	start := time.Unix(0, 0)

	const fixedTimeout = 300 * time.Millisecond // a plausible "traditional" timeout
	fixedDeaths := 0

	ts := []time.Time{start}
	d.Report(start)
	for i := 1; i < 300; i++ {
		interval := 200*time.Millisecond + time.Duration(rng.Intn(81)-40)*time.Millisecond
		if i%10 == 0 {
			interval = 450 * time.Millisecond
		}
		next := ts[i-1].Add(interval)
		if interval > fixedTimeout {
			fixedDeaths++ // fixed timeout declares the node dead here
		}
		ts = append(ts, next)
		d.Report(next)
	}

	if fixedDeaths == 0 {
		t.Fatal("scenario broken: expected the fixed timeout to flap on jitter bursts")
	}
	// The phi detector never once crosses its threshold during the same phase.
	for i, tm := range ts {
		if i == 0 {
			continue
		}
		phi, err := d.Phi(tm.Add(-time.Millisecond))
		if err != nil {
			continue
		}
		if phi >= d.Threshold() {
			t.Fatalf("phi detector false-positive at heartbeat %d (phi=%.2f)", i, phi)
		}
	}
	t.Logf("fixed timeout declared death %d times; phi detector declared it 0 times", fixedDeaths)
}

func TestRealPartitionReclaimsLeaseDecisively(t *testing.T) {
	d := NewDetector(testConfig())
	rng := rand.New(rand.NewSource(99))
	start := time.Unix(0, 0)
	ts, _ := feedHeartbeats(d, start, 300, rng)
	last := ts[len(ts)-1]

	var reclaimed Lease
	var reclaimPhi float64
	m := NewMonitor(d, Lease{Holder: "node-7"}, 50*time.Millisecond, func(l Lease, phi float64) {
		reclaimed = l
		reclaimPhi = phi
	})

	// Network partition: heartbeats stop at `last`. Step the clock forward
	// and find the moment the lease is reclaimed.
	var reclaimAt time.Duration
	step := 10 * time.Millisecond
	for elapsed := step; elapsed <= 5*time.Second; elapsed += step {
		if m.Check(last.Add(elapsed)) {
			reclaimAt = elapsed
			break
		}
	}
	if reclaimAt == 0 {
		t.Fatal("lease was never reclaimed during a real partition")
	}
	// The detector must wait long enough to absorb the 450ms jitter spikes
	// yet act decisively, well under a conservative 5s fixed timeout.
	if reclaimAt < 450*time.Millisecond {
		t.Fatalf("reclaimed too eagerly at %v; would have flapped on jitter", reclaimAt)
	}
	if reclaimAt > 2*time.Second {
		t.Fatalf("reclaimed too slowly at %v; not decisive", reclaimAt)
	}
	if reclaimed.Holder != "node-7" {
		t.Fatalf("wrong lease reclaimed: %+v", reclaimed)
	}
	if reclaimPhi < d.Threshold() {
		t.Fatalf("reclaim phi %.2f below threshold %.0f", reclaimPhi, d.Threshold())
	}
	t.Logf("lease reclaimed after %v of silence (phi=%.2f)", reclaimAt, reclaimPhi)

	// Reclamation happens exactly once.
	if m.Check(last.Add(10 * time.Second)) {
		t.Fatal("lease reclaimed twice")
	}
	if !m.Reclaimed() {
		t.Fatal("monitor does not report reclaimed state")
	}
}

func TestPhiGrowsMonotonicallyWithSilence(t *testing.T) {
	d := NewDetector(testConfig())
	rng := rand.New(rand.NewSource(3))
	start := time.Unix(0, 0)
	ts, _ := feedHeartbeats(d, start, 100, rng)
	last := ts[len(ts)-1]

	prev := -1.0
	for elapsed := 100 * time.Millisecond; elapsed <= 2*time.Second; elapsed += 100 * time.Millisecond {
		phi, err := d.Phi(last.Add(elapsed))
		if err != nil {
			t.Fatal(err)
		}
		if phi < prev {
			t.Fatalf("phi not monotonic in silence: %.2f then %.2f", prev, phi)
		}
		prev = phi
	}
	if prev < d.Threshold() {
		t.Fatalf("phi %.2f never crossed threshold after 2s of silence", prev)
	}
}

func TestNoHeartbeatYet(t *testing.T) {
	d := NewDetector(testConfig())
	if _, err := d.Phi(time.Now()); err != ErrNoHeartbeat {
		t.Fatalf("want ErrNoHeartbeat, got %v", err)
	}
	if !d.Available(time.Now()) {
		t.Fatal("node should be optimistically available before first heartbeat")
	}
}

func TestDistributionAdaptsToSlowerNetwork(t *testing.T) {
	d := NewDetector(testConfig())
	start := time.Unix(0, 0)
	// A slower but steady link: 1s intervals, tiny jitter.
	cur := start
	d.Report(cur)
	for i := 0; i < 150; i++ {
		cur = cur.Add(time.Second + time.Duration(i%5-2)*time.Millisecond)
		d.Report(cur)
	}
	// Slightly late (1.1x interval) must look perfectly normal.
	phi, err := d.Phi(cur.Add(1100 * time.Millisecond))
	if err != nil {
		t.Fatal(err)
	}
	if phi > 1.5 {
		t.Fatalf("phi %.2f too high for 1.1x interval on a steady 1s link", phi)
	}
	// Even 1.5x the interval must not convict the node.
	phi, err = d.Phi(cur.Add(1500 * time.Millisecond))
	if err != nil {
		t.Fatal(err)
	}
	if phi >= d.Threshold() {
		t.Fatalf("false conviction on a steady link: phi %.2f at 1.5x interval", phi)
	}
}

func TestMonitorRunLoopWithRealClock(t *testing.T) {
	cfg := testConfig()
	cfg.Threshold = 3.0                         // trip quickly for a fast test
	cfg.MinStdDeviation = 50 * time.Millisecond // absorb scheduler slop
	d := NewDetector(cfg)
	now := time.Now()
	// Train on a fast, steady stream so the detector expects ~20ms intervals.
	for i := 0; i < 60; i++ {
		d.Report(now.Add(time.Duration(i) * 20 * time.Millisecond))
	}

	reclaimed := make(chan Lease, 1)
	m := NewMonitor(d, Lease{Holder: "node-live"}, 5*time.Millisecond, func(l Lease, phi float64) {
		reclaimed <- l
	})
	stop := make(chan struct{})
	defer close(stop)
	go m.Run(stop)

	// Keep heartbeating in real time; the lease must survive.
	deadline := time.Now().Add(300 * time.Millisecond)
	for time.Now().Before(deadline) {
		d.Report(time.Now())
		time.Sleep(20 * time.Millisecond)
	}
	if m.Reclaimed() {
		t.Fatal("lease reclaimed while heartbeats were flowing")
	}

	// Stop heartbeats: the lease must be reclaimed promptly.
	select {
	case l := <-reclaimed:
		if l.Holder != "node-live" {
			t.Fatalf("unexpected lease holder %q", l.Holder)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("lease not reclaimed after heartbeats stopped")
	}
}
