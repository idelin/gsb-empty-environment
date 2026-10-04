package wsample_test

import (
	"bytes"
	"errors"
	"math/big"
	"reflect"
	"testing"

	"github.com/example/wsample"
)

func newSampler(t *testing.T, src wsample.Source, weights ...int64) *wsample.Sampler {
	t.Helper()
	s := wsample.New(src)
	for _, w := range weights {
		if _, err := s.Add(big.NewInt(w)); err != nil {
			t.Fatalf("Add(%d): %v", w, err)
		}
	}
	return s
}

func drawWithStream(t *testing.T, weights []int64, k int, stream []byte) []int {
	t.Helper()
	s := wsample.New(wsample.ReaderSource{R: bytes.NewReader(stream)})
	for _, w := range weights {
		if _, err := s.Add(big.NewInt(w)); err != nil {
			t.Fatalf("Add(%d): %v", w, err)
		}
	}
	got, err := s.Draw(k)
	if err != nil {
		t.Fatalf("Draw(%d): %v", k, err)
	}
	return got
}

func sumOf(weights []int64) int64 {
	var total int64
	for _, w := range weights {
		total += w
	}
	return total
}

// TestExactDistribution enumerates the entire random domain for a small
// weight set and checks that the sampler reproduces the exact theoretical
// probabilities of sequential weighted sampling without replacement:
//
//	P(first=i, second=j) = w_i/T * w_j/(T-w_i)
//
// Every accepted uniform value in [0, T) costs exactly one byte here (all
// enumerated values fit under the rejection mask), so each (r1, r2) pair has
// probability 1/(T*(T-w_first)).
func TestExactDistribution(t *testing.T) {
	weights := []int64{1, 2, 3}
	total := sumOf(weights)
	k := 2

	type outcome struct{ first, second int }
	got := map[outcome]*big.Rat{}

	for r1 := int64(0); r1 < total; r1++ {
		probe := drawWithStream(t, weights, k, []byte{byte(r1), 0})
		first := probe[0]
		rem := total - weights[first]
		for r2 := int64(0); r2 < rem; r2++ {
			res := drawWithStream(t, weights, k, []byte{byte(r1), byte(r2)})
			if res[0] != first {
				t.Fatalf("r1=%d: first pick changed between probe (%d) and draw (%d)", r1, first, res[0])
			}
			o := outcome{res[0], res[1]}
			p, ok := got[o]
			if !ok {
				p = new(big.Rat)
				got[o] = p
			}
			p.Add(p, big.NewRat(1, total*rem))
		}
	}

	if len(got) != 6 {
		t.Fatalf("expected 6 ordered outcomes, got %d: %v", len(got), got)
	}
	sum := new(big.Rat)
	for i := range weights {
		for j := range weights {
			if i == j {
				continue
			}
			want := big.NewRat(weights[i]*weights[j], total*(total-weights[i]))
			o := outcome{i, j}
			if got[o].Cmp(want) != 0 {
				t.Errorf("P(%d then %d) = %s, want %s", i, j, got[o], want)
			}
			sum.Add(sum, got[o])
		}
	}
	if sum.Cmp(big.NewRat(1, 1)) != 0 {
		t.Errorf("probabilities sum to %s, want 1", sum)
	}
}

// TestExactDistributionWithZeroWeights does the same exact enumeration with
// zero-weight items present, verifying they are never selected and the
// positive items keep their exact probabilities.
func TestExactDistributionWithZeroWeights(t *testing.T) {
	weights := []int64{0, 2, 0, 1}
	total := sumOf(weights) // 3
	k := 2

	type outcome struct{ first, second int }
	got := map[outcome]*big.Rat{}

	for r1 := int64(0); r1 < total; r1++ {
		probe := drawWithStream(t, weights, k, []byte{byte(r1), 0})
		first := probe[0]
		if weights[first] == 0 {
			t.Fatalf("zero-weight item %d selected", first)
		}
		rem := total - weights[first]
		for r2 := int64(0); r2 < rem; r2++ {
			res := drawWithStream(t, weights, k, []byte{byte(r1), byte(r2)})
			o := outcome{res[0], res[1]}
			p, ok := got[o]
			if !ok {
				p = new(big.Rat)
				got[o] = p
			}
			p.Add(p, big.NewRat(1, total*rem))
		}
	}

	for o := range got {
		if weights[o.first] == 0 || weights[o.second] == 0 {
			t.Errorf("zero-weight item in outcome %v", o)
		}
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 ordered outcomes, got %d: %v", len(got), got)
	}
	for i := range weights {
		for j := range weights {
			if i == j || weights[i] == 0 || weights[j] == 0 {
				continue
			}
			want := big.NewRat(weights[i]*weights[j], total*(total-weights[i]))
			if got[outcome{i, j}].Cmp(want) != 0 {
				t.Errorf("P(%d then %d) = %s, want %s", i, j, got[outcome{i, j}], want)
			}
		}
	}
}

