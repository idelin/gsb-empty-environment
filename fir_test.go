package fir

import (
	"math/big"
	"strings"
	"testing"
)

// naiveOutput computes y[n] = sum_i coeffs[i] * x[n-i] directly from the
// full sample sequence (zero-padded before index 0). It is the reference
// implementation the filter is checked against.
func naiveOutput(coeffs []*big.Rat, samples []*big.Rat, n int) *big.Rat {
	y := new(big.Rat)
	term := new(big.Rat)
	for i, c := range coeffs {
		j := n - i
		if j < 0 {
			break
		}
		y.Add(y, term.Mul(c, samples[j]))
	}
	return y
}

func rat(num, den int64) *big.Rat { return big.NewRat(num, den) }

func ratCoeffs(pairs ...int64) []Coeff {
	c := make([]Coeff, len(pairs)/2)
	for i := range c {
		c[i] = Coeff{Num: pairs[2*i], Den: pairs[2*i+1]}
	}
	return c
}

func mustRats(t *testing.T, coeffs []Coeff) []*big.Rat {
	t.Helper()
	rats, err := coeffsToRats(coeffs)
	if err != nil {
		t.Fatalf("coeffsToRats: %v", err)
	}
	return rats
}

// samples builds a deterministic pseudo-random rational sample sequence.
func samples(n int) []*big.Rat {
	out := make([]*big.Rat, n)
	state := uint32(12345)
	for i := range out {
		state = state*1664525 + 1013904223
		num := int64(state%2001) - 1000
		state = state*1664525 + 1013904223
		den := int64(state%97) + 1
		out[i] = rat(num, den)
	}
	return out
}

func checkEqual(t *testing.T, got, want []*big.Rat, context string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s: got %d outputs, want %d", context, len(got), len(want))
	}
	for i := range want {
		if got[i].Cmp(want[i]) != 0 {
			t.Fatalf("%s: output %d: got %s, want %s", context, i, got[i].RatString(), want[i].RatString())
		}
	}
}

// TestBlockPartitioning runs the same sample sequence through the filter in
// many different block layouts (including empty blocks) and checks every
// output against the naive convolution.
func TestBlockPartitioning(t *testing.T) {
	coeffs := ratCoeffs(1, 2, -3, 4, 2, 5, 1, 7)
	ref := mustRats(t, coeffs)
	in := samples(200)

	want := make([]*big.Rat, len(in))
	for n := range in {
		want[n] = naiveOutput(ref, in, n)
	}

	layouts := [][]int{
		{200},          // single block
		{1, 1, 1, 197}, // tiny blocks
		{0, 0, 200, 0}, // empty blocks around one big block
		{7, 0, 13, 0, 0, 64, 3, 113},
		{50, 50, 50, 50},
	}
	for _, layout := range layouts {
		f, err := NewFilter(coeffs)
		if err != nil {
			t.Fatalf("NewFilter: %v", err)
		}
		var got []*big.Rat
		off := 0
		for _, size := range layout {
			block := in[off : off+size]
			got = append(got, f.ProcessBlock(block)...)
			off += size
		}
		if off != len(in) {
			t.Fatalf("layout %v covers %d samples, want %d", layout, off, len(in))
		}
		checkEqual(t, got, want, "layout")
	}
}

// TestEmptyBlock verifies that empty blocks are no-ops and leave the state
// untouched.
func TestEmptyBlock(t *testing.T) {
	f, err := NewFilter(ratCoeffs(1, 3, 1, 3, 1, 3))
	if err != nil {
		t.Fatalf("NewFilter: %v", err)
	}
	if out := f.ProcessBlock(nil); len(out) != 0 {
		t.Fatalf("nil block: got %d outputs", len(out))
	}
	if out := f.ProcessBlock([]*big.Rat{}); len(out) != 0 {
		t.Fatalf("empty block: got %d outputs", len(out))
	}
	if f.Pos() != 0 {
		t.Fatalf("empty blocks moved pos to %d", f.Pos())
	}
	// Interleave empty blocks with real samples; must match naive.
	in := samples(10)
	ref := mustRats(t, ratCoeffs(1, 3, 1, 3, 1, 3))
	var want []*big.Rat
	for n := range in {
		want = append(want, naiveOutput(ref, in, n))
	}
	var got []*big.Rat
	for _, x := range in {
		got = append(got, f.ProcessBlock(nil)...)
		got = append(got, f.ProcessBlock([]*big.Rat{x})...)
	}
	checkEqual(t, got, want, "interleaved empty blocks")
}

