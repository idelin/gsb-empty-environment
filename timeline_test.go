package resample

import (
	"math/rand"
	"testing"
)

func streamTimeline(tl *Timeline, x []float64, chunks []int) []float64 {
	var out []float64
	pos := 0
	for _, c := range chunks {
		if pos+c > len(x) {
			c = len(x) - pos
		}
		out = append(out, tl.Write(x[pos:pos+c])...)
		pos += c
	}
	out = append(out, tl.Write(x[pos:])...)
	out = append(out, tl.Flush()...)
	return out
}

// Streaming across a rate switch must reproduce the one-shot absolute
// rational time-axis model bit-for-bit, for arbitrary chunkings.
func TestSwitchMatchesOneShot(t *testing.T) {
	rng := rand.New(rand.NewSource(99))
	outRates := []Rate{{1, 1}, {3, 2}, {2, 3}}
	segSets := [][]InputSegment{
		{{FromIndex: 0, Rate: Rate{1, 1}}},
		{{FromIndex: 0, Rate: Rate{1, 1}}, {FromIndex: 10, Rate: Rate{2, 1}}},
		{{FromIndex: 0, Rate: Rate{2, 1}}, {FromIndex: 7, Rate: Rate{1, 2}}},
		{{FromIndex: 0, Rate: Rate{3, 2}}, {FromIndex: 1, Rate: Rate{1, 3}}, {FromIndex: 40, Rate: Rate{5, 2}}},
		{{FromIndex: 0, Rate: Rate{1, 1}}, {FromIndex: 59, Rate: Rate{4, 1}}},
	}
	for _, outRate := range outRates {
		for _, segs := range segSets {
			for _, n := range []int{1, 2, 13, 60, 200} {
				x := randSamples(rng, n)
				want := ResampleTimeline(x, outRate, segs...)
				for trial := 0; trial < 4; trial++ {
					tl := NewTimeline(outRate, segs...)
					got := streamTimeline(tl, x, randomChunks(rng, n))
					if !equalFloats(got, want) {
						t.Fatalf("outRate=%v segs=%v n=%d trial=%d: got %d samples, want %d",
							outRate, segs, n, trial, len(got), len(want))
					}
				}
			}
		}
	}
}

