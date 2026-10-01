package sim

import (
	"testing"

	"redsim/red"
)

func queueParams() red.Params {
	return red.Params{
		MinThBytes:     4000,
		MaxThBytes:     12000,
		MaxProb:        0.5,
		Weight:         0.002,
		CapacityBytes:  30000,
		MaxPacketBytes: 1500,
	}
}

type arrivalSpec struct {
	tick uint64
	flow int
	size int
}

// genArrivals builds a deterministic arrival stream from a keyed source.
func genArrivals(seed uint64, flows int, horizon uint64) []arrivalSpec {
	src := red.KeyedSource{Seed: seed}
	var out []arrivalSpec
	for f := 0; f < flows; f++ {
		tick := uint64(0)
		for k := uint64(0); ; k++ {
			gap := uint64(2 + src.Uint64(uint64(f)<<40|k*2)%18)
			size := int(64 + src.Uint64(uint64(f)<<40|k*2+1)%1437)
			tick += gap
			if tick > horizon {
				break
			}
			out = append(out, arrivalSpec{tick, f, size})
		}
	}
	return out
}

func build(t *testing.T, flows int, seed uint64, arrivals []arrivalSpec) *Network {
	t.Helper()
	params := make([]red.Params, flows)
	for i := range params {
		params[i] = queueParams()
	}
	n, err := New(Config{LinkRateBytesPerTick: 100, QuantumBytes: 1500}, params, seed)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	for _, a := range arrivals {
		if err := n.AddArrival(a.tick, a.flow, a.size); err != nil {
			t.Fatalf("AddArrival: %v", err)
		}
	}
	return n
}

func drain(n *Network, horizon uint64) {
	n.Run(horizon)
	for n.Pending() {
		n.Run(n.Now() + 100000)
	}
}

func statsEqual(a, b []FlowStats) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// Same seed => same trajectory: identical digest, drops and stats.
func TestReplayDeterminism(t *testing.T) {
	arrivals := genArrivals(7, 3, 20000)
	n1 := build(t, 3, 42, arrivals)
	n2 := build(t, 3, 42, arrivals)
	drain(n1, 20000)
	drain(n2, 20000)

	if len(n1.Drops) == 0 {
		t.Fatal("scenario produced no drops; cannot validate replay")
	}
	if n1.Digest() != n2.Digest() {
		t.Fatalf("same seed, different digests: %x vs %x", n1.Digest(), n2.Digest())
	}
	if !statsEqual(n1.Stats(), n2.Stats()) {
		t.Fatal("same seed, different stats")
	}

	n3 := build(t, 3, 43, arrivals)
	drain(n3, 20000)
	if n1.Digest() == n3.Digest() {
		t.Fatal("different seeds produced identical trajectories")
	}
}

// Processing the same event stream in chunks must not change sampling or
// the resulting trajectory.
func TestChunkingInvariance(t *testing.T) {
	arrivals := genArrivals(7, 3, 20000)

	oneShot := build(t, 3, 42, arrivals)
	drain(oneShot, 20000)

	chunked := build(t, 3, 42, arrivals)
	for tick := uint64(0); tick <= 20000; tick += 777 {
		chunked.Run(tick)
	}
	for chunked.Pending() {
		chunked.Run(chunked.Now() + 777)
	}

	if oneShot.Digest() != chunked.Digest() {
		t.Fatalf("chunked run diverged: %x vs %x", oneShot.Digest(), chunked.Digest())
	}
	if !statsEqual(oneShot.Stats(), chunked.Stats()) {
		t.Fatal("chunked run produced different stats")
	}
	if len(oneShot.Drops) != len(chunked.Drops) {
		t.Fatal("chunked run produced different drop count")
	}
}

// Every arrived byte is either completed or dropped; nothing leaks.
func TestByteConservation(t *testing.T) {
	arrivals := genArrivals(7, 3, 20000)
	n := build(t, 3, 42, arrivals)
	drain(n, 20000)

	for f, s := range n.Stats() {
		if got, want := s.BytesCompleted+s.BytesDropped, s.BytesArrived; got != want {
			t.Fatalf("flow %d: completed+dropped %d != arrived %d", f, got, want)
		}
		if got, want := s.Completed+s.Dropped, s.Arrived; got != want {
			t.Fatalf("flow %d: completed+dropped packets %d != arrived %d", f, got, want)
		}
	}
	for i, b := range n.QueueBytes() {
		if b != 0 {
			t.Fatalf("queue %d still holds %d bytes after drain", i, b)
		}
	}
}

