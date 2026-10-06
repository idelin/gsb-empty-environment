package mixer

import (
	"math/big"
	"math/rand"
	"testing"
)

// naiveMixer is an independent, deliberately simple reference: it keeps the
// raw segment list and recomputes every requested sample from scratch with
// big.Rat arithmetic, rounding half-to-even and saturating at the end.
type naiveMixer struct {
	min, max int64
	segs     map[string]naiveSegment
}

type naiveSegment struct {
	pcm    [][]int64
	offset int64
	chMap  []int
	num    int64
	den    int64
}

func newNaive(min, max int64) *naiveMixer {
	return &naiveMixer{min: min, max: max, segs: make(map[string]naiveSegment)}
}

func (n *naiveMixer) add(id string, pcm [][]int64, offset int64, chMap []int, num, den int64) {
	if chMap == nil {
		chMap = make([]int, len(pcm))
		for i := range chMap {
			chMap[i] = i
		}
	}
	cp := make([][]int64, len(pcm))
	for i, ch := range pcm {
		cp[i] = append([]int64(nil), ch...)
	}
	n.segs[id] = naiveSegment{pcm: cp, offset: offset, chMap: chMap, num: num, den: den}
}

func (n *naiveMixer) remove(id string) { delete(n.segs, id) }

func (n *naiveMixer) sample(ch int, pos int64) int64 {
	sum := new(big.Rat)
	for _, s := range n.segs {
		for src, dst := range s.chMap {
			if dst != ch {
				continue
			}
			i := pos - s.offset
			if i < 0 || i >= int64(len(s.pcm[src])) {
				continue
			}
			gain := big.NewRat(s.num, s.den)
			sum.Add(sum, gain.Mul(gain, big.NewRat(s.pcm[src][i], 1)))
		}
	}
	return clampInt64(roundRatHalfEven(sum), n.min, n.max)
}

// roundRatHalfEven rounds r to the nearest integer, ties to even.
func roundRatHalfEven(r *big.Rat) int64 {
	num, den := r.Num(), r.Denom()
	q, rem := new(big.Int), new(big.Int)
	q.QuoRem(num, den, rem)
	abs2 := new(big.Int).Abs(rem)
	abs2.Lsh(abs2, 1)
	c := abs2.Cmp(den)
	if c > 0 || (c == 0 && q.Bit(0) == 1) {
		if num.Sign() >= 0 {
			q.Add(q, big.NewInt(1))
		} else {
			q.Sub(q, big.NewInt(1))
		}
	}
	if !q.IsInt64() {
		if q.Sign() < 0 {
			return minInt64
		}
		return maxInt64
	}
	return q.Int64()
}

func clampInt64(v, min, max int64) int64 {
	if v < min {
		return min
	}
	if v > max {
		return max
	}
	return v
}

func mustNew(t *testing.T, channels int, min, max int64) *Mixer {
	t.Helper()
	m, err := New(channels, min, max)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return m
}

func mustAdd(t *testing.T, m *Mixer, id string, pcm [][]int64, offset int64, chMap []int, num, den int64) Range {
	t.Helper()
	r, err := m.Add(id, pcm, offset, chMap, num, den)
	if err != nil {
		t.Fatalf("Add(%q): %v", id, err)
	}
	return r
}

func TestHalfEvenTies(t *testing.T) {
	m := mustNew(t, 1, -32768, 32767)
	// gain 1/2 turns odd samples into exact .5 values.
	pcm := [][]int64{{3, 5, -3, -5, 1, -1, 7}}
	mustAdd(t, m, "a", pcm, 0, nil, 1, 2)
	want := []int64{2, 2, -2, -2, 0, 0, 4} // 1.5->2, 2.5->2, -1.5->-2, -2.5->-2, 0.5->0, -0.5->0, 3.5->4
	for i, w := range want {
		if got := m.Sample(0, int64(i)); got != w {
			t.Errorf("pos %d: got %d, want %d", i, got, w)
		}
	}
}

