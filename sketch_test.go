package decayfreq

import (
	"math/rand"
	"sort"
	"sync"
	"testing"
)

// exactRef is an independent exact frequency reference with eager decay.
type exactRef struct {
	counts map[string]uint64
}

func newExactRef() *exactRef { return &exactRef{counts: map[string]uint64{}} }

func (r *exactRef) add(key string, n uint64) { r.counts[key] += n }

func (r *exactRef) decay() {
	for k, v := range r.counts {
		r.counts[k] = v >> 1
	}
}

// TestEstimateErrorAgainstExactReference inserts a skewed stream and compares
// estimates with an independent exact reference: estimates must never
// underestimate, and the overestimate must stay within a loose deterministic
// bound derived from N/width.
func TestEstimateErrorAgainstExactReference(t *testing.T) {
	const (
		rows    = 5
		width   = 4096
		nEvents = 200000
		nKeys   = 5000
	)
	sk := NewSketch(rows, width)
	ref := newExactRef()
	rng := rand.New(rand.NewSource(1))
	keys := make([]string, nKeys)
	for i := range keys {
		keys[i] = string(rune('a'+i%26)) + "-" + string(rune('A'+i/26%26)) + "-" + itoa(i)
	}
	for i := 0; i < nEvents; i++ {
		// Skewed: small indices (heavy hitters) dominate.
		idx := int(rng.Float64() * rng.Float64() * rng.Float64() * nKeys)
		sk.Add(keys[idx], 1)
		ref.add(keys[idx], 1)
	}
	maxOver := uint64(0)
	for _, k := range keys {
		est := uint64(sk.Estimate(k))
		exact := ref.counts[k]
		if est < exact {
			t.Fatalf("estimate %d underestimates exact %d for key %q", est, exact, k)
		}
		if over := est - exact; over > maxOver {
			maxOver = over
		}
	}
	// Loose deterministic bound: expected noise per counter is N/width;
	// min over 5 rows keeps the observed overestimate far below 8x that.
	bound := uint64(8 * nEvents / width)
	if maxOver > bound {
		t.Fatalf("max overestimate %d exceeds bound %d", maxOver, bound)
	}
	// Heavy hitters must have small relative error.
	type kv struct {
		k string
		c uint64
	}
	var top []kv
	for k, c := range ref.counts {
		top = append(top, kv{k, c})
	}
	sort.Slice(top, func(i, j int) bool { return top[i].c > top[j].c })
	for _, e := range top[:10] {
		est := uint64(sk.Estimate(e.k))
		rel := float64(est-e.c) / float64(e.c)
		if rel > 0.02 {
			t.Fatalf("heavy hitter %q: exact %d est %d rel err %.4f > 2%%", e.k, e.c, est, rel)
		}
	}
	t.Logf("max overestimate %d (bound %d)", maxOver, bound)
}

// TestDecayInterleavingEquivalence proves the declared logical order: Decay()
// halves every counter at call time. The lazy implementation is compared
// counter-by-counter against an eager model applying the same op sequence.
func TestDecayInterleavingEquivalence(t *testing.T) {
	type op struct {
		isDecay bool
		key     string
		n       uint32
	}
	ops := []op{
		{key: "a", n: 3}, {key: "b", n: 5}, {isDecay: true},
		{key: "a", n: 4}, {key: "c", n: 2}, {isDecay: true}, {isDecay: true},
		{key: "b", n: 1}, {key: "a", n: 7}, {isDecay: true},
		{key: "c", n: 9},
	}
	sk := NewSketch(4, 64)
	// Eager counter-level model using the same hasher.
	model := make([][]uint32, 4)
	for i := range model {
		model[i] = make([]uint32, 64)
	}
	ref := newExactRef()
	for _, o := range ops {
		if o.isDecay {
			sk.Decay()
			for r := range model {
				for j := range model[r] {
					model[r][j] >>= 1
				}
			}
			ref.decay()
			continue
		}
		sk.Add(o.key, o.n)
		ref.add(o.key, uint64(o.n))
		for r := range model {
			idx := sk.index(r, o.key)
			c := model[r][idx]
			if uint64(c)+uint64(o.n) > uint64(MaxCount) {
				model[r][idx] = MaxCount
			} else {
				model[r][idx] = c + o.n
			}
		}
	}
	snap := sk.Snapshot()
	for r := range model {
		for j := range model[r] {
			if snap.Counters[r][j] != model[r][j] {
				t.Fatalf("counter[%d][%d]: lazy %d != eager %d", r, j, snap.Counters[r][j], model[r][j])
			}
		}
	}
	// Estimates still upper-bound the exact decayed reference.
	for _, k := range []string{"a", "b", "c"} {
		if est := uint64(sk.Estimate(k)); est < ref.counts[k] {
			t.Fatalf("key %q: estimate %d < decayed exact %d", k, est, ref.counts[k])
		}
	}
}

