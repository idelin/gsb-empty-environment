package phidetector

import (
	"context"
	"math"
	"math/rand"
	"testing"
	"time"
)

// netProfile describes a simulated link's heartbeat timing.
type netProfile struct {
	interval   time.Duration // base heartbeat interval
	jitter     time.Duration // uniform jitter in [-jitter, +jitter]
	spikeEvery int           // every Nth beat is delayed by spike (0 = never)
	spike      time.Duration // extra tail-latency delay
}

// train feeds n heartbeats following the profile, starting at start.
// It returns the time of the last heartbeat and the largest gap seen.
func train(d *Detector, start time.Time, n int, p netProfile, rng *rand.Rand) (last time.Time, maxGap time.Duration) {
	tm := start
	d.Report(tm)
	for i := 1; i <= n; i++ {
		delta := p.interval
		if p.jitter > 0 {
			delta += time.Duration(rng.Int63n(2*int64(p.jitter)) - int64(p.jitter))
		}
		if p.spikeEvery > 0 && i%p.spikeEvery == 0 {
			delta += p.spike
		}
		tm = tm.Add(delta)
		d.Report(tm)
		if delta > maxGap {
			maxGap = delta
		}
	}
	return tm, maxGap
}

// steadyLink models a healthy but imperfect network: 100ms heartbeats,
// ±25ms jitter, and a 150ms tail-latency spike roughly every 15 beats.
var steadyLink = netProfile{
	interval:   100 * time.Millisecond,
	jitter:     25 * time.Millisecond,
	spikeEvery: 15,
	spike:      150 * time.Millisecond,
}

func TestPhiRequiresHeartbeats(t *testing.T) {
	d := New()
	if _, err := d.Phi(time.Now()); err != ErrNoHeartbeat {
		t.Fatalf("expected ErrNoHeartbeat, got %v", err)
	}
}

