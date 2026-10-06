package fir

import (
	"bytes"
	"encoding/gob"
	"math/rand"
	"testing"
)

// naiveReference computes the expected output by direct convolution,
// applying each scheduled switch at its sample boundary. It sums taps in
// the same order as the filter so results are bit-identical.
func naiveReference(input []float64, initial []float64, switches []switchState) []float64 {
	out := make([]float64, len(input))
	coeffs := initial
	for n := range input {
		for _, s := range switches {
			if s.At <= uint64(n) {
				coeffs = s.Coeffs
			}
		}
		y := 0.0
		for k, c := range coeffs {
			if k > n {
				break
			}
			y += c * input[n-k]
		}
		out[n] = y
	}
	return out
}

func testInput(n int) []float64 {
	rng := rand.New(rand.NewSource(42))
	in := make([]float64, n)
	for i := range in {
		in[i] = rng.Float64()*2 - 1
	}
	return in
}

func requireEqual(t *testing.T, got, want []float64) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("length mismatch: got %d samples, want %d", len(got), len(want))
	}
	for i := range got {
		if got[i] != want[i] {
			t.Fatalf("sample %d: got %v, want %v", i, got[i], want[i])
		}
	}
}

func TestProcessBlockMatchesNaive(t *testing.T) {
	coeffs := []float64{0.5, -0.25, 0.125, 0.0625}
	in := testInput(500)
	want := naiveReference(in, coeffs, nil)

	for _, chunk := range []int{1, 2, 3, 7, 64, 500} {
		f, err := New(coeffs)
		if err != nil {
			t.Fatal(err)
		}
		var got []float64
		for i := 0; i < len(in); i += chunk {
			end := i + chunk
			if end > len(in) {
				end = len(in)
			}
			got = append(got, f.ProcessBlock(in[i:end])...)
		}
		requireEqual(t, got, want)
	}
}

func TestEmptyBlocks(t *testing.T) {
	coeffs := []float64{1.0 / 3, 1.0 / 3, 1.0 / 3}
	in := testInput(100)
	want := naiveReference(in, coeffs, nil)

	f, err := New(coeffs)
	if err != nil {
		t.Fatal(err)
	}
	var got []float64
	for i := 0; i < len(in); i += 10 {
		if out := f.ProcessBlock(nil); len(out) != 0 {
			t.Fatalf("empty block produced %d samples", len(out))
		}
		end := i + 10
		if end > len(in) {
			end = len(in)
		}
		got = append(got, f.ProcessBlock(in[i:end])...)
		got = append(got, f.ProcessBlock([]float64{})...)
	}
	requireEqual(t, got, want)
	if f.Count() != uint64(len(in)) {
		t.Fatalf("count: got %d, want %d", f.Count(), len(in))
	}
}

func TestCoefficientSwitchAtBoundary(t *testing.T) {
	initial := []float64{1, 0, 0}
	grown := []float64{0.25, 0.25, 0.25, 0.25, 0.25} // longer: needs old history
	in := testInput(200)
	switches := []switchState{{At: 50, Coeffs: grown}, {At: 120, Coeffs: []float64{2, -1}}}
	want := naiveReference(in, initial, switches)

	f, err := New(initial)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.ScheduleSwitch(120, []float64{2, -1}); err != nil {
		t.Fatal(err)
	}
	if err := f.ScheduleSwitch(50, grown); err != nil { // scheduled out of order
		t.Fatal(err)
	}
	got := f.ProcessBlock(in)
	requireEqual(t, got, want)
}

func TestCheckpointRestoreEqualsContinuous(t *testing.T) {
	coeffs := []float64{0.4, -0.3, 0.2, -0.1}
	in := testInput(300)
	switches := []switchState{{At: 150, Coeffs: []float64{1, 1, 1, 1, 1, 1}}}
	want := naiveReference(in, coeffs, switches)

	// Continuous reference run.
	cont, err := New(coeffs)
	if err != nil {
		t.Fatal(err)
	}
	if err := cont.ScheduleSwitch(150, switches[0].Coeffs); err != nil {
		t.Fatal(err)
	}
	requireEqual(t, cont.ProcessBlock(in), want)

	// Suspended run: export at several points, restore, continue.
	f, err := New(coeffs)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.ScheduleSwitch(150, switches[0].Coeffs); err != nil {
		t.Fatal(err)
	}
	var got []float64
	pos := 0
	for _, stop := range []int{0, 1, 37, 149, 150, 151, 299} {
		got = append(got, f.ProcessBlock(in[pos:stop])...)
		pos = stop
		data, err := f.Export()
		if err != nil {
			t.Fatal(err)
		}
		f, err = Restore(data)
		if err != nil {
			t.Fatal(err)
		}
		if f.Count() != uint64(stop) {
			t.Fatalf("restored count: got %d, want %d", f.Count(), stop)
		}
	}
	got = append(got, f.ProcessBlock(in[pos:])...)
	requireEqual(t, got, want)
}

func TestRestoreVersionMismatch(t *testing.T) {
	f, err := New([]float64{1, 1})
	if err != nil {
		t.Fatal(err)
	}
	f.ProcessBlock([]float64{3, 4, 5})

	// Same layout, unsupported version.
	bad := checkpoint{Version: CheckpointVersion + 1, Coeffs: []float64{1, 1}, Hist: make([]float64, 2)}
	var buf bytes.Buffer
	if err := gob.NewEncoder(&buf).Encode(bad); err != nil {
		t.Fatal(err)
	}
	if _, err := Restore(buf.Bytes()); err != ErrVersionMismatch {
		t.Fatalf("got %v, want ErrVersionMismatch", err)
	}

	// Garbage is not a version error but an invalid checkpoint.
	if _, err := Restore([]byte("not a checkpoint")); err != ErrInvalidCheckpoint {
		t.Fatalf("got %v, want ErrInvalidCheckpoint", err)
	}

	// A well-formed checkpoint from this version restores fine.
	data, err := f.Export()
	if err != nil {
		t.Fatal(err)
	}
	g, err := Restore(data)
	if err != nil {
		t.Fatal(err)
	}
	requireEqual(t, g.ProcessBlock([]float64{6, 7}), f.ProcessBlock([]float64{6, 7}))
}

func TestNewRational(t *testing.T) {
	f, err := NewRational([]int64{1, 1, 1}, []int64{2, 4, 4})
	if err != nil {
		t.Fatal(err)
	}
	in := []float64{1, 2, 3, 4}
	requireEqual(t, f.ProcessBlock(in), naiveReference(in, []float64{0.5, 0.25, 0.25}, nil))

	if _, err := NewRational([]int64{1}, []int64{0}); err == nil {
		t.Fatal("expected error for zero denominator")
	}
	if _, err := NewRational(nil, nil); err != ErrEmptyCoeffs {
		t.Fatalf("got %v, want ErrEmptyCoeffs", err)
	}
	if _, err := New(nil); err != ErrEmptyCoeffs {
		t.Fatalf("got %v, want ErrEmptyCoeffs", err)
	}
	if err := f.ScheduleSwitch(10, nil); err != ErrEmptyCoeffs {
		t.Fatalf("got %v, want ErrEmptyCoeffs", err)
	}
}