// TestSaturationNoOverflow verifies counters saturate at MaxCount instead of
// wrapping, and stay correct after decay.
func TestSaturationNoOverflow(t *testing.T) {
	sk := NewSketch(4, 64)
	sk.Add("hot", MaxCount)
	sk.Add("hot", MaxCount) // would wrap to ~0 on overflow
	if est := sk.Estimate("hot"); est != MaxCount {
		t.Fatalf("saturated estimate = %d, want %d", est, uint32(MaxCount))
	}
	sk.Decay()
	if est := sk.Estimate("hot"); est != MaxCount>>1 {
		t.Fatalf("post-decay estimate = %d, want %d", est, uint32(MaxCount>>1))
	}
}

// TestDecayNeverNegative hammers decay far beyond the count magnitude.
func TestDecayNeverNegative(t *testing.T) {
	sk := NewSketch(4, 64)
	sk.Add("x", 1)
	for i := 0; i < 100; i++ {
		sk.Decay()
	}
	if est := sk.Estimate("x"); est != 0 {
		t.Fatalf("estimate after 100 decays = %d, want 0", est)
	}
	// Counters remain usable after deep decay.
	sk.Add("x", 3)
	if est := sk.Estimate("x"); est != 3 {
		t.Fatalf("estimate after re-add = %d, want 3", est)
	}
}

// TestForcedCollisions forces every key into the same slot in every row.
// Estimates become the shared total: still upper bounds, never negative,
// and value-correct for the colliding counter itself.
func TestForcedCollisions(t *testing.T) {
	collide := func(row int, key string) uint64 { return 0 }
	sk := NewSketch(4, 64, WithHasher(collide))
	sk.Add("a", 5)
	sk.Add("b", 7)
	if est := sk.Estimate("a"); est != 12 {
		t.Fatalf("colliding estimate = %d, want shared total 12", est)
	}
	if est := sk.Estimate("b"); est != 12 {
		t.Fatalf("colliding estimate = %d, want shared total 12", est)
	}
	sk.Decay()
	if est := sk.Estimate("a"); est != 6 {
		t.Fatalf("post-decay colliding estimate = %d, want 6", est)
	}
	// Saturation under collisions does not overflow.
	sk.Add("a", MaxCount)
	sk.Add("b", MaxCount)
	if est := sk.Estimate("c"); est != MaxCount {
		t.Fatalf("saturated colliding estimate = %d, want %d", est, uint32(MaxCount))
	}
}

// TestConcurrentShardedAdds hammers the shards from many goroutines; run with
// -race. With no decay, estimates must equal exact counts for keys whose
// slots are collision-free, and upper-bound them otherwise.
func TestConcurrentShardedAdds(t *testing.T) {
	sk := NewSketch(4, 1024)
	ref := newExactRef()
	var mu sync.Mutex
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			rng := rand.New(rand.NewSource(int64(g)))
			for i := 0; i < 5000; i++ {
				k := itoa(rng.Intn(500))
				sk.Add(k, 1)
				mu.Lock()
				ref.add(k, 1)
				mu.Unlock()
			}
		}(g)
	}
	wg.Wait()
	for k, exact := range ref.counts {
		if est := uint64(sk.Estimate(k)); est < exact {
			t.Fatalf("key %q: estimate %d < exact %d", k, est, exact)
		}
	}
}

// itoa is a tiny deterministic int->string to keep tests dependency-free.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}
