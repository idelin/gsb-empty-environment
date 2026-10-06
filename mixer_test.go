package pcmix

import (
	"math/big"
	"math/rand"
	"testing"
)

// naiveMixer is an independent reference implementation: it keeps the
// fragment list and recomputes every requested frame from scratch with
// big.Rat sums, rounding via Euclidean division.
type naiveMixer struct {
	channels int
	min, max int64
	frags    map[string]*fragment
}

func newNaive(channels int, bitDepth uint) *naiveMixer {
	return &naiveMixer{
		channels: channels,
		min:      -int64(1) << (bitDepth - 1),
		max:      int64(1)<<(bitDepth-1) - 1,
		frags:    make(map[string]*fragment),
	}
}

func (n *naiveMixer) add(id string, offset int64, channelMap []int, gain Gain, samples []int32) {
	g, _ := gain.normalized()
	n.frags[id] = &fragment{offset, append([]int(nil), channelMap...), g, append([]int32(nil), samples...)}
}

func (n *naiveMixer) remove(id string) { delete(n.frags, id) }

func (n *naiveMixer) render(start, end int64) [][]int32 {
	out := make([][]int32, n.channels)
	for ch := 0; ch < n.channels; ch++ {
		row := make([]int32, end-start)
		for frame := start; frame < end; frame++ {
			sum := new(big.Rat)
			for _, f := range n.frags {
				nch := int64(len(f.channelMap))
				idx := frame - f.offset
				if idx < 0 || idx >= f.frames() {
					continue
				}
				for c := int64(0); c < nch; c++ {
					if f.channelMap[c] != ch {
						continue
					}
					s := f.samples[idx*nch+c]
					term := big.NewRat(int64(s)*f.gain.Num, f.gain.Den)
					sum.Add(sum, term)
				}
			}
			row[frame-start] = n.saturate(naiveRound(sum))
		}
		out[ch] = row
	}
	return out
}

// naiveRound rounds half to even using floor division only.
func naiveRound(r *big.Rat) *big.Int {
	num := new(big.Int).Set(r.Num())
	den := r.Denom()
	q, rem := new(big.Int), new(big.Int)
	q.DivMod(num, den, rem) // Euclidean: 0 <= rem < den
	twice := new(big.Int).Lsh(rem, 1)
	switch twice.Cmp(den) {
	case 1:
		q.Add(q, big.NewInt(1))
	case 0:
		if q.Bit(0) == 1 {
			q.Add(q, big.NewInt(1))
		}
	}
	return q
}

func (n *naiveMixer) saturate(v *big.Int) int32 {
	if !v.IsInt64() {
		if v.Sign() < 0 {
			return int32(n.min)
		}
		return int32(n.max)
	}
	x := v.Int64()
	if x < n.min {
		return int32(n.min)
	}
	if x > n.max {
		return int32(n.max)
	}
	return int32(x)
}

func eq(t *testing.T, got, want [][]int32) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("channel count %d != %d", len(got), len(want))
	}
	for ch := range got {
		if len(got[ch]) != len(want[ch]) {
			t.Fatalf("ch %d: len %d != %d", ch, len(got[ch]), len(want[ch]))
		}
		for i := range got[ch] {
			if got[ch][i] != want[ch][i] {
				t.Fatalf("ch %d frame %d: got %d want %d", ch, i, got[ch][i], want[ch][i])
			}
		}
	}
}

func TestRoundHalfEven(t *testing.T) {
	cases := []struct {
		num, den int64
		want     int64
	}{
		{1, 2, 0}, {3, 2, 2}, {5, 2, 2}, {7, 2, 4}, // ties to even
		{-1, 2, 0}, {-3, 2, -2}, {-5, 2, -2}, {-7, 2, -4},
		{1, 3, 0}, {2, 3, 1}, {-2, 3, -1}, {4, 3, 1}, {5, 3, 2},
		{0, 7, 0}, {10, 1, 10}, {-10, 1, -10},
	}
	for _, c := range cases {
		got := roundHalfEven(big.NewRat(c.num, c.den))
		if got.Cmp(big.NewInt(c.want)) != 0 {
			t.Errorf("roundHalfEven(%d/%d) = %v, want %d", c.num, c.den, got, c.want)
		}
	}
}

func TestSaturateOnceNotPerTrack(t *testing.T) {
	// Two fragments that each clip individually but cancel in the mix:
	// 40000 + (-40000) must render 0, not clamp-then-sum.
	m, err := New(1, 16)
	if err != nil {
		t.Fatal(err)
	}
	if err := m.Add("a", 0, []int{0}, Gain{1, 1}, []int32{40000}); err != nil {
		t.Fatal(err)
	}
	if err := m.Add("b", 0, []int{0}, Gain{-1, 1}, []int32{40000}); err != nil {
		t.Fatal(err)
	}
	got := m.Render(0, 1)
	if got[0][0] != 0 {
		t.Fatalf("got %d, want 0 (no per-track saturation)", got[0][0])
	}
	// And a genuinely clipping sum saturates once at the boundary.
	if err := m.Add("c", 1, []int{0}, Gain{1, 1}, []int32{30000, -30000}); err != nil {
		t.Fatal(err)
	}
	if err := m.Add("d", 1, []int{0}, Gain{1, 1}, []int32{30000, -30000}); err != nil {
		t.Fatal(err)
	}
	got = m.Render(1, 3)
	if got[0][0] != 32767 || got[0][1] != -32768 {
		t.Fatalf("got %v, want [32767 -32768]", got[0])
	}
}

