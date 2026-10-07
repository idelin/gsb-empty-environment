package paircmp

import (
	"errors"
	"io"
	"math/rand"
	"testing"
)

// chunkedErrReader serves data in short reads of at most maxChunk
// bytes and fails with err once pos reaches errAt (-1 disables).
type chunkedErrReader struct {
	data     []byte
	pos      int
	maxChunk int
	errAt    int
	err      error
}

func (r *chunkedErrReader) Read(p []byte) (int, error) {
	if r.errAt >= 0 && r.pos >= r.errAt {
		return 0, r.err
	}
	n := min(len(p), r.maxChunk, len(r.data)-r.pos)
	if r.errAt >= 0 {
		n = min(n, r.errAt-r.pos)
	}
	if n <= 0 {
		return 0, io.EOF
	}
	copy(p, r.data[r.pos:r.pos+n])
	r.pos += n
	return n, nil
}

func TestCompareStreamsRandom(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	for trial := 0; trial < 400; trial++ {
		a, b := genPair(rng)
		lr := &chunkedErrReader{data: a, maxChunk: 1 + rng.Intn(9), errAt: -1}
		rr := &chunkedErrReader{data: b, maxChunk: 1 + rng.Intn(9), errAt: -1}
		res := CompareStreams(lr, rr, 1+rng.Intn(16))
		if !res.Complete || res.Err != nil {
			t.Fatalf("trial %d: incomplete: %+v", trial, res)
		}
		if res.Compared != int64(min(len(a), len(b))) {
			t.Fatalf("trial %d: Compared=%d", trial, res.Compared)
		}
		wantD, wantM := modelCompare(a, b)
		if !intervalsEqual(res.Diffs, wantD) {
			t.Fatalf("trial %d: diffs %v, want %v", trial, res.Diffs, wantD)
		}
		if !missingEqual(res.Missing, wantM) {
			t.Fatalf("trial %d: missing %+v, want %+v", trial, res.Missing, wantM)
		}
	}
}

func TestCompareStreamsErrors(t *testing.T) {
	rng := rand.New(rand.NewSource(2))
	boom := errors.New("boom")
	for trial := 0; trial < 400; trial++ {
		a, b := genPair(rng)
		errAtL, errAtR := -1, -1
		if rng.Intn(2) == 0 {
			errAtL = rng.Intn(len(a) + 1)
		}
		if rng.Intn(2) == 0 {
			errAtR = rng.Intn(len(b) + 1)
		}
		if errAtL < 0 && errAtR < 0 {
			errAtL = rng.Intn(len(a) + 1)
		}
		bufSize := 1 + rng.Intn(16)
		lr := &chunkedErrReader{data: a, maxChunk: 1 + rng.Intn(9), errAt: errAtL, err: boom}
		rr := &chunkedErrReader{data: b, maxChunk: 1 + rng.Intn(9), errAt: errAtR, err: boom}
		res := CompareStreams(lr, rr, bufSize)
		if res.Complete || res.Err == nil {
			t.Fatalf("trial %d: expected incomplete with error, got %+v", trial, res)
		}
		if res.Missing != nil {
			t.Fatalf("trial %d: missing tail reported on incomplete run: %+v", trial, res.Missing)
		}
		// Comparison must stop exactly at the first unreadable byte.
		stop := int64(min(len(a), len(b)))
		if errAtL >= 0 {
			stop = min(stop, int64(errAtL))
		}
		if errAtR >= 0 {
			stop = min(stop, int64(errAtR))
		}
		if res.Compared != stop {
			t.Fatalf("trial %d: Compared=%d, want %d (errAtL=%d errAtR=%d lenA=%d lenB=%d)",
				trial, res.Compared, stop, errAtL, errAtR, len(a), len(b))
		}
		// Confirmed diffs must equal the model clipped to Compared.
		wantD, _ := modelCompare(a, b)
		if got := clipIntervals(wantD, res.Compared); !intervalsEqual(res.Diffs, got) {
			t.Fatalf("trial %d: diffs %v, want %v", trial, res.Diffs, got)
		}
	}
}

type zeroReader struct{}

func (zeroReader) Read(p []byte) (int, error) { return 0, nil }

func TestCompareStreamsNoProgress(t *testing.T) {
	res := CompareStreams(zeroReader{}, zeroReader{}, 8)
	if !errors.Is(res.Err, ErrNoProgress) || res.Complete {
		t.Fatalf("expected ErrNoProgress, got %+v", res)
	}
}
