package wsample

import (
	"errors"
	"fmt"
	"io"
	"math/big"
	"reflect"
	"testing"
)

// enumerateOutcomes enumerates the entire random domain for drawing k items
// from the given weights: every sequence of accepted uniform values
// v_d in [0, total_d) at each depth d. Each sequence maps to exactly one
// canonical byte stream (values < 256 fit a single byte and are never
// rejected), so running the sampler on that stream replays that point of the
// random domain. Returns the exact probability mass of each ordered outcome
// as a big.Rat.
func enumerateOutcomes(t *testing.T, weights []uint64, k int) map[string]*big.Rat {
	t.Helper()
	outcomes := map[string]*big.Rat{}
	var rec func(prefix []byte, prob *big.Rat, depth int)
	rec = func(prefix []byte, prob *big.Rat, depth int) {
		if depth == k {
			stream := NewBytesStream(prefix)
			s := New(weights, stream)
			got, err := s.DrawK(k, nil)
			if err != nil {
				t.Fatalf("DrawK on stream %v: %v", prefix, err)
			}
			if stream.Pos() != uint64(len(prefix)) {
				t.Fatalf("stream %v: consumed %d bytes, want %d", prefix, stream.Pos(), len(prefix))
			}
			key := fmt.Sprint(got)
			acc := outcomes[key]
			if acc == nil {
				acc = new(big.Rat)
				outcomes[key] = acc
			}
			acc.Add(acc, prob)
			return
		}
		s := New(weights, NewBytesStream(prefix))
		if _, err := s.DrawK(depth, nil); err != nil {
			t.Fatalf("prefix %v: %v", prefix, err)
		}
		total := s.Total()
		if !total.IsInt64() || total.Int64() <= 0 || total.Int64() > 255 {
			t.Fatalf("test helper requires 0 < total < 256, got %v", total)
		}
		step := new(big.Rat).SetFrac(big.NewInt(1), total)
		for v := int64(0); v < total.Int64(); v++ {
			next := append(append([]byte(nil), prefix...), byte(v))
			rec(next, new(big.Rat).Mul(prob, step), depth+1)
		}
	}
	rec(nil, big.NewRat(1, 1), 0)
	return outcomes
}

// theoreticalOrdered returns the exact probability of the ordered draw
// seq = (i0, i1, ...) under weighted sampling without replacement:
// prod_d w_{i_d} / (T - w_{i_0} - ... - w_{i_{d-1}}).
func theoreticalOrdered(weights []uint64, seq []int) *big.Rat {
	p := big.NewRat(1, 1)
	remaining := new(big.Int)
	for _, w := range weights {
		remaining.Add(remaining, new(big.Int).SetUint64(w))
	}
	for _, i := range seq {
		w := new(big.Int).SetUint64(weights[i])
		p.Mul(p, new(big.Rat).SetFrac(w, remaining))
		remaining.Sub(remaining, w)
	}
	return p
}

func TestExactProbabilities(t *testing.T) {
	weights := []uint64{1, 2, 3}
	const k = 2
	got := enumerateOutcomes(t, weights, k)

	want := map[string]*big.Rat{}
	for i := range weights {
		for j := range weights {
			if i == j {
				continue
			}
			want[fmt.Sprint([]int{i, j})] = theoreticalOrdered(weights, []int{i, j})
		}
	}
	if len(got) != len(want) {
		t.Fatalf("got %d distinct outcomes, want %d", len(got), len(want))
	}
	total := new(big.Rat)
	for key, p := range got {
		w, ok := want[key]
		if !ok {
			t.Fatalf("unexpected outcome %s with probability %v", key, p)
		}
		if p.Cmp(w) != 0 {
			t.Errorf("outcome %s: enumerated probability %v, theoretical %v", key, p, w)
		}
		total.Add(total, p)
	}
	if total.Cmp(big.NewRat(1, 1)) != 0 {
		t.Errorf("probabilities sum to %v, want 1", total)
	}
}

func TestExactProbabilitiesWithZeroWeights(t *testing.T) {
	weights := []uint64{0, 2, 0, 2}
	got := enumerateOutcomes(t, weights, 2)

	// Only items 1 and 3 can ever appear; each ordered pair has mass 1/2.
	want := map[string]*big.Rat{
		fmt.Sprint([]int{1, 3}): big.NewRat(1, 2),
		fmt.Sprint([]int{3, 1}): big.NewRat(1, 2),
	}
	if len(got) != len(want) {
		t.Fatalf("got outcomes %v, want only items 1 and 3", got)
	}
	for key, p := range got {
		if p.Cmp(want[key]) != 0 {
			t.Errorf("outcome %s: got %v, want %v", key, p, want[key])
		}
	}
}

