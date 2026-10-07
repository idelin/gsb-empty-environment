package paircmp

import (
	"context"
	"crypto/sha256"
	"errors"
	"math/rand"
	"sync"
	"testing"
)

// fakeRemote serves a byte array over the Remote interface with
// controllable segmentation, short reads, shuffling and failures.
type fakeRemote struct {
	mu        sync.Mutex
	data      []byte
	version   string
	respCap   int64 // max bytes per Fetch response (short reads)
	segMax    int64 // max bytes per segment
	shuffle   bool
	failAfter int // 1-based Fetch call at which to start failing; 0 = never
	calls     int
	headCalls int
	onFetch   func(call int, f *fakeRemote)
}

var errFetch = errors.New("fetch failed")

func (f *fakeRemote) Fetch(_ context.Context, off, want int64) ([]Segment, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	if f.failAfter > 0 && f.calls >= f.failAfter {
		return nil, errFetch
	}
	if f.onFetch != nil {
		f.onFetch(f.calls, f)
	}
	if off >= int64(len(f.data)) {
		return nil, nil
	}
	end := min(off+want, int64(len(f.data)))
	if f.respCap > 0 {
		end = min(end, off+f.respCap)
	}
	var segs []Segment
	for p := off; p < end; {
		q := end
		if f.segMax > 0 {
			q = min(q, p+f.segMax)
		}
		segs = append(segs, Segment{
			Off:     p,
			Data:    append([]byte(nil), f.data[p:q]...),
			Version: f.version,
		})
		p = q
	}
	if f.shuffle {
		rand.Shuffle(len(segs), func(i, j int) { segs[i], segs[j] = segs[j], segs[i] })
	}
	return segs, nil
}

func (f *fakeRemote) Head(_ context.Context, off, want int64) (string, [32]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.headCalls++
	if off >= int64(len(f.data)) {
		return f.version, sha256.Sum256(nil), nil
	}
	end := min(off+want, int64(len(f.data)))
	return f.version, sha256.Sum256(f.data[off:end]), nil
}

