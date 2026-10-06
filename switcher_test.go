package resample

import (
	"math/rand"
	"testing"
)

func TestSwitcherMatchesOneShotTimeline(t *testing.T) {
	rng := rand.New(rand.NewSource(3))
	cases := []struct {
		segs     []Segment
		switches []int64
	}{
		{[]Segment{{2, 1}, {3, 4}}, []int64{17}},
		{[]Segment{{1, 2}, {3, 1}, {1, 1}}, []int64{5, 40}},
		{[]Segment{{5, 3}, {2, 5}}, []int64{1}},
		{[]Segment{{1, 1}, {4, 1}}, []int64{200}}, // switch beyond end of stream
	}
	for _, tc := range cases {
		for _, n := range []int{1, 8, 64, 150} {
			samples := make([]float64, n)
			for i := range samples {
				samples[i] = rng.NormFloat64()
			}
			want := oneShot(samples, tc.segs, tc.switches)

			sw, err := NewSwitcher(tc.segs, tc.switches)
			if err != nil {
				t.Fatal(err)
			}
			got := feedChunks(t, sw.Write, sw.Flush, samples, []int{1, 5, 13, 2})
			assertClose(t, got, want)
		}
	}
}

func TestSwitcherPhaseContinuousAcrossSwitch(t *testing.T) {
	// Rate 1 -> rate 2 at input index 4. Absolute output positions must be
	// 0,1,2,3,4, 4.5,5,5.5,... (phase carried by absolute time, not reset).
	// With input x[i]=i the interpolated values equal the positions.
	sw, err := NewSwitcher([]Segment{{1, 1}, {2, 1}}, []int64{4})
	if err != nil {
		t.Fatal(err)
	}
	in := []float64{0, 1, 2, 3, 4, 5, 6}
	got := feedChunks(t, sw.Write, sw.Flush, in, []int{7})
	ref := oneShot(in, []Segment{{1, 1}, {2, 1}}, []int64{4})
	if len(got) != len(ref) {
		t.Fatalf("count: got %d want %d", len(got), len(ref))
	}
	assertClose(t, got, ref)
	// Spot-check the exact switch neighbourhood. The final position 6.5
	// would need sample 7, which does not exist, so the last sample (6)
	// is repeated instead.
	for k, v := range []float64{0, 1, 2, 3, 4, 4.5, 5, 5.5, 6, 6} {
		if got[k] != v {
			t.Fatalf("output %d: got %v want %v", k, got[k], v)
		}
	}
}

func TestSwitcherCheckpointRestore(t *testing.T) {
	rng := rand.New(rand.NewSource(11))
	const n = 120
	samples := make([]float64, n)
	for i := range samples {
		samples[i] = rng.NormFloat64()
	}
	segs := []Segment{{2, 1}, {3, 4}, {5, 2}}
	switches := []int64{30, 77}

	// Uninterrupted run.
	full, _ := NewSwitcher(segs, switches)
	want := feedChunks(t, full.Write, full.Flush, samples, []int{n})

	// Run partway, snapshot, continue.
	a, _ := NewSwitcher(segs, switches)
	var got []float64
	out, err := a.Write(samples[:50])
	if err != nil {
		t.Fatal(err)
	}
	got = append(got, out...)
	checkpoint := a.Snapshot()
	out, err = a.Write(samples[50:])
	if err != nil {
		t.Fatal(err)
	}
	got = append(got, out...)
	tail, err := a.Flush()
	if err != nil {
		t.Fatal(err)
	}
	got = append(got, tail...)
	assertClose(t, got, oneShot(samples, segs, switches))
	if len(got) != len(want) {
		t.Fatalf("checkpoint run diverged: %d vs %d outputs", len(got), len(want))
	}

	// Replay from the checkpoint in a fresh Switcher: identical suffix.
	b, _ := NewSwitcher(segs, switches)
	b.Restore(checkpoint)
	var suffix []float64
	out, err = b.Write(samples[50:])
	if err != nil {
		t.Fatal(err)
	}
	suffix = append(suffix, out...)
	tail, err = b.Flush()
	if err != nil {
		t.Fatal(err)
	}
	suffix = append(suffix, tail...)
	prefixLen := len(got) - len(suffix)
	for k := range suffix {
		if suffix[k] != got[prefixLen+k] {
			t.Fatalf("replay diverged at suffix %d: %v vs %v", k, suffix[k], got[prefixLen+k])
		}
	}
	// The checkpoint is a deep copy: restoring again must still work.
	c, _ := NewSwitcher(segs, switches)
	c.Restore(checkpoint)
	if _, err := c.Write(samples[50:]); err != nil {
		t.Fatal(err)
	}
}

func TestSwitcherEmptyAndBufferBound(t *testing.T) {
	sw, _ := NewSwitcher([]Segment{{3, 2}, {1, 3}}, []int64{10})
	if out, err := sw.Flush(); err != nil || len(out) != 0 {
		t.Fatalf("empty flush: %v %v", out, err)
	}

	sw, _ = NewSwitcher([]Segment{{3, 2}, {1, 3}}, []int64{100000})
	for i := 0; i < 200000; i++ {
		if _, err := sw.Write([]float64{float64(i)}); err != nil {
			t.Fatal(err)
		}
		if b := sw.Buffered(); b > 2 {
			t.Fatalf("buffer grew to %d", b)
		}
	}
	if _, err := sw.Flush(); err != nil {
		t.Fatal(err)
	}
}

func TestSwitcherBadConfig(t *testing.T) {
	if _, err := NewSwitcher(nil, nil); err != ErrBadSegments {
		t.Fatalf("got %v", err)
	}
	if _, err := NewSwitcher([]Segment{{1, 1}}, []int64{3}); err != ErrBadSegments {
		t.Fatalf("got %v", err)
	}
	if _, err := NewSwitcher([]Segment{{1, 1}, {1, 1}}, []int64{5, 5}); err != ErrBadSegments {
		t.Fatalf("got %v", err)
	}
	if _, err := NewSwitcher([]Segment{{1, 0}}, nil); err != ErrNonPositiveRatio {
		t.Fatalf("got %v", err)
	}
}