func TestNoPerTrackSaturation(t *testing.T) {
	m := mustNew(t, 1, -100, 100)
	// 300 alone would clip to 100; combined with -250 the exact sum is 50
	// and must come out unsaturated.
	mustAdd(t, m, "hot", [][]int64{{300}}, 0, nil, 1, 1)
	mustAdd(t, m, "neg", [][]int64{{250}}, 0, nil, -1, 1)
	if got := m.Sample(0, 0); got != 50 {
		t.Fatalf("got %d, want 50 (per-track saturation would give -150 or 0)", got)
	}
	// Same idea across a shared denominator: 3/2 * 160 = 240, minus 140.
	mustAdd(t, m, "wide", [][]int64{{160}}, 1, nil, 3, 2)
	mustAdd(t, m, "down", [][]int64{{140}}, 1, nil, -1, 1)
	if got := m.Sample(0, 1); got != 100 {
		t.Fatalf("got %d, want 100", got)
	}
}

func TestSaturationBoundaries(t *testing.T) {
	m := mustNew(t, 1, -32768, 32767)
	mustAdd(t, m, "a", [][]int64{{32767, -32768, 32767, -32768}}, 0, nil, 1, 1)
	mustAdd(t, m, "b", [][]int64{{1, -1, 20000, -20000}}, 0, nil, 1, 1)
	want := []int64{32767, -32768, 32767, -32768} // 32768, -32769, 52767, -52768 all clip
	for i, w := range want {
		if got := m.Sample(0, int64(i)); got != w {
			t.Errorf("pos %d: got %d, want %d", i, got, w)
		}
	}
}

func TestRemoveRecomputesRange(t *testing.T) {
	m := mustNew(t, 2, -32768, 32767)
	mustAdd(t, m, "keep", [][]int64{{10, 20, 30, 40}}, 2, nil, 1, 1)
	r := mustAdd(t, m, "gone", [][]int64{{5, 5, 5}}, 3, []int{1}, 2, 1)
	if r.Start != 3 || r.End != 6 || len(r.Channels) != 1 || r.Channels[0] != 1 {
		t.Fatalf("unexpected add range: %+v", r)
	}
	before := m.Samples(1, 0, 8)
	rr, ok := m.Remove("gone")
	if !ok {
		t.Fatal("Remove reported missing segment")
	}
	if rr.Start != 3 || rr.End != 6 {
		t.Fatalf("unexpected remove range: %+v", rr)
	}
	after := m.Samples(1, 0, 8)
	for i := range before {
		var want int64
		if i >= 3 && i < 6 {
			want = before[i] - 10 // contribution was 2*5
		} else {
			want = before[i]
		}
		if after[i] != want {
			t.Errorf("pos %d: got %d, want %d", i, after[i], want)
		}
	}
	if _, ok := m.Remove("gone"); ok {
		t.Fatal("second Remove should report missing segment")
	}
}

func TestNegativeOffsetAndChannelMap(t *testing.T) {
	m := mustNew(t, 3, -32768, 32767)
	// Source channel 0 -> out 2, source channel 1 -> out 0; negative offset.
	mustAdd(t, m, "a", [][]int64{{100, 200, 300}, {7, 8, 9}}, -2, []int{2, 0}, -1, 2)
	// out2: -50 at pos -2, -100 at -1, -150 at 0; out0: -4 (3.5->4), -4, -4 (ties to even)
	if got := m.Sample(2, -2); got != -50 {
		t.Errorf("ch2 pos-2: got %d, want -50", got)
	}
	if got := m.Sample(2, 0); got != -150 {
		t.Errorf("ch2 pos0: got %d, want -150", got)
	}
	if got := m.Sample(0, -2); got != -4 { // -3.5 ties to even
		t.Errorf("ch0 pos-2: got %d, want -4", got)
	}
	if got := m.Sample(1, 0); got != 0 {
		t.Errorf("ch1 pos0: got %d, want 0", got)
	}
}