func TestRejectionSamplingNoModulo(t *testing.T) {
	// Total 3 needs 2 bits: one byte masked with 0x03. Bytes 7 and 255 both
	// mask to 3 and must be rejected (not reduced modulo 3); byte 2 is
	// accepted and selects item 1 (item 0 covers [0,1), item 1 covers [1,3)).
	stream := NewBytesStream([]byte{7, 255, 2})
	s := New([]uint64{1, 2}, stream)
	got, err := s.DrawOne()
	if err != nil {
		t.Fatal(err)
	}
	if got != 1 {
		t.Errorf("drew item %d, want 1", got)
	}
	if stream.Pos() != 3 {
		t.Errorf("consumed %d bytes, want 3 (two rejections then accept)", stream.Pos())
	}
}

func TestReplayFixedStream(t *testing.T) {
	weights := []uint64{5, 0, 3, 7, 11, 2, 13, 0, 1}
	data := make([]byte, 256)
	for i := range data {
		data[i] = byte(i*37 + 11)
	}
	run := func() []int {
		s := New(weights, NewBytesStream(data))
		got, err := s.DrawK(5, nil)
		if err != nil {
			t.Fatal(err)
		}
		return got
	}
	first, second := run(), run()
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("same byte stream replayed differently: %v vs %v", first, second)
	}
	seen := map[int]bool{}
	for _, i := range first {
		if seen[i] {
			t.Fatalf("item %d drawn twice in %v", i, first)
		}
		seen[i] = true
		if weights[i] == 0 {
			t.Fatalf("zero-weight item %d drawn in %v", i, first)
		}
	}
}

