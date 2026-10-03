package cabac

import (
	"math/rand"
	"testing"
)

// roundTrip encodes bins with the given models and checks the decoder
// reproduces them exactly.
func roundTrip(t *testing.T, bins []uint32, models []Model) {
	t.Helper()
	enc := NewEncoder()
	for i, b := range bins {
		enc.EncodeBin(models[i], b)
	}
	stream := enc.Flush()

	// Mirror the encoder's model sharing: identical Model instances on the
	// encode side must map to one shared fresh instance on the decode side.
	clones := make(map[Model]Model)
	decModels := make([]Model, len(models))
	for i, m := range models {
		if _, ok := clones[m]; !ok {
			switch mm := m.(type) {
			case *Context:
				clones[m] = NewContext()
			case *StaticModel:
				clones[m] = NewStaticModel(mm.MPS(), float64(mm.ProbLPS())/65536.0)
			}
		}
		decModels[i] = clones[m]
	}
	dec := NewDecoder(stream)
	for i, want := range bins {
		if got := dec.DecodeBin(decModels[i]); got != want {
			t.Fatalf("bin %d: got %d, want %d (stream %d bytes)", i, got, want, len(stream))
		}
	}
}

func TestRoundTripRandomBins(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	for _, n := range []int{1, 2, 7, 33, 256, 4096, 100000} {
		bins := make([]uint32, n)
		models := make([]Model, n)
		set := NewContextSet(4)
		for i := range bins {
			bins[i] = uint32(rng.Intn(2))
			models[i] = set.At(rng.Intn(4))
		}
		roundTrip(t, bins, models)
	}
}

func TestRoundTripSkewedBins(t *testing.T) {
	rng := rand.New(rand.NewSource(2))
	// Per-context bias: each context sees a differently skewed source.
	for _, n := range []int{1000, 50000} {
		bins := make([]uint32, n)
		models := make([]Model, n)
		set := NewContextSet(3)
		bias := []float64{0.01, 0.3, 0.9}
		for i := range bins {
			c := rng.Intn(3)
			if rng.Float64() < bias[c] {
				bins[i] = 1
			}
			models[i] = set.At(c)
		}
		roundTrip(t, bins, models)
	}
}

func TestRoundTripStaticModel(t *testing.T) {
	rng := rand.New(rand.NewSource(3))
	m := NewStaticModel(0, 0.25)
	bins := make([]uint32, 20000)
	models := make([]Model, len(bins))
	for i := range bins {
		if rng.Float64() < 0.25 {
			bins[i] = 1
		}
		models[i] = m
	}
	roundTrip(t, bins, models)
}

// Acceptance (kernel level): a sustained run of one symbol must cost far
// less than 0.2 bits per bin once the context has adapted.
func TestConstantSymbolCostConverges(t *testing.T) {
	const n = 200000
	ctx := NewContext()
	enc := NewEncoder()
	marks := []int{n / 2, n - 1}
	bitsAt := make(map[int]uint64)
	for i := 0; i < n; i++ {
		enc.EncodeBin(ctx, 0)
		for _, m := range marks {
			if i == m {
				bitsAt[m] = enc.Bits()
			}
		}
	}
	enc.Flush()
	tail := float64(bitsAt[n-1]-bitsAt[n/2]) / float64(n-1-n/2)
	t.Logf("steady-state cost: %.4f bits/bin (state %d)", tail, ctx.State())
	if tail > 0.2 {
		t.Fatalf("steady-state cost %.4f bits/bin exceeds 0.2 bits", tail)
	}
	if tail > 0.05 {
		t.Fatalf("expected deep convergence below 0.05 bits/bin, got %.4f", tail)
	}
}

func TestContextAdaptation(t *testing.T) {
	c := NewContext()
	if c.State() != 0 {
		t.Fatalf("initial state = %d, want 0 (max uncertainty)", c.State())
	}
	// MPS run climbs the state table and saturates.
	for i := 0; i < 100; i++ {
		c.Update(0)
	}
	if c.State() < 62 {
		t.Fatalf("state after 100 MPS = %d, want >= 62", c.State())
	}
	if c.MPS() != 0 {
		t.Fatalf("MPS flipped unexpectedly to %d", c.MPS())
	}
	// A single LPS drops the state sharply.
	before := c.State()
	c.Update(1)
	if c.State() >= before {
		t.Fatalf("LPS did not drop state: %d -> %d", before, c.State())
	}
	// LPS at maximal uncertainty swaps the MPS.
	c2 := NewContext()
	c2.Update(1)
	if c2.MPS() != 1 {
		t.Fatalf("MPS did not swap after LPS at state 0")
	}
}

func TestStateTables(t *testing.T) {
	for s := 1; s < NumStates; s++ {
		if probLPS16[s] > probLPS16[s-1] {
			t.Fatalf("probLPS16 not non-increasing at %d", s)
		}
	}
	if probLPS16[0] != 32768 {
		t.Fatalf("probLPS16[0] = %d, want 32768 (p=0.5)", probLPS16[0])
	}
	if got := probLPS16[NumStates-1]; got < 1200 || got > 1300 {
		t.Fatalf("probLPS16[63] = %d, want ~1229 (H.264 floor 0.01875)", got)
	}
	for s := 0; s < NumStates; s++ {
		if int(nextStateLPS[s]) >= NumStates || int(nextStateMPS[s]) >= NumStates {
			t.Fatalf("transition out of range at state %d", s)
		}
	}
}

func TestStaticModelIsStatic(t *testing.T) {
	m := NewStaticModel(1, 0.1)
	p0 := m.ProbLPS()
	for i := 0; i < 1000; i++ {
		m.Update(uint32(i % 2))
	}
	if m.ProbLPS() != p0 {
		t.Fatalf("static model probability changed: %d -> %d", p0, m.ProbLPS())
	}
	if m.MPS() != 1 {
		t.Fatalf("static model MPS changed")
	}
}

// The adaptive kernel must compress a locally-structured stream strictly
// better than the static model, using only the kernel primitives.
func TestAdaptiveBeatsStatic(t *testing.T) {
	rng := rand.New(rand.NewSource(4))
	var bins []uint32
	// Structured: long constant runs (locally predictable) with rare flips.
	for len(bins) < 100000 {
		run := 50 + rng.Intn(400)
		v := uint32(rng.Intn(2))
		for i := 0; i < run; i++ {
			bins = append(bins, v)
		}
	}
	var ones int
	for _, b := range bins {
		ones += int(b)
	}

	ctx := NewContext()
	encA := NewEncoder()
	for _, b := range bins {
		encA.EncodeBin(ctx, b)
	}
	adaptive := len(encA.Flush())

	p := float64(ones) / float64(len(bins))
	mps := uint32(0)
	if p > 0.5 {
		mps, p = 1, 1-p
	}
	sm := NewStaticModel(mps, p)
	encS := NewEncoder()
	for _, b := range bins {
		encS.EncodeBin(sm, b)
	}
	static := len(encS.Flush())

	t.Logf("adaptive=%dB static=%dB", adaptive, static)
	if adaptive >= static {
		t.Fatalf("adaptive (%dB) not smaller than static (%dB)", adaptive, static)
	}
}