func TestRemoteOutOfOrderShortReads(t *testing.T) {
	rng := rand.New(rand.NewSource(3))
	ctx := context.Background()
	for trial := 0; trial < 200; trial++ {
		a, b := genPair(rng)
		left := &fakeRemote{data: a, version: "v1", respCap: int64(1 + rng.Intn(13)), segMax: int64(1 + rng.Intn(7)), shuffle: true}
		right := &fakeRemote{data: b, version: "r1", respCap: int64(1 + rng.Intn(13)), segMax: int64(1 + rng.Intn(7)), shuffle: true}
		res, err := CompareRemote(ctx, left, right, int64(1+rng.Intn(24)))
		if err != nil || !res.Complete {
			t.Fatalf("trial %d: %+v err=%v", trial, res, err)
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

// runPasses drives Compare until a non-invalidated result, counting
// invalidations.
func runPasses(t *testing.T, cmp *RemoteComparator) (RemoteResult, int) {
	t.Helper()
	ctx := context.Background()
	var ck *Checkpoint
	invalidations := 0
	for i := 0; i < 100; i++ {
		res := cmp.Compare(ctx, ck)
		if !res.Invalidated {
			return res, invalidations
		}
		invalidations++
		ck = &res.Checkpoint
	}
	t.Fatal("too many invalidations")
	return RemoteResult{}, 0
}

func TestRemoteVersionSwitchInvalidates(t *testing.T) {
	rng := rand.New(rand.NewSource(4))
	base := randBytes(rng, 100)
	a := base
	b := mutateBytes(rng, base, 15)
	finalA := mutateBytes(rng, a, 20)
	finalB := mutateBytes(rng, b, 20)
	finalA[0] = ^a[0]
	finalB[3] = ^b[3]
	// Short reads (respCap < chunk) force multi-fetch chunks, so the
	// switch lands mid-chunk and mid-run alike.
	left := &fakeRemote{data: a, version: "v1", respCap: 3, segMax: 2, shuffle: true}
	right := &fakeRemote{data: b, version: "r1", respCap: 3, segMax: 2, shuffle: true}
	left.onFetch = func(call int, f *fakeRemote) {
		if call == 5 {
			f.data = finalA
			f.version = "v2"
		}
	}
	right.onFetch = func(call int, f *fakeRemote) {
		if call == 5 {
			f.data = finalB
			f.version = "r2"
		}
	}
	cmp := &RemoteComparator{Left: left, Right: right, Chunk: 8}
	res, invalidations := runPasses(t, cmp)
	if invalidations == 0 {
		t.Fatal("expected at least one invalidation")
	}
	if !res.Complete || res.Err != nil {
		t.Fatalf("incomplete: %+v", res)
	}
	// The final conclusion must be computed purely from the final
	// versions: no interval may splice bytes from different versions.
	wantD, wantM := modelCompare(finalA, finalB)
	if !intervalsEqual(res.Diffs, wantD) {
		t.Fatalf("diffs %v, want %v", res.Diffs, wantD)
	}
	if !missingEqual(res.Missing, wantM) {
		t.Fatalf("missing %+v, want %+v", res.Missing, wantM)
	}
}

func TestRemoteVersionOnlyChangeInvalidates(t *testing.T) {
	rng := rand.New(rand.NewSource(5))
	a, b := genPair(rng)
	left := &fakeRemote{data: a, version: "v1"}
	right := &fakeRemote{data: b, version: "r1"}
	left.onFetch = func(call int, f *fakeRemote) {
		if call == 4 {
			f.version = "v2" // data unchanged
		}
	}
	cmp := &RemoteComparator{Left: left, Right: right, Chunk: 8}
	res, invalidations := runPasses(t, cmp)
	if invalidations == 0 {
		t.Fatal("version-only change must invalidate the conclusion")
	}
	if !res.Complete {
		t.Fatalf("incomplete: %+v", res)
	}
	wantD, wantM := modelCompare(a, b)
	if !intervalsEqual(res.Diffs, wantD) || !missingEqual(res.Missing, wantM) {
		t.Fatalf("diffs %v missing %+v, want %v %+v", res.Diffs, res.Missing, wantD, wantM)
	}
}

func TestRemoteResumeReusesValidatedPrefix(t *testing.T) {
	rng := rand.New(rand.NewSource(6))
	base := randBytes(rng, 120)
	a := base
	b := mutateBytes(rng, base, 15)
	left := &fakeRemote{data: a, version: "v1", failAfter: 6}
	right := &fakeRemote{data: b, version: "r1", failAfter: 6}
	cmp := &RemoteComparator{Left: left, Right: right, Chunk: 8}
	ctx := context.Background()

	res1 := cmp.Compare(ctx, nil)
	if res1.Err == nil || res1.Complete {
		t.Fatalf("expected failure, got %+v", res1)
	}
	ck := res1.Checkpoint
	if ck.Offset != 40 {
		t.Fatalf("checkpoint offset %d, want 40", ck.Offset)
	}
	// Confirmed diffs are kept in the failed result.
	wantD, _ := modelCompare(a, b)
	if got := clipIntervals(wantD, res1.Checkpoint.Offset); !intervalsEqual(ck.Diffs, got) {
		t.Fatalf("checkpoint diffs %v, want %v", ck.Diffs, got)
	}

	left.failAfter, right.failAfter = 0, 0
	leftCalls, rightCalls := left.calls, right.calls
	res2 := cmp.Compare(ctx, &ck)
	if !res2.Complete || res2.Err != nil {
		t.Fatalf("resume failed: %+v", res2)
	}
	if !intervalsEqual(res2.Diffs, wantD) {
		t.Fatalf("diffs %v, want %v", res2.Diffs, wantD)
	}
	// The validated prefix must be reused via Head, not re-fetched.
	if left.headCalls == 0 || right.headCalls == 0 {
		t.Fatal("expected Head validation calls")
	}
	wantFetches := (int64(len(a))-ck.Offset)/8 + 1 // remaining chunks + EOF probe
	if got := int64(left.calls - leftCalls); got != wantFetches {
		t.Fatalf("left fetches after resume = %d, want %d", got, wantFetches)
	}
	if got := int64(right.calls - rightCalls); got != wantFetches {
		t.Fatalf("right fetches after resume = %d, want %d", got, wantFetches)
	}
}

func TestRemoteResumeDigestMismatch(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	base := randBytes(rng, 120)
	a := base
	b := mutateBytes(rng, base, 15)
	left := &fakeRemote{data: a, version: "v1", failAfter: 6}
	right := &fakeRemote{data: b, version: "r1", failAfter: 6}
	cmp := &RemoteComparator{Left: left, Right: right, Chunk: 8}
	ctx := context.Background()

	res1 := cmp.Compare(ctx, nil)
	ck := res1.Checkpoint
	left.failAfter, right.failAfter = 0, 0
	// Mutate left inside the validated region WITHOUT bumping the
	// version: only the digest check can catch it.
	left.data[16] ^= 0xFF
	left.data[20] ^= 0xFF

	res2 := cmp.Compare(ctx, &ck)
	if !res2.Complete {
		t.Fatalf("resume failed: %+v", res2)
	}
	wantD, _ := modelCompare(left.data, b)
	if !intervalsEqual(res2.Diffs, wantD) {
		t.Fatalf("diffs %v, want %v", res2.Diffs, wantD)
	}
	// Validation must stop at the first mismatched chunk ([16,24)).
	if left.headCalls != 3 {
		t.Fatalf("left head calls = %d, want 3", left.headCalls)
	}
	if right.headCalls != 5 {
		t.Fatalf("right head calls = %d, want 5", right.headCalls)
	}
}

func TestRemoteResumeMergesRunAcrossBoundary(t *testing.T) {
	a := make([]byte, 64)
	b := make([]byte, 64)
	for i := 38; i <= 45; i++ {
		b[i] = 1 // diff run straddles the checkpoint offset 40
	}
	left := &fakeRemote{data: a, version: "v1", failAfter: 6}
	right := &fakeRemote{data: b, version: "r1", failAfter: 6}
	cmp := &RemoteComparator{Left: left, Right: right, Chunk: 8}
	ctx := context.Background()

	res1 := cmp.Compare(ctx, nil)
	ck := res1.Checkpoint
	if ck.Offset != 40 {
		t.Fatalf("checkpoint offset %d, want 40", ck.Offset)
	}
	left.failAfter, right.failAfter = 0, 0
	res2 := cmp.Compare(ctx, &ck)
	if !res2.Complete {
		t.Fatalf("resume failed: %+v", res2)
	}
	want := []Interval{{Start: 38, End: 46}}
	if !intervalsEqual(res2.Diffs, want) {
		t.Fatalf("diffs %v, want %v (run must merge across resume point)", res2.Diffs, want)
	}
}

func TestRemoteMissingTail(t *testing.T) {
	rng := rand.New(rand.NewSource(8))
	a, b := genPair(rng)
	a = append(a, randBytes(rng, 30)...) // left longer
	left := &fakeRemote{data: a, version: "v1", respCap: 5, segMax: 3, shuffle: true}
	right := &fakeRemote{data: b, version: "r1", respCap: 4, segMax: 2, shuffle: true}
	res, err := CompareRemote(context.Background(), left, right, 8)
	if err != nil || !res.Complete {
		t.Fatalf("%+v err=%v", res, err)
	}
	wantD, wantM := modelCompare(a, b)
	if !intervalsEqual(res.Diffs, wantD) || !missingEqual(res.Missing, wantM) {
		t.Fatalf("diffs %v missing %+v, want %v %+v", res.Diffs, res.Missing, wantD, wantM)
	}
	if res.Missing == nil || res.Missing.Side != 1 {
		t.Fatalf("missing tail should be on the right side: %+v", res.Missing)
	}
}
