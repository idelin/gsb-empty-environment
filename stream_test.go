package diffcmp

import (
	"errors"
	"io"
	"math"
	"math/rand"
	"reflect"
	"testing"
)

var errInjected = errors.New("injected read error")

// fuzzReader delivers data with random short reads and, once pos reaches
// errAt, fails every read with a hard (non-EOF) error. errAt beyond
// len(data) means a clean EOF.
type fuzzReader struct {
	data     []byte
	pos      int
	errAt    int
	err      error
	maxChunk int
	rng      *rand.Rand
}

func (r *fuzzReader) Read(p []byte) (int, error) {
	if r.pos >= len(r.data) {
		return 0, io.EOF
	}
	if r.pos >= r.errAt {
		return 0, r.err
	}
	n := min(len(p), len(r.data)-r.pos, r.errAt-r.pos)
	if r.maxChunk > 0 && n > r.maxChunk {
		n = 1 + r.rng.Intn(r.maxChunk)
	}
	copy(p, r.data[r.pos:r.pos+n])
	r.pos += n
	return n, nil
}

// runsOver returns the maximal contiguous difference intervals between
// two equal-length slices.
func runsOver(a, b []byte) []Interval {
	var out []Interval
	open := -1
	for i := range a {
		if a[i] != b[i] {
			if open < 0 {
				open = i
			}
		} else if open >= 0 {
			out = append(out, Interval{Start: int64(open), End: int64(i)})
			open = -1
		}
	}
	if open >= 0 {
		out = append(out, Interval{Start: int64(open), End: int64(len(a))})
	}
	return out
}

// modelStream is the full-array model: it derives the expected result
// directly from the complete contents and the error positions, with no
// chunking or short-read concerns.
func modelStream(a, b []byte, errAAt, errBAt int) StreamResult {
	effA := min(len(a), errAAt)
	effB := min(len(b), errBAt)
	aErr := errAAt < len(a)
	bErr := errBAt < len(b)
	n := min(effA, effB)
	res := StreamResult{Diffs: runsOver(a[:n], b[:n])}
	incomplete := func() StreamResult {
		res.Err = errInjected
		return res
	}
	switch {
	case effA == effB:
		if aErr || bErr {
			return incomplete()
		}
		res.Complete = true
	case effA < effB:
		if aErr {
			return incomplete()
		}
		tail := MissingTail{Side: 0, Start: int64(effA)}
		if bErr {
			tail.End = -1
			res.Missing = append(res.Missing, tail)
			return incomplete()
		}
		tail.End = int64(effB)
		res.Missing = append(res.Missing, tail)
		res.Complete = true
	default:
		if bErr {
			return incomplete()
		}
		tail := MissingTail{Side: 1, Start: int64(effB)}
		if aErr {
			tail.End = -1
			res.Missing = append(res.Missing, tail)
			return incomplete()
		}
		tail.End = int64(effA)
		res.Missing = append(res.Missing, tail)
		res.Complete = true
	}
	return res
}

func randBytes(rng *rand.Rand, n int) []byte {
	b := make([]byte, n)
	rng.Read(b)
	return b
}

// mutate returns a copy of base with each byte flipped with probability p.
func mutate(rng *rand.Rand, base []byte, p float64) []byte {
	out := append([]byte(nil), base...)
	for i := range out {
		if rng.Float64() < p {
			out[i] ^= 1 << uint(rng.Intn(8))
		}
	}
	return out
}

