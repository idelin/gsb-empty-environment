package freqsketch

import (
	"math"
	"math/rand"
	"strconv"
	"sync"
	"testing"
)

// injectiveHasher maps keys "k0".."k9" to distinct buckets in every row
// (buckets >= 10), isolating decay logic from collisions.
func injectiveHasher(key string, seed uint64, buckets int) int {
	idx := int(key[len(key)-1] - '0')
	return (idx + int(seed%7)) % buckets
}

func TestAddAndEstimate(t *testing.T) {
	s := NewSketch(SketchConfig{})
	s.Add("a", 3)
	s.Add("a", 2)
	s.Add("b", 1)
	if got := s.Estimate("a"); got != 5 {
		t.Fatalf("Estimate(a) = %d, want 5", got)
	}
	if got := s.Estimate("b"); got != 1 {
		t.Fatalf("Estimate(b) = %d, want 1", got)
	}
	if got := s.Estimate("missing"); got != 0 {
		t.Fatalf("Estimate(missing) = %d, want 0", got)
	}
}

// TestSaturationNoOverflow: counts saturate at MaxCount and never wrap.
func TestSaturationNoOverflow(t *testing.T) {
	s := NewSketch(SketchConfig{MaxCount: 100})
	for i := 0; i < 1000; i++ {
		s.Add("hot", 1)
	}
	if got := s.Estimate("hot"); got != 100 {
		t.Fatalf("after saturation Estimate = %d, want 100", got)
	}
	// A huge delta must saturate, not overflow.
	if got := s.Add("hot", math.MaxUint64); got != 100 {
		t.Fatalf("Add(MaxUint64) = %d, want 100 (saturated)", got)
	}
	if got := s.Estimate("hot"); got != 100 {
		t.Fatalf("Estimate after huge delta = %d, want 100", got)
	}
}

// TestDecayInterleavingEquivalence: interleaving decay epochs with new
// events must equal the declared logical order. The reference model
// applies decay eagerly at the declared points; the sketch decays lazily.
func TestDecayInterleavingEquivalence(t *testing.T) {
	type op struct {
		key   string
		decay bool
	}
	ops := []op{
		{key: "k1"}, {key: "k2"}, {key: "k1"}, {key: "k3"},
		{decay: true},
		{key: "k1"}, {key: "k4"}, {key: "k2"},
		{decay: true}, {decay: true},
		{key: "k1"}, {key: "k5"},
		{decay: true},
		{key: "k2"}, {key: "k1"},
	}

	// Reference model: exact counts, eager halving at decay points.
	model := map[string]uint64{}
	// Sketch with collision-free hasher.
	s := NewSketch(SketchConfig{
		Shards: 4, Rows: 3, BucketsPerShard: 64, Hasher: injectiveHasher,
	})

	for _, o := range ops {
		if o.decay {
			for k := range model {
				model[k] >>= 1
			}
			s.AdvanceEpoch()
			continue
		}
		model[o.key]++
		s.Add(o.key, 1)
		// Interleave reads at arbitrary points; they must not disturb
		// the logical sequence.
		s.Estimate("k1")
		s.Estimate("k9")
	}
	for k, want := range model {
		if got := s.Estimate(k); got != want {
			t.Fatalf("Estimate(%s) = %d, want %d (logical order violated)", k, got, want)
		}
	}
	// Untouched keys decay to zero.
	if got := s.Estimate("k9"); got != 0 {
		t.Fatalf("Estimate(k9) = %d, want 0", got)
	}
}

// TestDecayNeverNegative: decay is a right shift on unsigned saturating
// counters; values can never go negative and always reach zero.
func TestDecayNeverNegative(t *testing.T) {
	s := NewSketch(SketchConfig{MaxCount: 50})
	s.Add("a", 50)
	s.Add("b", 7)
	for i := 0; i < 200; i++ {
		s.AdvanceEpoch()
	}
	for _, k := range []string{"a", "b"} {
		if got := s.Estimate(k); got != 0 {
			t.Fatalf("Estimate(%s) = %d after 200 epochs, want 0", k, got)
		}
	}
	// Interleave adds with heavy decay; values stay within [0, MaxCount].
	for i := 0; i < 100; i++ {
		s.Add("a", 3)
		s.AdvanceEpoch()
		if got := s.Estimate("a"); got > 50 {
			t.Fatalf("Estimate exceeded MaxCount: %d", got)
		}
	}
}