// countingSource wraps a byte slice and reports how many bytes were read.
type countingSource struct {
	data []byte
	off  int
}

func (c *countingSource) Fill(buf []byte) error {
	if c.off+len(buf) > len(c.data) {
		return errors.New("test stream exhausted")
	}
	copy(buf, c.data[c.off:c.off+len(buf)])
	c.off += len(buf)
	return nil
}

func fixedStream(n int) []byte {
	data := make([]byte, n)
	state := uint32(0x12345678)
	for i := range data {
		// xorshift32, deterministic across runs.
		state ^= state << 13
		state ^= state >> 17
		state ^= state << 5
		data[i] = byte(state)
	}
	return data
}

// TestReplayDeterministic checks that a fixed byte stream replays exactly:
// same indices, same number of consumed bytes, across independent runs and
// across a sequence of draws with weight mutations in between.
func TestReplayDeterministic(t *testing.T) {
	weights := []int64{5, 0, 3, 7, 2, 0, 11}
	stream := fixedStream(1 << 16)

	run := func() ([][]int, int) {
		src := &countingSource{data: stream}
		s := newSampler(t, src, weights...)
		current := append([]int64(nil), weights...)
		var seq [][]int
		for round := 0; round < 50; round++ {
			got, err := s.Draw(3)
			if err != nil {
				t.Fatalf("round %d: %v", round, err)
			}
			for _, idx := range got {
				if current[idx] == 0 {
					t.Fatalf("round %d: zero-weight item %d selected", round, idx)
				}
			}
			seq = append(seq, got)
			// Mutate weights between draws; replay must reproduce this too.
			idx := round % len(weights)
			current[idx] = int64(round % 4)
			if err := s.SetWeight(idx, big.NewInt(current[idx])); err != nil {
				t.Fatalf("round %d: %v", round, err)
			}
		}
		return seq, src.off
	}

	seq1, n1 := run()
	seq2, n2 := run()
	if !reflect.DeepEqual(seq1, seq2) {
		t.Fatalf("same byte stream produced different draws")
	}
	if n1 != n2 {
		t.Fatalf("consumed byte counts differ: %d vs %d", n1, n2)
	}
}

var errCancel = errors.New("draw cancelled")

// flakySource serves a fixed stream but fails with errCancel once it has
// served failAfter bytes, until re-enabled.
type flakySource struct {
	data      []byte
	off       int
	failAfter int
	failing   bool
}

func (f *flakySource) Fill(buf []byte) error {
	if f.failing && f.off+len(buf) > f.failAfter {
		return errCancel
	}
	if f.off+len(buf) > len(f.data) {
		return errors.New("test stream exhausted")
	}
	copy(buf, f.data[f.off:f.off+len(buf)])
	f.off += len(buf)
	return nil
}