func TestCancelCommitsAndResumeMatchesUninterrupted(t *testing.T) {
	weights := []uint64{3, 1, 4, 1, 5, 9, 2, 6}
	data := make([]byte, 512)
	for i := range data {
		data[i] = byte(i*101 + 7)
	}

	// Reference: uninterrupted draw of 5 items.
	ref, err := New(weights, NewBytesStream(data)).DrawK(5, nil)
	if err != nil {
		t.Fatal(err)
	}

	// Cancelled run: allow exactly 2 draws, then cancel.
	stream := NewBytesStream(data)
	s := New(weights, stream)
	remaining := 2
	partial, err := s.DrawK(5, func() bool {
		if remaining == 0 {
			return true
		}
		remaining--
		return false
	})
	if !errors.Is(err, ErrCancelled) {
		t.Fatalf("got err %v, want ErrCancelled", err)
	}
	if len(partial) != 2 {
		t.Fatalf("got %d committed items, want 2", len(partial))
	}
	// No rollback: the two drawn items are gone from the set, and they are
	// exactly the first two items of the uninterrupted run.
	for _, i := range partial {
		if s.Alive(i) {
			t.Errorf("drawn item %d still alive after cancellation", i)
		}
	}
	if !reflect.DeepEqual(partial, ref[:2]) {
		t.Fatalf("committed prefix %v differs from reference %v", partial, ref[:2])
	}

	// Persist and restore: snapshot carries set state + stream position.
	snap := s.Snapshot()
	restoredStream := NewBytesStream(data)
	if err := restoredStream.Seek(snap.StreamPos); err != nil {
		t.Fatal(err)
	}
	s2, err := Restore(snap, restoredStream)
	if err != nil {
		t.Fatal(err)
	}
	rest, err := s2.DrawK(3, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := append(append([]int(nil), partial...), rest...); !reflect.DeepEqual(got, ref) {
		t.Fatalf("resumed draw %v differs from uninterrupted %v", got, ref)
	}
}

func TestCancelBeforeAnyDrawLeavesStateUntouched(t *testing.T) {
	weights := []uint64{2, 3, 5}
	stream := NewBytesStream([]byte{1, 2, 3, 4})
	s := New(weights, stream)
	got, err := s.DrawK(2, func() bool { return true })
	if !errors.Is(err, ErrCancelled) {
		t.Fatalf("got err %v, want ErrCancelled", err)
	}
	if len(got) != 0 {
		t.Fatalf("drew %v despite immediate cancellation", got)
	}
	if s.Total().Cmp(big.NewInt(10)) != 0 || s.Remaining() != 3 || stream.Pos() != 0 {
		t.Fatalf("state changed: total %v, remaining %d, pos %d", s.Total(), s.Remaining(), stream.Pos())
	}
}

func TestRestoreRejectsWrongStreamPosition(t *testing.T) {
	s := New([]uint64{1, 1}, NewBytesStream([]byte{0, 0, 0, 0}))
	snap := s.Snapshot()
	bad := NewBytesStream([]byte{0, 0, 0, 0})
	if _, err := bad.Read(make([]byte, 1)); err != nil {
		t.Fatal(err)
	}
	if _, err := Restore(snap, bad); !errors.Is(err, ErrStreamPosition) {
		t.Fatalf("got err %v, want ErrStreamPosition", err)
	}
}

func TestSetWeight(t *testing.T) {
	s := New([]uint64{1, 1}, NewBytesStream([]byte{0, 0, 0, 0}))
	if err := s.SetWeight(1, 0); err != nil {
		t.Fatal(err)
	}
	if s.Total().Cmp(big.NewInt(1)) != 0 || s.Remaining() != 1 {
		t.Fatalf("total %v remaining %d, want 1 and 1", s.Total(), s.Remaining())
	}
	// Only item 0 can be drawn now.
	for i := 0; i < 4; i++ {
		s := New([]uint64{1, 0}, NewBytesStream([]byte{byte(2 * i)}))
		got, err := s.DrawOne()
		if err != nil {
			t.Fatal(err)
		}
		if got != 0 {
			t.Fatalf("drew %d, want 0 (only positive-weight item)", got)
		}
	}
	if err := s.SetWeight(0, 9); err != nil {
		t.Fatal(err)
	}
	if s.Total().Cmp(big.NewInt(9)) != 0 {
		t.Fatalf("total %v, want 9", s.Total())
	}
	// Draw item 0, then mutating it must fail.
	if _, err := s.DrawOne(); err != nil {
		t.Fatal(err)
	}
	if err := s.SetWeight(0, 1); !errors.Is(err, ErrItemRemoved) {
		t.Fatalf("got err %v, want ErrItemRemoved", err)
	}
}

func TestDrawKPreflightErrors(t *testing.T) {
	stream := NewBytesStream([]byte{0, 0, 0, 0})
	s := New([]uint64{1, 0, 2}, stream)
	if _, err := s.DrawK(3, nil); !errors.Is(err, ErrNotEnoughItems) {
		t.Fatalf("got err %v, want ErrNotEnoughItems", err)
	}
	// Failed preflight must not consume bytes or change state.
	if stream.Pos() != 0 || s.Total().Cmp(big.NewInt(3)) != 0 || s.Remaining() != 2 {
		t.Fatalf("state changed after failed DrawK")
	}
	if _, err := s.DrawK(-1, nil); err == nil {
		t.Fatal("negative k accepted")
	}
}

func TestBigWeightSums(t *testing.T) {
	// Sum exceeds 64 bits; totals must be tracked with big.Int.
	big1 := uint64(1)<<63 + 7
	big2 := uint64(1)<<63 + 3
	s := New([]uint64{big1, big2}, NewBytesStream(make([]byte, 64)))
	want := new(big.Int).Add(new(big.Int).SetUint64(big1), new(big.Int).SetUint64(big2))
	if s.Total().Cmp(want) != 0 {
		t.Fatalf("total %v, want %v", s.Total(), want)
	}
	got, err := s.DrawK(2, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0] == got[1] {
		t.Fatalf("bad draw %v", got)
	}
	if s.Total().Sign() != 0 {
		t.Fatalf("total %v after exhausting set, want 0", s.Total())
	}
	if _, err := s.DrawOne(); !errors.Is(err, ErrNoPositiveWeight) {
		t.Fatalf("got err %v, want ErrNoPositiveWeight", err)
	}
}

func TestStreamExhaustionIsAnErrorNotAHang(t *testing.T) {
	s := New([]uint64{1, 1}, NewBytesStream(nil))
	if _, err := s.DrawOne(); !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("got err %v, want EOF", err)
	}
}