// TestForcedCollisionCorrectness: a hasher that collides every key into
// one bucket must not break value correctness: estimates stay upper
// bounds, never negative, never above MaxCount.
func TestForcedCollisionCorrectness(t *testing.T) {
	allCollide := func(string, uint64, int) int { return 0 }
	s := NewSketch(SketchConfig{Hasher: allCollide, MaxCount: 1 << 20})

	r := rand.New(rand.NewSource(7))
	exact := map[string]uint64{}
	var total uint64
	keys := make([]string, 64)
	for i := range keys {
		keys[i] = "key-" + strconv.Itoa(i)
	}
	for i := 0; i < 20000; i++ {
		k := keys[r.Intn(len(keys))]
		s.Add(k, 1)
		exact[k]++
		total++
	}
	for k, v := range exact {
		est := s.Estimate(k)
		if est < v {
			t.Fatalf("collision caused underestimate for %s: est=%d exact=%d", k, est, v)
		}
		if est > total {
			t.Fatalf("estimate %d exceeds total inserts %d", est, total)
		}
	}
	// Saturate the shared cell, then decay fully: still no wrap-around.
	s.Add("key-0", math.MaxUint64)
	if got := s.Estimate("key-0"); got != 1<<20 {
		t.Fatalf("saturated estimate = %d, want %d", got, 1<<20)
	}
	for i := 0; i < 64; i++ {
		s.AdvanceEpoch()
	}
	for k := range exact {
		if got := s.Estimate(k); got != 0 {
			t.Fatalf("Estimate(%s) = %d after full decay, want 0", k, got)
		}
	}
}

// TestEstimateErrorBound: against an independent exact reference, the
// sketch never underestimates, and aggregate error stays bounded.
func TestEstimateErrorBound(t *testing.T) {
	s := NewSketch(SketchConfig{}) // 16 shards x 4 rows x 1024 buckets
	r := rand.New(rand.NewSource(42))
	z := rand.NewZipf(r, 1.2, 1, 999)
	exact := map[string]uint64{}
	const n = 200000
	for i := 0; i < n; i++ {
		k := "item-" + strconv.FormatUint(z.Uint64(), 10)
		s.Add(k, 1)
		exact[k]++
	}
	var absErr uint64
	for k, v := range exact {
		est := s.Estimate(k)
		if est < v {
			t.Fatalf("underestimate for %s: est=%d exact=%d", k, est, v)
		}
		absErr += est - v
	}
	// With 64k cells and 200k inserts, per-cell load is ~3; min over 4
	// rows keeps the total absolute error far below 10% of the stream.
	if got := float64(absErr) / n; got > 0.10 {
		t.Fatalf("total relative error %.4f exceeds 0.10", got)
	}
	t.Logf("keys=%d total-abs-err=%d (%.4f of stream)", len(exact), absErr, float64(absErr)/n)
}

// TestConcurrentShards: sharded updates under concurrency preserve the
// total count (no lost updates, no races — run with -race).
func TestConcurrentShards(t *testing.T) {
	s := NewSketch(SketchConfig{})
	const workers = 8
	const perWorker = 5000
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			key := "w" + strconv.Itoa(id)
			for i := 0; i < perWorker; i++ {
				s.Add(key, 1)
			}
		}(w)
	}
	wg.Wait()
	for w := 0; w < workers; w++ {
		if got := s.Estimate("w" + strconv.Itoa(w)); got < perWorker {
			t.Fatalf("worker %d: Estimate = %d, want >= %d", w, got, perWorker)
		}
	}
}

// TestSnapshotRestore: checkpoint and restore reproduce identical state.
func TestSnapshotRestore(t *testing.T) {
	s := NewSketch(SketchConfig{})
	s.Add("a", 5)
	s.Add("b", 9)
	s.AdvanceEpoch()
	snap := s.Snapshot()
	// At snapshot time epoch=1, so lazy decay already applies on read:
	// a reads as 5>>1=2, b as 9>>1=4. Restore must reproduce exactly that.
	if got := s.Estimate("a"); got != 2 {
		t.Fatalf("at snapshot Estimate(a) = %d, want 2", got)
	}
	s.Add("a", 100)
	s.AdvanceEpoch()
	s.Restore(snap)
	if got := s.Estimate("a"); got != 2 {
		t.Fatalf("after restore Estimate(a) = %d, want 2", got)
	}
	if got := s.Estimate("b"); got != 4 {
		t.Fatalf("after restore Estimate(b) = %d, want 4", got)
	}
	// Post-restore decay follows the restored epoch.
	s.AdvanceEpoch()
	if got := s.Estimate("a"); got != 1 {
		t.Fatalf("after restore+decay Estimate(a) = %d, want 1", got)
	}
}
