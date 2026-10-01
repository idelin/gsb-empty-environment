package red

import (
	"math"
	"testing"
)

func testParams() Params {
	return Params{
		MinThBytes:     1000,
		MaxThBytes:     3000,
		MaxProb:        0.5,
		Weight:         0.002,
		CapacityBytes:  20000,
		MaxPacketBytes: 1500,
	}
}

func mustNew(t *testing.T, p Params) *RED {
	t.Helper()
	r, err := New(p)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return r
}

// exhaustiveSource enumerates every `bits`-bit random value as an event
// identity: identity id maps to the draw id / 2^bits exactly. This lets
// tests enumerate the full finite random-bit space of the drop sampler.
type exhaustiveSource struct{ bits uint }

func (s exhaustiveSource) Uint64(id uint64) uint64 { return id << (64 - s.bits) }

func TestParamsValidate(t *testing.T) {
	if err := testParams().Validate(); err != nil {
		t.Fatalf("valid params rejected: %v", err)
	}
	bad := testParams()
	bad.MaxThBytes = bad.MinThBytes
	if err := bad.Validate(); err == nil {
		t.Fatal("maxTh == minTh accepted")
	}
	bad = testParams()
	bad.Weight = 0
	if err := bad.Validate(); err == nil {
		t.Fatal("zero weight accepted")
	}
}

// Below MinThBytes there must never be a probabilistic drop, for any draw.
func TestBelowThresholdNeverDrops(t *testing.T) {
	r := mustNew(t, testParams())
	r.avg = 500 // below minTh
	const bits = 10
	src := exhaustiveSource{bits: bits}
	for id := uint64(0); id < 1<<bits; id++ {
		d := r.Decide(1, 500, 1500, src, id)
		if d.Verdict != Accept {
			t.Fatalf("id=%d: verdict %v below minTh", id, d.Verdict)
		}
		if d.Prob != 0 || d.BaseProb != 0 {
			t.Fatalf("id=%d: nonzero probability below minTh", id)
		}
	}
}

// At or above MaxThBytes every packet is force-dropped regardless of draw.
func TestAboveThresholdAlwaysForced(t *testing.T) {
	r := mustNew(t, testParams())
	r.avg = 3000 // exactly maxTh
	const bits = 10
	src := exhaustiveSource{bits: bits}
	for id := uint64(0); id < 1<<bits; id++ {
		d := r.Decide(1, 3000, 1500, src, id)
		if d.Verdict != DropForced {
			t.Fatalf("id=%d: verdict %v at maxTh, want forced", id, d.Verdict)
		}
		if d.Prob != 1 {
			t.Fatalf("id=%d: forced drop with prob %v", id, d.Prob)
		}
	}
}

// Enumerate the full finite random-bit space and verify the empirical drop
// probability matches the configured probability exactly (up to the grid
// resolution), and that every byte is accounted for (byte conservation).
func TestDropProbabilityByEnumeration(t *testing.T) {
	cases := []struct {
		name string
		avg  float64
		size int
		want float64 // expected probability
	}{
		{"avg-mid-full-size", 2000, 1500, 0.25},   // 0.5 * (2000-1000)/2000
		{"avg-mid-half-size", 2000, 750, 0.125},   // packet-size normalization
		{"avg-low-quarter", 1500, 1500, 0.125},    // 0.5 * 500/2000
		{"avg-near-max", 2998, 1500, 0.5 * 0.999}, // just below forced
	}
	const bits = 12
	const n = 1 << bits
	src := exhaustiveSource{bits: bits}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := mustNew(t, testParams())
			r.avg = tc.avg
			var drops, accepts uint64
			var droppedBytes, acceptedBytes uint64
			for id := uint64(0); id < n; id++ {
				// queueBytes == avg keeps the average fixed across calls.
				d := r.Decide(1, int(tc.avg), tc.size, src, id)
				switch d.Verdict {
				case DropProbabilistic:
					drops++
					droppedBytes += uint64(tc.size)
				case Accept:
					accepts++
					acceptedBytes += uint64(tc.size)
				default:
					t.Fatalf("id=%d: unexpected verdict %v", id, d.Verdict)
				}
			}
			// Byte conservation: every arrived byte is accepted or dropped.
			if got, want := acceptedBytes+droppedBytes, uint64(n)*uint64(tc.size); got != want {
				t.Fatalf("byte conservation violated: %d != %d", got, want)
			}
			if drops+accepts != n {
				t.Fatalf("decision conservation violated: %d + %d != %d", drops, accepts, n)
			}
			// Exact count: drop iff id/n < p, i.e. count == ceil(p*n).
			wantDrops := uint64(math.Ceil(tc.want * n))
			if drops != wantDrops {
				t.Fatalf("drops = %d, want exactly %d (p=%v, n=%d)", drops, wantDrops, tc.want, n)
			}
			if got := float64(drops) / n; math.Abs(got-tc.want) > 1.0/n {
				t.Fatalf("empirical p = %v, want %v +/- %v", got, tc.want, 1.0/n)
			}
		})
	}
}