// DRR shares the saturated link fairly and no flow starves, even with
// mixed packet sizes. Fairness is judged by per-flow byte throughput and
// worst-case delay, never by mean delay alone.
func TestFairSchedulingNoStarvation(t *testing.T) {
	arrivals := genArrivals(11, 3, 30000)
	n := build(t, 3, 42, arrivals)
	drain(n, 30000)

	stats := n.Stats()
	var minB, maxB uint64 = ^uint64(0), 0
	for f, s := range stats {
		if s.Completed == 0 {
			t.Fatalf("flow %d starved: zero completed packets", f)
		}
		if s.MaxDelay == 0 {
			t.Fatalf("flow %d: max delay not tracked", f)
		}
		if s.BytesCompleted < minB {
			minB = s.BytesCompleted
		}
		if s.BytesCompleted > maxB {
			maxB = s.BytesCompleted
		}
	}
	if ratio := float64(minB) / float64(maxB); ratio < 0.7 {
		t.Fatalf("unfair sharing: min/max completed bytes ratio %v", ratio)
	}
}

// A flow of 1500B packets competing with flows of 64B packets must still
// receive its fair byte share under DRR (quantum 1500B).
func TestMixedSizesNoStarvation(t *testing.T) {
	var arrivals []arrivalSpec
	for f := 0; f < 3; f++ {
		size := 64
		gap := uint64(1) // 64 B/tick, above the 33 B/tick fair share
		if f == 0 {
			size = 1500
			gap = 6 // 250 B/tick, also backlogged
		}
		for tick := uint64(0); tick <= 30000; tick += gap {
			arrivals = append(arrivals, arrivalSpec{tick, f, size})
		}
	}
	n := build(t, 3, 1, arrivals)
	drain(n, 30000)

	stats := n.Stats()
	total := uint64(0)
	for _, s := range stats {
		total += s.BytesCompleted
	}
	for f, s := range stats {
		if s.Completed == 0 {
			t.Fatalf("flow %d starved with mixed sizes", f)
		}
		share := float64(s.BytesCompleted) / float64(total)
		if share < 0.25 || share > 0.42 {
			t.Fatalf("flow %d byte share %v outside fair band", f, share)
		}
	}
}

// An uncongested network stays below minTh and drops nothing.
func TestNoDropsWhenUncongested(t *testing.T) {
	var arrivals []arrivalSpec
	for f := 0; f < 3; f++ {
		for tick := uint64(0); tick <= 10000; tick += 200 {
			arrivals = append(arrivals, arrivalSpec{tick, f, 500})
		}
	}
	n := build(t, 3, 1, arrivals)
	drain(n, 10000)
	if len(n.Drops) != 0 {
		t.Fatalf("uncongested network dropped %d packets", len(n.Drops))
	}
}

// Under overload, every drop carries an auditable basis, and probabilistic
// drops only occur with avg inside [minTh, maxTh).
func TestDropRecordsCarryBasis(t *testing.T) {
	arrivals := genArrivals(7, 3, 20000)
	n := build(t, 3, 42, arrivals)
	drain(n, 20000)

	if len(n.Drops) == 0 {
		t.Fatal("overloaded scenario produced no drops")
	}
	p := queueParams()
	seen := map[red.Verdict]bool{}
	for _, d := range n.Drops {
		if d.Decision.Reason == "" {
			t.Fatalf("drop pkt=%d has no recorded basis", d.Packet)
		}
		seen[d.Decision.Verdict] = true
		switch d.Decision.Verdict {
		case red.DropProbabilistic:
			if d.Decision.Avg < p.MinThBytes || d.Decision.Avg >= p.MaxThBytes {
				t.Fatalf("probabilistic drop with avg %v outside [%v,%v)",
					d.Decision.Avg, p.MinThBytes, p.MaxThBytes)
			}
			if d.Decision.Draw >= d.Decision.Prob {
				t.Fatalf("probabilistic drop with draw %v >= p %v",
					d.Decision.Draw, d.Decision.Prob)
			}
		case red.DropForced:
			if d.Decision.Avg < p.MaxThBytes {
				t.Fatalf("forced drop with avg %v < maxTh %v", d.Decision.Avg, p.MaxThBytes)
			}
		case red.DropOverflow:
		default:
			t.Fatalf("unexpected verdict %v in drop log", d.Decision.Verdict)
		}
	}
	if !seen[red.DropForced] && !seen[red.DropOverflow] {
		t.Fatal("overloaded scenario produced neither forced nor overflow drops")
	}
}