// TestCheckpointResume pauses mid-stream, exports, restores, and continues;
// the resumed outputs must equal the uninterrupted run sample by sample.
func TestCheckpointResume(t *testing.T) {
	coeffs := ratCoeffs(3, 7, -2, 9, 5, 11, 1, 13)
	ref := mustRats(t, coeffs)
	in := samples(300)

	want := make([]*big.Rat, len(in))
	for n := range in {
		want[n] = naiveOutput(ref, in, n)
	}

	for _, pauseAt := range []int{0, 1, 5, 137, 299} {
		f, err := NewFilter(coeffs)
		if err != nil {
			t.Fatalf("NewFilter: %v", err)
		}
		got := f.ProcessBlock(in[:pauseAt])

		data, err := f.ExportBytes()
		if err != nil {
			t.Fatalf("ExportBytes: %v", err)
		}
		g, err := RestoreBytes(data)
		if err != nil {
			t.Fatalf("RestoreBytes: %v", err)
		}
		if g.Pos() != uint64(pauseAt) {
			t.Fatalf("restored pos %d, want %d", g.Pos(), pauseAt)
		}
		got = append(got, g.ProcessBlock(in[pauseAt:])...)
		checkEqual(t, got, want, "resume")
	}
}

// TestCoeffSwitch checks that a scheduled coefficient switch takes effect
// exactly at the requested sample boundary, with old history still
// participating in the new convolution.
func TestCoeffSwitch(t *testing.T) {
	coeffsA := ratCoeffs(1, 2, 1, 3)                      // length 2
	coeffsB := ratCoeffs(2, 5, -1, 7, 3, 11, 1, 13, 4, 3) // length 5, longer: needs old history
	refA := mustRats(t, coeffsA)
	refB := mustRats(t, coeffsB)
	in := samples(120)
	const boundary = 40

	want := make([]*big.Rat, len(in))
	for n := range in {
		if n < boundary {
			want[n] = naiveOutput(refA, in, n)
		} else {
			want[n] = naiveOutput(refB, in, n)
		}
	}

	f, err := NewFilter(coeffsA)
	if err != nil {
		t.Fatalf("NewFilter: %v", err)
	}
	if err := f.SetCoeffsAt(coeffsB, boundary); err != nil {
		t.Fatalf("SetCoeffsAt: %v", err)
	}
	// Process in odd-sized blocks with empties so the boundary falls
	// mid-stream, not aligned to any block edge.
	var got []*big.Rat
	for off := 0; off < len(in); {
		size := 9
		if off+size > len(in) {
			size = len(in) - off
		}
		got = append(got, f.ProcessBlock(nil)...)
		got = append(got, f.ProcessBlock(in[off:off+size])...)
		off += size
	}
	checkEqual(t, got, want, "coefficient switch")
}

// TestCoeffSwitchAcrossCheckpoint schedules a switch, checkpoints before
// the boundary, and verifies the switch still fires after restore.
func TestCoeffSwitchAcrossCheckpoint(t *testing.T) {
	coeffsA := ratCoeffs(1, 1)
	coeffsB := ratCoeffs(1, 2, 1, 4, 1, 8)
	refA := mustRats(t, coeffsA)
	refB := mustRats(t, coeffsB)
	in := samples(60)
	const boundary = 30

	want := make([]*big.Rat, len(in))
	for n := range in {
		if n < boundary {
			want[n] = naiveOutput(refA, in, n)
		} else {
			want[n] = naiveOutput(refB, in, n)
		}
	}

	f, err := NewFilter(coeffsA)
	if err != nil {
		t.Fatalf("NewFilter: %v", err)
	}
	if err := f.SetCoeffsAt(coeffsB, boundary); err != nil {
		t.Fatalf("SetCoeffsAt: %v", err)
	}
	got := f.ProcessBlock(in[:10])
	data, err := f.ExportBytes()
	if err != nil {
		t.Fatalf("ExportBytes: %v", err)
	}
	g, err := RestoreBytes(data)
	if err != nil {
		t.Fatalf("RestoreBytes: %v", err)
	}
	got = append(got, g.ProcessBlock(in[10:])...)
	checkEqual(t, got, want, "switch across checkpoint")
}