func TestRemoveAffectedRange(t *testing.T) {
	m, err := New(2, 16)
	if err != nil {
		t.Fatal(err)
	}
	if err := m.Add("x", -5, []int{0, 1}, Gain{1, 2}, []int32{10, 20, 30, 40, 50, 60}); err != nil {
		t.Fatal(err)
	}
	start, end, ok := m.Remove("x")
	if !ok || start != -5 || end != -2 {
		t.Fatalf("Remove range = (%d,%d,%v), want (-5,-2,true)", start, end, ok)
	}
	after := m.Render(-8, 4)
	for ch := range after {
		for i, v := range after[ch] {
			if v != 0 {
				t.Fatalf("ch %d frame %d = %d after remove, want 0", ch, int64(i)-8, v)
			}
		}
	}
	if _, _, ok := m.Remove("x"); ok {
		t.Fatal("second Remove should report ok=false")
	}
	if _, _, ok := m.Range(); ok {
		t.Fatal("Range on empty mixer should be ok=false")
	}
}

func TestReplaceSameID(t *testing.T) {
	m, err := New(1, 16)
	if err != nil {
		t.Fatal(err)
	}
	if err := m.Add("a", 0, []int{0}, Gain{1, 1}, []int32{100}); err != nil {
		t.Fatal(err)
	}
	if err := m.Add("a", 0, []int{0}, Gain{1, 1}, []int32{7}); err != nil {
		t.Fatal(err)
	}
	if got := m.Render(0, 1)[0][0]; got != 7 {
		t.Fatalf("replace: got %d, want 7", got)
	}
}

func TestErrors(t *testing.T) {
	m, err := New(2, 16)
	if err != nil {
		t.Fatal(err)
	}
	if err := m.Add("a", 0, nil, Gain{1, 1}, nil); err == nil {
		t.Error("empty channel map accepted")
	}
	if err := m.Add("a", 0, []int{2}, Gain{1, 1}, []int32{1}); err == nil {
		t.Error("out-of-range channel accepted")
	}
	if err := m.Add("a", 0, []int{0}, Gain{1, 0}, []int32{1}); err == nil {
		t.Error("zero denominator accepted")
	}
	if err := m.Add("a", 0, []int{0, 1}, Gain{1, 1}, []int32{1, 2, 3}); err == nil {
		t.Error("misaligned sample count accepted")
	}
	if _, err := New(0, 16); err == nil {
		t.Error("zero channels accepted")
	}
	if _, err := New(1, 1); err == nil {
		t.Error("bitDepth 1 accepted")
	}
}

// TestAgainstNaive cross-checks the incremental mixer against the naive
// reference over randomized overlapping fragments, negative offsets and
// gains, boundary values, and undo/re-add sequences.
func TestAgainstNaive(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	for trial := 0; trial < 60; trial++ {
		channels := 1 + rng.Intn(3)
		depth := uint(8 + 8*rng.Intn(3)) // 8, 16 or 24... plus 32 below
		if rng.Intn(4) == 0 {
			depth = 32
		}
		m, err := New(channels, depth)
		if err != nil {
			t.Fatal(err)
		}
		n := newNaive(channels, depth)
		max := int64(1)<<(depth-1) - 1
		min := -int64(1) << (depth - 1)

		var ids []string
		steps := 5 + rng.Intn(10)
		for s := 0; s < steps; s++ {
			if len(ids) > 0 && rng.Intn(4) == 0 { // undo a random fragment
				k := rng.Intn(len(ids))
				id := ids[k]
				ids = append(ids[:k], ids[k+1:]...)
				if _, _, ok := m.Remove(id); !ok {
					t.Fatalf("trial %d: Remove(%q) failed", trial, id)
				}
				n.remove(id)
				continue
			}
			nch := 1 + rng.Intn(channels)
			cmap := make([]int, nch)
			for i := range cmap {
				cmap[i] = rng.Intn(channels)
			}
			frames := 1 + rng.Intn(12)
			samples := make([]int32, frames*nch)
			for i := range samples {
				switch rng.Intn(5) {
				case 0: // force boundary values to hit clipping
					samples[i] = int32(max)
				case 1:
					samples[i] = int32(min)
				default:
					samples[i] = int32(rng.Int63n(max-min+1) + min)
				}
			}
			gain := Gain{Num: rng.Int63n(9) - 4, Den: 1 + rng.Int63n(7)}
			if rng.Intn(3) == 0 {
				gain.Den = -gain.Den // exercise sign normalization
			}
			offset := int64(rng.Intn(21) - 10) // overlapping, negative offsets
			id := string(rune('A' + s))
			if err := m.Add(id, offset, cmap, gain, samples); err != nil {
				t.Fatalf("trial %d: Add: %v", trial, err)
			}
			n.add(id, offset, cmap, gain, samples)
			ids = append(ids, id)
		}

		start, end, ok := m.Range()
		if !ok {
			continue
		}
		// Render with margin on both sides to check zero padding too.
		got := m.Render(start-3, end+3)
		want := n.render(start-3, end+3)
		eq(t, got, want)
	}
}