// Phase must pass through the switch continuously in absolute time: the
// output grid is the same absolute rational grid on both sides of the
// switch, and an output straddling the switch interpolates between samples
// from both segments.
func TestSwitchPhaseContinuity(t *testing.T) {
	// Input: rate 1 up to index 4, rate 2 from index 4 on.
	// Input times: 0,1,2,3,4, 4.5,5,5.5,...,8 (idx 12); virtual end 8.5.
	// Output rate 3/4 (step 4/3): outputs at 0, 4/3, 8/3, 4, 16/3, 20/3, 8.
	// t=4 is exactly the switch sample; t=16/3 and t=20/3 straddle the
	// sparser post-switch grid; t=8 lands exactly on the last sample.
	segs := []InputSegment{{FromIndex: 0, Rate: Rate{1, 1}}, {FromIndex: 4, Rate: Rate{2, 1}}}
	x := []float64{0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12}
	got := ResampleTimeline(x, Rate{3, 4}, segs...)
	// x[i]=i, so values by linear interpolation on the absolute axis:
	// 16/3 lies between t=5 (x=6) and t=5.5 (x=7): 6 + (1/3)/(1/2) = 6+2/3;
	// 20/3 lies between t=6.5 (x=9) and t=7 (x=10): 9 + (1/6)/(1/2) = 9+1/3.
	want := []float64{0, 4.0 / 3, 8.0 / 3, 4, 6 + 2.0/3, 9 + 1.0/3, 12}
	if !equalFloats(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	// The streamed version with a chunk boundary exactly at the switch
	// must agree bit-for-bit.
	tl := NewTimeline(Rate{3, 4}, segs...)
	streamed := append(tl.Write(x[:4]), tl.Write(x[4:])...)
	streamed = append(streamed, tl.Flush()...)
	if !equalFloats(streamed, want) {
		t.Fatalf("streamed: got %v, want %v", streamed, want)
	}
}

func TestTimelineEmpty(t *testing.T) {
	tl := NewTimeline(Rate{1, 1}, InputSegment{FromIndex: 0, Rate: Rate{1, 1}})
	if out := tl.Flush(); out != nil {
		t.Fatalf("flush with no input: got %v, want nil", out)
	}
	if got := ResampleTimeline(nil, Rate{1, 1}, InputSegment{FromIndex: 0, Rate: Rate{1, 1}}); got != nil {
		t.Fatalf("one-shot empty: got %v, want nil", got)
	}
}

// Timeline buffering must stay O(1) across long streams and rate switches.
func TestTimelineBoundedBuffer(t *testing.T) {
	rng := rand.New(rand.NewSource(3))
	segs := []InputSegment{
		{FromIndex: 0, Rate: Rate{1, 1}},
		{FromIndex: 50000, Rate: Rate{3, 2}},
	}
	tl := NewTimeline(Rate{2, 3}, segs...)
	for i := 0; i < 100000; i++ {
		tl.Write([]float64{rng.NormFloat64()})
		if b := tl.Buffered(); b > 2 {
			t.Fatalf("buffered %d samples after %d writes", b, i+1)
		}
	}
	tl.Flush()
}

// makeFragments covers [0,n) with random overlapping fragments, then
// duplicates some of them and shuffles the whole delivery order.
func makeFragments(rng *rand.Rand, x []float64) []Fragment {
	n := len(x)
	var frags []Fragment
	pos := 0
	for pos < n {
		start := pos - rng.Intn(3) // overlap with the previous fragment
		if start < 0 {
			start = 0
		}
		length := 1 + rng.Intn(9)
		if start+length > n {
			length = n - start
		}
		samples := make([]float64, length)
		copy(samples, x[start:start+length])
		frags = append(frags, Fragment{Start: int64(start), Samples: samples})
		pos = start + length
	}
	// Duplicate some fragments.
	extra := len(frags)
	for i := 0; i < extra; i++ {
		if rng.Intn(2) == 0 {
			frags = append(frags, frags[i])
		}
	}
	rng.Shuffle(len(frags), func(i, j int) { frags[i], frags[j] = frags[j], frags[i] })
	return frags
}

// feedFragments pushes fragments through the sequencer into the timeline
// and returns the full resampled output.
func feedFragments(seq *Sequencer, tl *Timeline, frags []Fragment) []float64 {
	var out []float64
	for _, f := range frags {
		ready := seq.Add(f)
		out = append(out, tl.Write(ready)...)
	}
	out = append(out, tl.Flush()...)
	return out
}

// Out-of-order, duplicated fragments must resample to exactly the same
// output as the in-order stream; only the confirmed prefix is published.
func TestFragmentsEndToEnd(t *testing.T) {
	rng := rand.New(rand.NewSource(1234))
	segs := []InputSegment{
		{FromIndex: 0, Rate: Rate{1, 1}},
		{FromIndex: 30, Rate: Rate{2, 1}},
		{FromIndex: 75, Rate: Rate{1, 2}},
	}
	outRate := Rate{3, 2}
	for _, n := range []int{1, 10, 100, 333} {
		x := randSamples(rng, n)
		want := ResampleTimeline(x, outRate, segs...)
		for trial := 0; trial < 4; trial++ {
			frags := makeFragments(rng, x)
			seq := NewSequencer()
			tl := NewTimeline(outRate, segs...)
			got := feedFragments(seq, tl, frags)
			if !equalFloats(got, want) {
				t.Fatalf("n=%d trial=%d: got %d samples, want %d", n, trial, len(got), len(want))
			}
			if seq.Next() != int64(n) || seq.Pending() != 0 {
				t.Fatalf("n=%d trial=%d: sequencer next=%d pending=%d", n, trial, seq.Next(), seq.Pending())
			}
		}
	}
}

// A gap must never be zero-filled: nothing past the gap is published until
// the missing fragment arrives.
func TestGapNeverZeroFilled(t *testing.T) {
	x := []float64{1, 2, 3, 4, 5, 6, 7, 8}
	seq := NewSequencer()
	// Deliver the tail first: a gap at [0,4) must block everything.
	if ready := seq.Add(Fragment{Start: 4, Samples: x[4:]}); len(ready) != 0 {
		t.Fatalf("gap was filled: released %v", ready)
	}
	if seq.Pending() != 4 {
		t.Fatalf("pending = %d, want 4", seq.Pending())
	}
	// Duplicates behind the gap change nothing.
	if ready := seq.Add(Fragment{Start: 6, Samples: x[6:]}); len(ready) != 0 {
		t.Fatalf("duplicate released across gap: %v", ready)
	}
	// Close the gap in two pieces, out of order.
	if ready := seq.Add(Fragment{Start: 2, Samples: x[2:4]}); len(ready) != 0 {
		t.Fatalf("released before gap closed: %v", ready)
	}
	ready := seq.Add(Fragment{Start: 0, Samples: x[0:2]})
	if !equalFloats(ready, x) {
		t.Fatalf("released %v, want %v", ready, x)
	}
	if seq.Pending() != 0 {
		t.Fatalf("pending = %d, want 0", seq.Pending())
	}
}

// In-order delivery must not accumulate pending samples: memory stays
// bounded by the reordering window, not the stream length.
func TestSequencerInOrderNoGrowth(t *testing.T) {
	rng := rand.New(rand.NewSource(5))
	seq := NewSequencer()
	for i := 0; i < 10000; i++ {
		seq.Add(Fragment{Start: int64(i), Samples: []float64{rng.NormFloat64()}})
		if seq.Pending() != 0 {
			t.Fatalf("pending grew to %d at fragment %d", seq.Pending(), i)
		}
	}
}

// Checkpointing mid-stream and restoring into fresh instances must produce
// output identical to the uninterrupted run.
func TestCheckpointRestore(t *testing.T) {
	rng := rand.New(rand.NewSource(2024))
	segs := []InputSegment{
		{FromIndex: 0, Rate: Rate{1, 1}},
		{FromIndex: 25, Rate: Rate{2, 1}},
		{FromIndex: 60, Rate: Rate{1, 3}},
	}
	outRate := Rate{4, 3}
	x := randSamples(rng, 120)
	frags := makeFragments(rng, x)

	// Baseline: uninterrupted run.
	want := feedFragments(NewSequencer(), NewTimeline(outRate, segs...), frags)

	// Cut the delivery at a fragment boundary, checkpoint, restore into
	// fresh instances, then continue with the remaining fragments.
	for _, cut := range []int{0, 1, len(frags) / 3, len(frags) / 2, len(frags) - 1, len(frags)} {
		seq := NewSequencer()
		tl := NewTimeline(outRate, segs...)
		var head []float64
		for _, f := range frags[:cut] {
			head = append(head, tl.Write(seq.Add(f))...)
		}
		seqState := seq.Checkpoint()
		tlState := tl.Checkpoint()

		// Continue the original instances: sanity check.
		var tail []float64
		for _, f := range frags[cut:] {
			tail = append(tail, tl.Write(seq.Add(f))...)
		}
		tail = append(tail, tl.Flush()...)
		got := append(append([]float64{}, head...), tail...)
		if !equalFloats(got, want) {
			t.Fatalf("cut=%d: continued run differs from baseline", cut)
		}

		// Restore into fresh instances and continue with the remaining
		// fragments: the already emitted head plus the restored tail must
		// match the baseline.
		seq2 := RestoreSequencer(seqState)
		tl2 := RestoreTimeline(tlState)
		var tail2 []float64
		for _, f := range frags[cut:] {
			tail2 = append(tail2, tl2.Write(seq2.Add(f))...)
		}
		tail2 = append(tail2, tl2.Flush()...)
		got2 := append(append([]float64{}, head...), tail2...)
		if !equalFloats(got2, want) {
			t.Fatalf("cut=%d: restored run differs from baseline", cut)
		}
	}
}

// A checkpoint taken across a rate switch must preserve the absolute-time
// output phase exactly.
func TestCheckpointAcrossSwitch(t *testing.T) {
	rng := rand.New(rand.NewSource(77))
	segs := []InputSegment{
		{FromIndex: 0, Rate: Rate{2, 1}},
		{FromIndex: 10, Rate: Rate{1, 2}},
	}
	outRate := Rate{5, 2}
	x := randSamples(rng, 40)
	want := streamTimeline(NewTimeline(outRate, segs...), x, nil)
	for cut := 0; cut <= len(x); cut++ {
		tl := NewTimeline(outRate, segs...)
		head := tl.Write(x[:cut])
		st := tl.Checkpoint()
		tl2 := RestoreTimeline(st)
		got := append(head, tl2.Write(x[cut:])...)
		got = append(got, tl2.Flush()...)
		if !equalFloats(got, want) {
			t.Fatalf("cut=%d: got %d samples, want %d", cut, len(got), len(want))
		}
	}
}