func TestPhiZeroBeforeMinSamples(t *testing.T) {
	d := New(WithMinSamples(10))
	last, _ := train(d, time.Now(), 5, steadyLink, rand.New(rand.NewSource(1)))
	phi, err := d.Phi(last.Add(10 * time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if phi != 0 {
		t.Fatalf("phi should be 0 with too few samples, got %v", phi)
	}
	if d.IsSuspected(last.Add(10 * time.Second)) {
		t.Fatal("must not suspect before minSamples reached")
	}
}

func TestPhiMonotonicWithElapsed(t *testing.T) {
	d := New()
	rng := rand.New(rand.NewSource(42))
	last, _ := train(d, time.Now(), 100, steadyLink, rng)

	prev := -1.0
	for _, wait := range []time.Duration{50, 100, 150, 200, 300, 500, 800, 1200, 2000} {
		phi, err := d.Phi(last.Add(wait * time.Millisecond))
		if err != nil {
			t.Fatal(err)
		}
		if phi < prev {
			t.Fatalf("phi not monotonic: prev=%v cur=%v at wait=%vms", prev, phi, wait)
		}
		prev = phi
	}
}

// Acceptance boundary: occasional short jitter must NOT trip the
// detector, even though a naive fixed timeout would declare the node
// dead and trigger a violent re-election.
func TestJitterDoesNotCauseFalsePositive(t *testing.T) {
	d := New(WithPhiThreshold(8))
	rng := rand.New(rand.NewSource(7))
	last, maxGap := train(d, time.Now(), 300, steadyLink, rng)

	// During training the link naturally produced gaps up to ~275ms.
	// A fixed-timeout detector at 250ms would have fired repeatedly.
	fixedTimeout := 250 * time.Millisecond
	if maxGap <= fixedTimeout {
		t.Fatalf("test setup broken: training max gap %v should exceed fixed timeout %v", maxGap, fixedTimeout)
	}
	t.Logf("training produced natural gaps up to %v (fixed timeout %v would have misfired)", maxGap.Round(time.Millisecond), fixedTimeout)

	// A 250ms delay is well inside the observed distribution: the φ
	// detector must stay calm where the fixed timeout fires.
	phi, err := d.Phi(last.Add(250 * time.Millisecond))
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("phi after 250ms jitter spike: %.2f (threshold 8)", phi)
	if phi >= 8 {
		t.Fatalf("false positive: phi=%.2f crossed threshold on mere jitter", phi)
	}
	if d.IsSuspected(last.Add(250 * time.Millisecond)) {
		t.Fatal("detector wrongly reclaimed lease during jitter")
	}
}

// Acceptance boundary: a genuine partition must be detected decisively.
func TestRealFailureTriggersReclaim(t *testing.T) {
	d := New(WithPhiThreshold(8))
	rng := rand.New(rand.NewSource(7))
	last, _ := train(d, time.Now(), 300, steadyLink, rng)

	// Node is truly gone: after 1s of silence (~10x the mean interval),
	// suspicion must be overwhelming.
	phi, err := d.Phi(last.Add(1 * time.Second))
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("phi after 1s of silence: %.2f (threshold 8)", phi)
	if phi < 8 {
		t.Fatalf("failed to detect real partition: phi=%.2f below threshold", phi)
	}
	if !d.IsSuspected(last.Add(1 * time.Second)) {
		t.Fatal("lease should be reclaimed after real failure")
	}
}

// The detector adapts to each link's variance instead of applying one
// fixed timeout: the same 200ms delay is fatal on a calm link but
// perfectly normal on a noisy one.
func TestAdaptsToVariance(t *testing.T) {
	calm := New(WithPhiThreshold(8))
	noisy := New(WithPhiThreshold(8))
	rng := rand.New(rand.NewSource(99))
	now := time.Now()
	lastCalm, _ := train(calm, now, 200, netProfile{interval: 100 * time.Millisecond, jitter: 5 * time.Millisecond}, rng)
	lastNoisy, _ := train(noisy, now, 200, netProfile{interval: 100 * time.Millisecond, jitter: 45 * time.Millisecond}, rng)

	probe := 200 * time.Millisecond
	phiCalm, _ := calm.Phi(lastCalm.Add(probe))
	phiNoisy, _ := noisy.Phi(lastNoisy.Add(probe))
	t.Logf("phi at +200ms: calm link=%.2f, noisy link=%.2f", phiCalm, phiNoisy)
	if phiNoisy >= phiCalm {
		t.Fatalf("detector did not adapt: noisy link phi %.2f should be lower than calm link phi %.2f", phiNoisy, phiCalm)
	}
	if phiNoisy >= 8 {
		t.Fatalf("noisy link should tolerate 200ms delay, phi=%.2f", phiNoisy)
	}
	if phiCalm < 8 {
		t.Fatalf("calm link should treat 200ms silence as fatal, phi=%.2f", phiCalm)
	}
}

func TestMonitorReclaimsLeaseOnPartition(t *testing.T) {
	d := New(WithPhiThreshold(8), WithMinSamples(5))
	rng := rand.New(rand.NewSource(3))
	// Train so that the final heartbeat lands ~now; then silence begins.
	fast := netProfile{interval: 30 * time.Millisecond, jitter: 5 * time.Millisecond}
	start := time.Now().Add(-60 * 35 * time.Millisecond)
	train(d, start, 60, fast, rng)

	m := NewMonitor(d, 5*time.Millisecond)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	reclaimed := make(chan float64, 1)
	go m.Run(ctx, func(phi float64, at time.Time) { reclaimed <- phi })

	select {
	case phi := <-reclaimed:
		t.Logf("lease reclaimed at phi=%.2f", phi)
		if phi < 8 {
			t.Fatalf("reclaimed too early at phi=%.2f", phi)
		}
	case <-ctx.Done():
		t.Fatal("monitor never reclaimed the lease despite total silence")
	}

	if ok, at := m.Reclaimed(); !ok || at.IsZero() {
		t.Fatal("Reclaimed() should report true with a timestamp")
	}
}

func TestMonitorSurvivesJitterStorm(t *testing.T) {
	d := New(WithPhiThreshold(8), WithMinSamples(5), WithMinStdDev(25*time.Millisecond))
	rng := rand.New(rand.NewSource(11))

	// Background heartbeat goroutine with jitter and occasional spikes.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			default:
			}
			// Base interval ~35ms with jitter; every ~10th beat spikes to 100ms.
			delay := 25*time.Millisecond + time.Duration(rng.Int63n(int64(20*time.Millisecond)))
			if rng.Intn(10) == 0 {
				delay = 100 * time.Millisecond
			}
			time.Sleep(delay)
			d.Report(time.Now())
		}
	}()

	// Let it train, then watch for false reclaim during continued jitter.
	time.Sleep(500 * time.Millisecond)
	m := NewMonitor(d, 5*time.Millisecond)
	watchCtx, watchCancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer watchCancel()
	reclaimed := make(chan struct{}, 1)
	go m.Run(watchCtx, func(phi float64, at time.Time) { reclaimed <- struct{}{} })

	select {
	case <-reclaimed:
		t.Fatal("false positive: lease reclaimed during jitter storm")
	case <-watchCtx.Done():
		// Survived the jitter storm without reclaiming. Success.
	}
}