// TestCancelResume cancels a draw midway, checks that the set state is
// untouched, then resumes and verifies the result and the consumed stream
// position match an uninterrupted reference run over the same bytes.
func TestCancelResume(t *testing.T) {
	weights := []int64{3, 1, 4, 1, 5, 9, 2, 6}
	k := 4
	stream := fixedStream(1 << 16)

	// Reference run without cancellation.
	refSrc := &countingSource{data: stream}
	ref := newSampler(t, refSrc, weights...)
	want, err := ref.Draw(k)
	if err != nil {
		t.Fatalf("reference draw: %v", err)
	}
	wantConsumed := refSrc.off

	// Cancel after 2 bytes: guaranteed mid-draw since k picks need >= 4.
	flaky := &flakySource{data: stream, failAfter: 2, failing: true}
	s := newSampler(t, flaky, weights...)
	totalBefore := s.Total()
	if _, err := s.Draw(k); !errors.Is(err, errCancel) {
		t.Fatalf("expected cancellation, got %v", err)
	}
	// Set state must be intact.
	if s.Total().Cmp(totalBefore) != 0 {
		t.Fatalf("total changed after cancel: %s -> %s", totalBefore, s.Total())
	}
	for i, w := range weights {
		if s.Weight(i).Cmp(big.NewInt(w)) != 0 {
			t.Fatalf("weight %d changed after cancel", i)
		}
	}

	// Resume: buffered bytes must be replayed, then the rest of the stream.
	flaky.failing = false
	got, err := s.Draw(k)
	if err != nil {
		t.Fatalf("resumed draw: %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("resumed draw %v differs from reference %v", got, want)
	}
	if flaky.off != wantConsumed {
		t.Fatalf("stream position after resume = %d, want %d (bytes were not replayed exactly)", flaky.off, wantConsumed)
	}
}

// TestCancelResumeRepeated cancels the same draw several times in a row;
// each retry must resume the stream exactly.
func TestCancelResumeRepeated(t *testing.T) {
	weights := []int64{2, 8, 1, 8, 2}
	k := 5 // needs at least 5 random bytes, so failAfter < 5 is always mid-draw
	stream := fixedStream(1 << 16)

	want := drawWithStream(t, weights, k, stream)

	flaky := &flakySource{data: stream, failAfter: 1, failing: true}
	s := newSampler(t, flaky, weights...)
	for attempt := 0; attempt < 3; attempt++ {
		if _, err := s.Draw(k); !errors.Is(err, errCancel) {
			t.Fatalf("attempt %d: expected cancellation, got %v", attempt, err)
		}
		flaky.failAfter++ // let the next attempt get a little further
	}
	flaky.failing = false
	got, err := s.Draw(k)
	if err != nil {
		t.Fatalf("resumed draw: %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("after repeated cancel: got %v, want %v", got, want)
	}
}

func TestEdgeCases(t *testing.T) {
	stream := fixedStream(1024)

	// k = 0 consumes no randomness.
	src := &countingSource{data: stream}
	s := newSampler(t, src, 1, 2, 3)
	got, err := s.Draw(0)
	if err != nil || len(got) != 0 {
		t.Fatalf("Draw(0) = %v, %v", got, err)
	}
	if src.off != 0 {
		t.Fatalf("Draw(0) consumed %d bytes", src.off)
	}

	// k larger than the number of positive-weight items.
	if _, err := s.Draw(4); !errors.Is(err, wsample.ErrTooFewPositiveItems) {
		t.Fatalf("Draw(4) with 3 items: %v", err)
	}
	if err := s.SetWeight(0, big.NewInt(0)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Draw(3); !errors.Is(err, wsample.ErrTooFewPositiveItems) {
		t.Fatalf("Draw(3) with 2 positive items: %v", err)
	}

	// Negative and nil weights are rejected.
	if _, err := s.Add(big.NewInt(-1)); !errors.Is(err, wsample.ErrNegativeWeight) {
		t.Fatalf("Add(-1): %v", err)
	}
	if err := s.SetWeight(1, big.NewInt(-5)); !errors.Is(err, wsample.ErrNegativeWeight) {
		t.Fatalf("SetWeight(-5): %v", err)
	}
	if _, err := s.Add(nil); err == nil {
		t.Fatalf("Add(nil) should fail")
	}

	// Total tracks mutations.
	if err := s.SetWeight(0, big.NewInt(10)); err != nil {
		t.Fatal(err)
	}
	if s.Total().Cmp(big.NewInt(15)) != 0 {
		t.Fatalf("total = %s, want 15", s.Total())
	}
}

// TestBigWeights exercises weights beyond 64 bits and checks replay
// determinism plus basic invariants.
func TestBigWeights(t *testing.T) {
	huge := func(p uint, extra int64) *big.Int {
		v := new(big.Int).Lsh(big.NewInt(1), p)
		return v.Add(v, big.NewInt(extra))
	}
	weights := []*big.Int{huge(100, 7), big.NewInt(0), huge(130, 1), huge(65, 3)}
	stream := fixedStream(1 << 16)

	run := func() []int {
		src := &countingSource{data: stream}
		s := wsample.New(src)
		for _, w := range weights {
			if _, err := s.Add(w); err != nil {
				t.Fatalf("Add: %v", err)
			}
		}
		got, err := s.Draw(3)
		if err != nil {
			t.Fatalf("Draw: %v", err)
		}
		return got
	}

	a, b := run(), run()
	if !reflect.DeepEqual(a, b) {
		t.Fatalf("big-weight replay mismatch: %v vs %v", a, b)
	}
	seen := map[int]bool{}
	for _, idx := range a {
		if weights[idx].Sign() == 0 {
			t.Fatalf("zero-weight item %d selected", idx)
		}
		if seen[idx] {
			t.Fatalf("item %d selected twice", idx)
		}
		seen[idx] = true
	}
}