// TestSetCoeffsAtValidation covers scheduling errors and immediate switch.
func TestSetCoeffsAtValidation(t *testing.T) {
	f, err := NewFilter(ratCoeffs(1, 1))
	if err != nil {
		t.Fatalf("NewFilter: %v", err)
	}
	f.ProcessBlock(samples(10))
	if err := f.SetCoeffsAt(ratCoeffs(1, 2), 5); err == nil {
		t.Fatal("expected error for switch in the past")
	}
	if err := f.SetCoeffsAt(ratCoeffs(1, 0), 10); err == nil {
		t.Fatal("expected error for zero denominator")
	}
	if err := f.SetCoeffsAt(nil, 10); err == nil {
		t.Fatal("expected error for empty coefficients")
	}
	// Immediate switch at the current position: y = 2*x from now on.
	in := samples(5)
	if err := f.SetCoeffsAt(ratCoeffs(2, 1), f.Pos()); err != nil {
		t.Fatalf("SetCoeffsAt: %v", err)
	}
	got := f.ProcessBlock(in)
	for i, y := range got {
		want := rat(0, 1).Mul(rat(2, 1), in[i])
		if y.Cmp(want) != 0 {
			t.Fatalf("immediate switch: output %d: got %s, want %s", i, y.RatString(), want.RatString())
		}
	}
}

// TestCheckpointVersionError ensures restoring a checkpoint with an
// unsupported version fails.
func TestCheckpointVersionError(t *testing.T) {
	f, err := NewFilter(ratCoeffs(1, 2, 1, 3))
	if err != nil {
		t.Fatalf("NewFilter: %v", err)
	}
	f.ProcessBlock(samples(8))

	cp := f.Export()
	cp.Version = CheckpointVersion + 1
	if _, err := Restore(cp); err == nil {
		t.Fatal("expected version error")
	} else if !strings.Contains(err.Error(), ErrCheckpointVersion.Error()) {
		t.Fatalf("error %q does not mention %q", err, ErrCheckpointVersion)
	}

	// Same check through the serialized form.
	data, err := f.ExportBytes()
	if err != nil {
		t.Fatalf("ExportBytes: %v", err)
	}
	tampered := strings.Replace(string(data), `"version":1`, `"version":99`, 1)
	if tampered == string(data) {
		t.Fatal("failed to tamper with serialized checkpoint")
	}
	if _, err := RestoreBytes([]byte(tampered)); err == nil {
		t.Fatal("expected version error from RestoreBytes")
	} else if !strings.Contains(err.Error(), ErrCheckpointVersion.Error()) {
		t.Fatalf("error %q does not mention %q", err, ErrCheckpointVersion)
	}
}

// TestCheckpointMalformed covers non-version checkpoint corruption.
func TestCheckpointMalformed(t *testing.T) {
	f, err := NewFilter(ratCoeffs(1, 2, 1, 3))
	if err != nil {
		t.Fatalf("NewFilter: %v", err)
	}
	f.ProcessBlock(samples(4))
	good := f.Export()

	bad := *good
	bad.Coeffs = nil
	if _, err := Restore(&bad); err == nil {
		t.Fatal("expected error for empty coefficients")
	}

	bad = *good
	bad.Coeffs = []string{"not-a-rational"}
	if _, err := Restore(&bad); err == nil {
		t.Fatal("expected error for invalid coefficient")
	}

	bad = *good
	bad.History = append(append([]string{}, good.History...), "1/2", "3/4", "5/6")
	if _, err := Restore(&bad); err == nil {
		t.Fatal("expected error for overlong history")
	}

	if _, err := RestoreBytes([]byte("{not json")); err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}
