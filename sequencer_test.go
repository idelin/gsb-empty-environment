package resample

import (
	"math/rand"
	"testing"
)

func TestSequencerOutOfOrderAndDuplicates(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	const n = 500
	samples := make([]float64, n)
	for i := range samples {
		samples[i] = rng.NormFloat64()
	}
	// Build random fragments, then deliver them shuffled and duplicated.
	type frag struct {
		start int64
		p     []float64
	}
	var frags []frag
	for off := 0; off < n; {
		l := 1 + rng.Intn(17)
		if l > n-off {
			l = n - off
		}
		frags = append(frags, frag{int64(off), samples[off : off+l]})
		off += l
	}
	rng.Shuffle(len(frags), func(a, b int) { frags[a], frags[b] = frags[b], frags[a] })

	seq := NewSequencer(0)
	var got []float64
	maxBuf := 0
	for _, f := range frags {
		got = append(got, seq.Add(f.start, f.p)...)
		got = append(got, seq.Add(f.start, f.p)...) // exact duplicate delivery
		if b := seq.Buffered(); b > maxBuf {
			maxBuf = b
		}
	}
	if len(got) != n {
		t.Fatalf("got %d samples, want %d", len(got), n)
	}
	for i := range got {
		if got[i] != samples[i] {
			t.Fatalf("sample %d: got %v want %v", i, got[i], samples[i])
		}
	}
	if seq.Buffered() != 0 {
		t.Fatalf("buffer not drained: %d", seq.Buffered())
	}
	t.Logf("peak reorder buffer: %d samples (stream %d)", maxBuf, n)
}

func TestSequencerGapIsNotZeroFilled(t *testing.T) {
	seq := NewSequencer(0)
	if out := seq.Add(0, []float64{1, 2}); len(out) != 2 {
		t.Fatalf("prefix: %v", out)
	}
	// Index 2 missing; index 3 arrives. Nothing may be released for it yet,
	// and certainly not a fabricated zero.
	if out := seq.Add(3, []float64{4}); len(out) != 0 {
		t.Fatalf("gap leaked output: %v", out)
	}
	// Fill the gap: both the gap sample and the held sample come out, in
	// order, with no zeros anywhere.
	out := seq.Add(2, []float64{3})
	if len(out) != 2 || out[0] != 3 || out[1] != 4 {
		t.Fatalf("after gap fill: %v", out)
	}
	// Stale (already confirmed) indices are dropped.
	if out := seq.Add(0, []float64{9, 9, 9, 9}); len(out) != 0 {
		t.Fatalf("stale replay leaked: %v", out)
	}
}

func TestSequencerInOrderKeepsNoBacklog(t *testing.T) {
	seq := NewSequencer(0)
	for i := int64(0); i < 100000; i++ {
		if out := seq.Add(i, []float64{float64(i)}); len(out) != 1 {
			t.Fatalf("in-order delivery blocked at %d", i)
		}
		if b := seq.Buffered(); b != 0 {
			t.Fatalf("in-order stream buffered %d samples", b)
		}
	}
}

// End-to-end: shuffled, duplicated fragments through the Sequencer into the
// Resampler must equal the one-shot rational reference.
func TestSequencerFeedingResampler(t *testing.T) {
	rng := rand.New(rand.NewSource(99))
	const n = 300
	samples := make([]float64, n)
	for i := range samples {
		samples[i] = rng.NormFloat64()
	}
	want := oneShot(samples, []Segment{{3, 2}}, nil)

	r, _ := New(3, 2)
	seq := NewSequencer(0)
	var got []float64
	push := func(start int64, p []float64) {
		confirmed := seq.Add(start, p)
		out, err := r.Write(confirmed)
		if err != nil {
			t.Fatal(err)
		}
		got = append(got, out...)
	}
	// Deliver in 7-sample fragments, reversed order, each sent twice.
	for off := n; off > 0; {
		lo := off - 7
		if lo < 0 {
			lo = 0
		}
		push(int64(lo), samples[lo:off])
		push(int64(lo), samples[lo:off])
		off = lo
	}
	tail, err := r.Flush()
	if err != nil {
		t.Fatal(err)
	}
	got = append(got, tail...)
	assertClose(t, got, want)
}