// Deterministic jitter storm in simulated time: 5000 beats with frequent
// spikes; phi is evaluated at every gap boundary and must never cross
// the threshold while heartbeats keep arriving.
func TestDetectorSurvivesJitterStormDeterministic(t *testing.T) {
	d := New(WithPhiThreshold(8))
	rng := rand.New(rand.NewSource(2024))
	storm := netProfile{
		interval:   50 * time.Millisecond,
		jitter:     20 * time.Millisecond,
		spikeEvery: 8,
		spike:      120 * time.Millisecond,
	}
	now := time.Now()
	d.Report(now)
	var maxPhi float64
	for i := 1; i <= 5000; i++ {
		delta := storm.interval + time.Duration(rng.Int63n(2*int64(storm.jitter))-int64(storm.jitter))
		if i%storm.spikeEvery == 0 {
			delta += storm.spike
		}
		// Evaluate phi just before the next heartbeat lands: this is the
		// worst-case moment of each gap.
		phi, err := d.Phi(now.Add(delta - time.Millisecond))
		if err != nil {
			t.Fatal(err)
		}
		if phi > maxPhi {
			maxPhi = phi
		}
		if d.IsSuspected(now.Add(delta - time.Millisecond)) {
			t.Fatalf("false positive at beat %d: phi=%.2f during jitter storm", i, phi)
		}
		now = now.Add(delta)
		d.Report(now)
	}
	t.Logf("max phi across 5000-beat jitter storm: %.2f (threshold 8)", maxPhi)
}

func TestPhiSaturatesGracefully(t *testing.T) {
	d := New(WithMinStdDev(time.Millisecond))
	last, _ := train(d, time.Now(), 50, netProfile{interval: 100 * time.Millisecond}, nil)
	phi, err := d.Phi(last.Add(1 * time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if math.IsNaN(phi) || phi < 8 {
		t.Fatalf("expected saturated phi >= threshold, got %v", phi)
	}
}

func TestSnapshotConsistency(t *testing.T) {
	d := New()
	rng := rand.New(rand.NewSource(5))
	last, _ := train(d, time.Now(), 100, steadyLink, rng)
	s := d.Snapshot(last.Add(120 * time.Millisecond))
	if s.Samples != 100 {
		t.Fatalf("expected 100 samples, got %d", s.Samples)
	}
	if s.Mean < 90*time.Millisecond || s.Mean > 130*time.Millisecond {
		t.Fatalf("mean off: %v", s.Mean)
	}
	if s.Suspected {
		t.Fatal("should not be suspected at +120ms")
	}
}