// TestRandomizedAgainstNaive cross-checks the mixer against the naive
// big-integer reference through random sequences of adds and removes with
// overlapping segments, negative offsets, negative rational gains and
// boundary-clipping values.
func TestRandomizedAgainstNaive(t *testing.T) {
	const (
		channels = 3
		minOut   = -32768
		maxOut   = 32767
		ops      = 60
	)
	rng := rand.New(rand.NewSource(20261007))
	m := mustNew(t, channels, minOut, maxOut)
	ref := newNaive(minOut, maxOut)

	interesting := []int64{-32768, -32767, -20000, -2, -1, 0, 1, 2, 20000, 32766, 32767}
	randSample := func() int64 {
		if rng.Intn(2) == 0 {
			return interesting[rng.Intn(len(interesting))]
		}
		return int64(rng.Intn(65536) - 32768)
	}

	var ids []string
	var lo, hi int64
	seen := false
	noteRange := func(off int64, length int) {
		end := off + int64(length)
		if !seen {
			lo, hi, seen = off, end, true
			return
		}
		if off < lo {
			lo = off
		}
		if end > hi {
			hi = end
		}
	}

	for op := 0; op < ops; op++ {
		if len(ids) > 0 && rng.Intn(3) == 0 {
			// Remove a random live segment.
			k := rng.Intn(len(ids))
			id := ids[k]
			ids = append(ids[:k], ids[k+1:]...)
			if _, ok := m.Remove(id); !ok {
				t.Fatalf("op %d: Remove(%q) failed", op, id)
			}
			ref.remove(id)
		} else {
			// Add a random segment.
			nSrc := 1 + rng.Intn(2)
			length := 1 + rng.Intn(24)
			pcm := make([][]int64, nSrc)
			for c := range pcm {
				pcm[c] = make([]int64, length)
				for i := range pcm[c] {
					pcm[c][i] = randSample()
				}
			}
			chMap := make([]int, nSrc)
			for i := range chMap {
				chMap[i] = rng.Intn(channels)
			}
			offset := int64(rng.Intn(41) - 20)
			num := int64(rng.Intn(9) - 4) // -4..4, may be 0
			den := int64(1 + rng.Intn(8))
			id := string(rune('A' + op))
			if _, err := m.Add(id, pcm, offset, chMap, num, den); err != nil {
				t.Fatalf("op %d: Add: %v", op, err)
			}
			ref.add(id, pcm, offset, chMap, num, den)
			ids = append(ids, id)
			noteRange(offset, length)
		}

		// Full comparison over the touched range (plus margin) on all channels.
		if !seen {
			continue
		}
		for ch := 0; ch < channels; ch++ {
			for pos := lo - 2; pos < hi+2; pos++ {
				got := m.Sample(ch, pos)
				want := ref.sample(ch, pos)
				if got != want {
					t.Fatalf("op %d: ch %d pos %d: got %d, want %d", op, ch, pos, got, want)
				}
			}
		}
	}
}

func TestAddValidation(t *testing.T) {
	m := mustNew(t, 2, -100, 100)
	if _, err := m.Add("x", [][]int64{{1}}, 0, nil, 1, 0); err == nil {
		t.Error("zero denominator accepted")
	}
	if _, err := m.Add("x", [][]int64{{1}}, 0, []int{2}, 1, 1); err == nil {
		t.Error("out-of-range channel accepted")
	}
	if _, err := m.Add("x", [][]int64{{1}, {1, 2}}, 0, nil, 1, 1); err == nil {
		t.Error("ragged pcm accepted")
	}
	mustAdd(t, m, "x", [][]int64{{1}}, 0, nil, 1, 1)
	if _, err := m.Add("x", [][]int64{{1}}, 0, nil, 1, 1); err == nil {
		t.Error("duplicate id accepted")
	}
}