func TestCompareStreamsAgainstFullArrayModel(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	chunkSizes := []int{1, 3, 7, 64, 1024}
	maxChunks := []int{0, 1, 2, 5, 16}
	for iter := 0; iter < 500; iter++ {
		lenA := rng.Intn(300)
		lenB := rng.Intn(300)
		common := min(lenA, lenB)
		base := randBytes(rng, common)
		a := append(randBytes(rng, 0), base...)
		a = append(a, randBytes(rng, lenA-common)...)
		b := mutate(rng, base, 0.15)
		b = append(b, randBytes(rng, lenB-common)...)

		errAAt, errBAt := math.MaxInt, math.MaxInt
		if rng.Intn(3) == 0 {
			errAAt = rng.Intn(lenA + 20)
		}
		if rng.Intn(3) == 0 {
			errBAt = rng.Intn(lenB + 20)
		}
		chunkSize := chunkSizes[rng.Intn(len(chunkSizes))]
		maxChunk := maxChunks[rng.Intn(len(maxChunks))]

		ra := &fuzzReader{data: a, errAt: errAAt, err: errInjected, maxChunk: maxChunk, rng: rng}
		rb := &fuzzReader{data: b, errAt: errBAt, err: errInjected, maxChunk: maxChunk, rng: rng}
		got := CompareStreams(ra, rb, chunkSize)
		want := modelStream(a, b, errAAt, errBAt)

		if !reflect.DeepEqual(got.Diffs, want.Diffs) {
			t.Fatalf("iter %d (lenA=%d lenB=%d errA=%d errB=%d chunk=%d): diffs got %v want %v",
				iter, lenA, lenB, errAAt, errBAt, chunkSize, got.Diffs, want.Diffs)
		}
		if !reflect.DeepEqual(got.Missing, want.Missing) {
			t.Fatalf("iter %d (lenA=%d lenB=%d errA=%d errB=%d chunk=%d): missing got %v want %v",
				iter, lenA, lenB, errAAt, errBAt, chunkSize, got.Missing, want.Missing)
		}
		if got.Complete != want.Complete {
			t.Fatalf("iter %d: complete got %v want %v", iter, got.Complete, want.Complete)
		}
		if (got.Err != nil) != (want.Err != nil) {
			t.Fatalf("iter %d: err got %v want non-nil=%v", iter, got.Err, want.Err != nil)
		}
		if got.Complete && got.Err != nil {
			t.Fatalf("iter %d: complete result carries error %v", iter, got.Err)
		}
	}
}

func TestCompareStreamsErrorKeepsConfirmedDiffs(t *testing.T) {
	a := []byte{1, 1, 1, 9, 9, 9}
	b := []byte{2, 2, 2, 9, 9, 9}
	// A differs in [0,3), then fails with a hard error at position 3.
	ra := &fuzzReader{data: a, errAt: 3, err: errInjected}
	rb := &fuzzReader{data: b, errAt: math.MaxInt, err: errInjected}
	got := CompareStreams(ra, rb, 4)
	want := []Interval{{Start: 0, End: 3}}
	if !reflect.DeepEqual(got.Diffs, want) {
		t.Fatalf("diffs got %v want %v", got.Diffs, want)
	}
	if got.Complete || got.Err == nil {
		t.Fatalf("expected incomplete result with error, got %+v", got)
	}
	if len(got.Missing) != 0 {
		t.Fatalf("hard error must not report a missing tail, got %v", got.Missing)
	}
}

func TestCompareStreamsUnequalTailIsMissingNotDiff(t *testing.T) {
	a := []byte{5, 6, 7}
	b := []byte{5, 6, 7, 8, 9, 10}
	got := CompareStreams(
		&fuzzReader{data: a, errAt: math.MaxInt, err: errInjected},
		&fuzzReader{data: b, errAt: math.MaxInt, err: errInjected},
		2,
	)
	if !got.Complete || got.Err != nil {
		t.Fatalf("expected complete result, got %+v", got)
	}
	if len(got.Diffs) != 0 {
		t.Fatalf("tail bytes must not be reported as diffs, got %v", got.Diffs)
	}
	want := []MissingTail{{Side: 0, Start: 3, End: 6}}
	if !reflect.DeepEqual(got.Missing, want) {
		t.Fatalf("missing got %v want %v", got.Missing, want)
	}
}