// The EWMA update and the idle decay are separate operations: UpdateAverage
// never decays, DecayIdle never pulls toward the instantaneous length.
func TestAverageUpdateAndIdleDecayAreSeparate(t *testing.T) {
	r := mustNew(t, testParams())
	r.avg = 2048
	r.UpdateAverage(2048)
	if r.avg != 2048 {
		t.Fatalf("UpdateAverage changed avg for equal qlen: %v", r.avg)
	}
	r.UpdateAverage(4096)
	if want := 2048 + 0.002*(4096-2048); r.avg != want {
		t.Fatalf("EWMA step: got %v want %v", r.avg, want)
	}

	before := r.avg
	r.DecayIdle(10)
	want := before * math.Pow(1-0.002, 10)
	if math.Abs(r.avg-want) > 1e-9 {
		t.Fatalf("DecayIdle: got %v want %v", r.avg, want)
	}
	r.DecayIdle(0)
	if r.avg != want {
		t.Fatal("DecayIdle(0) changed the average")
	}
}

// Idle decay integrates with Decide via NoteQueueEmpty, and stays separate
// from the probability normalization.
func TestIdleDecayAppliedOnNextDecide(t *testing.T) {
	r := mustNew(t, testParams())
	r.avg = 2000
	r.NoteQueueEmpty(5)
	r.Decide(105, 0, 1500, exhaustiveSource{bits: 12}, 0)
	want := 2000*math.Pow(0.998, 100) + 0.002*(0-2000*math.Pow(0.998, 100))
	if math.Abs(r.Average()-want) > 1e-6 {
		t.Fatalf("avg after idle decay + EWMA: got %v want %v", r.Average(), want)
	}
}

// Packet-size normalization scales the probability linearly and is clamped.
func TestPacketSizeNormalization(t *testing.T) {
	if got := NormalizeForPacketSize(0.4, 750, 1500); got != 0.2 {
		t.Fatalf("half-size normalization: got %v want 0.2", got)
	}
	if got := NormalizeForPacketSize(0.8, 3000, 1500); got != 1 {
		t.Fatalf("oversize normalization not clamped: got %v", got)
	}
	if got := NormalizeForPacketSize(0.4, 0, 1500); got != 0 {
		t.Fatalf("zero-size normalization: got %v", got)
	}
}

// KeyedSource is a pure function of (seed, identity).
func TestKeyedSourceDeterministic(t *testing.T) {
	a := KeyedSource{Seed: 42}
	b := KeyedSource{Seed: 42}
	c := KeyedSource{Seed: 43}
	for id := uint64(0); id < 100; id++ {
		if a.Uint64(id) != b.Uint64(id) {
			t.Fatalf("same seed, id %d: different values", id)
		}
	}
	diffID, diffSeed := 0, 0
	for id := uint64(0); id < 100; id++ {
		if a.Uint64(id) != a.Uint64(id+1000) {
			diffID++
		}
		if a.Uint64(id) != c.Uint64(id) {
			diffSeed++
		}
	}
	if diffID < 95 || diffSeed < 95 {
		t.Fatalf("keyed source looks correlated: diffID=%d diffSeed=%d", diffID, diffSeed)
	}
}
