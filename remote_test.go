package diffcmp

import (
	"context"
	"crypto/sha256"
	"errors"
	"math/rand"
	"reflect"
	"sync"
	"testing"
)

// versionSwitch atomically replaces a fake source's contents and version
// once its FetchRange call count reaches atCall.
type versionSwitch struct {
	atCall  int
	data    []byte
	version string
}

// fakeRemote serves an in-memory entity in randomly sized, shuffled
// segments, sometimes covering less than requested (short read), with
// optional scheduled version switches and one-shot transient errors.
type fakeRemote struct {
	mu       sync.Mutex
	data     []byte
	version  string
	rng      *rand.Rand
	maxSeg   int64
	shortPct int // percent chance a response covers only part of the request
	switches []versionSwitch
	failAt   map[int]error
	calls    int
}

func (f *fakeRemote) FetchRange(ctx context.Context, offset, length int64) ([]Segment, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	if err, ok := f.failAt[f.calls]; ok {
		delete(f.failAt, f.calls)
		return nil, err
	}
	for len(f.switches) > 0 && f.calls >= f.switches[0].atCall {
		f.data = f.switches[0].data
		f.version = f.switches[0].version
		f.switches = f.switches[1:]
	}
	if offset >= int64(len(f.data)) || length <= 0 {
		return nil, nil
	}
	end := min(offset+length, int64(len(f.data)))
	cover := end
	if f.shortPct > 0 && end-offset > 1 && f.rng.Intn(100) < f.shortPct {
		cover = offset + 1 + f.rng.Int63n(end-offset)
	}
	var segs []Segment
	for p := offset; p < cover; {
		n := int64(1)
		if f.maxSeg > 1 {
			n = 1 + f.rng.Int63n(f.maxSeg)
		}
		if p+n > cover {
			n = cover - p
		}
		segs = append(segs, Segment{
			Offset:  p,
			Data:    append([]byte(nil), f.data[p:p+n]...),
			Version: f.version,
		})
		p += n
	}
	f.rng.Shuffle(len(segs), func(i, j int) { segs[i], segs[j] = segs[j], segs[i] })
	return segs, nil
}

func (f *fakeRemote) Version(ctx context.Context) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.version, nil
}

// drive runs CompareRemote to completion, resuming from the returned
// checkpoint after transient errors and version changes.
func drive(ctx context.Context, a, b RemoteSource, chunk int64) (Checkpoint, RemoteStats, int, error) {
	var cp *Checkpoint
	var total RemoteStats
	versionChanges := 0
	for i := 0; i < 200; i++ {
		res, st, err := CompareRemote(ctx, a, b, chunk, cp)
		total.ReusedDiffs += st.ReusedDiffs
		if err == nil {
			return res, total, versionChanges, nil
		}
		if errors.Is(err, ErrVersionChanged) {
			versionChanges++
		}
		resCopy := res
		cp = &resCopy
	}
	return Checkpoint{}, total, versionChanges, errors.New("drive: too many restarts")
}

// modelRemote derives the expected diff intervals and missing tail
// directly from the full final contents of both sources.
func modelRemote(a, b []byte) ([]Interval, *MissingTail) {
	n := min(len(a), len(b))
	diffs := runsOver(a[:n], b[:n])
	var tail *MissingTail
	if len(a) < len(b) {
		tail = &MissingTail{Side: 0, Start: int64(n), End: int64(len(b))}
	} else if len(b) < len(a) {
		tail = &MissingTail{Side: 1, Start: int64(n), End: int64(len(a))}
	}
	return diffs, tail
}

func intervalsOf(diffs []ConfirmedDiff) []Interval {
	var out []Interval
	for _, d := range diffs {
		out = append(out, d.Interval)
	}
	return out
}

func checkFinal(t *testing.T, cp Checkpoint, dataA, dataB []byte, verA, verB string) {
	t.Helper()
	if !cp.Done {
		t.Fatalf("comparison not done: %+v", cp)
	}
	wantDiffs, wantTail := modelRemote(dataA, dataB)
	if got := intervalsOf(cp.Diffs); !reflect.DeepEqual(got, wantDiffs) {
		t.Fatalf("diffs got %v want %v", got, wantDiffs)
	}
	if !reflect.DeepEqual(cp.Tail, wantTail) {
		t.Fatalf("tail got %+v want %+v", cp.Tail, wantTail)
	}
	for _, d := range cp.Diffs {
		if d.VersionA != verA || d.VersionB != verB {
			t.Fatalf("interval %v mixes versions: got (%q,%q) want (%q,%q)",
				d.Interval, d.VersionA, d.VersionB, verA, verB)
		}
		if sha256.Sum256(dataA[d.Start:d.End]) != d.DigestA {
			t.Fatalf("interval %v: digest A does not match final contents", d.Interval)
		}
		if sha256.Sum256(dataB[d.Start:d.End]) != d.DigestB {
			t.Fatalf("interval %v: digest B does not match final contents", d.Interval)
		}
	}
}

func TestRemoteBasicOutOfOrderShortReads(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	dataA := randBytes(rng, 400)
	dataB := append(mutate(rng, dataA, 0.1), randBytes(rng, 20)...)
	a := &fakeRemote{data: dataA, version: "vA", rng: rand.New(rand.NewSource(11)), maxSeg: 7, shortPct: 30}
	b := &fakeRemote{data: dataB, version: "vB", rng: rand.New(rand.NewSource(12)), maxSeg: 5, shortPct: 40}
	cp, _, changes, err := drive(context.Background(), a, b, 50)
	if err != nil {
		t.Fatal(err)
	}
	if changes != 0 {
		t.Fatalf("unexpected version changes: %d", changes)
	}
	checkFinal(t, cp, dataA, dataB, "vA", "vB")
}

func TestRemoteResumeAfterTransientErrors(t *testing.T) {
	// A diff run in [0,16) closes early; the rest is identical.
	tail := randBytes(rand.New(rand.NewSource(21)), 200)
	dataA := append([]byte{0xAA, 0xAA, 0xAA, 0xAA, 0xAA, 0xAA, 0xAA, 0xAA,
		0xAA, 0xAA, 0xAA, 0xAA, 0xAA, 0xAA, 0xAA, 0xAA}, tail...)
	dataB := append([]byte{0xBB, 0xBB, 0xBB, 0xBB, 0xBB, 0xBB, 0xBB, 0xBB,
		0xBB, 0xBB, 0xBB, 0xBB, 0xBB, 0xBB, 0xBB, 0xBB}, tail...)
	a := &fakeRemote{data: dataA, version: "vA", rng: rand.New(rand.NewSource(22)),
		maxSeg: 64, failAt: map[int]error{3: errInjected}}
	b := &fakeRemote{data: dataB, version: "vB", rng: rand.New(rand.NewSource(23)),
		maxSeg: 64, failAt: map[int]error{6: errInjected}}
	cp, stats, changes, err := drive(context.Background(), a, b, 32)
	if err != nil {
		t.Fatal(err)
	}
	if changes != 0 {
		t.Fatalf("unexpected version changes: %d", changes)
	}
	checkFinal(t, cp, dataA, dataB, "vA", "vB")
	if stats.ReusedDiffs == 0 {
		t.Fatalf("expected confirmed intervals to be reused across resumes")
	}
}

func TestRemoteResumeRebuildsOpenRun(t *testing.T) {
	// A diff run spanning the first two chunks; a transient error hits
	// while the run is still open.
	tail := randBytes(rand.New(rand.NewSource(31)), 80)
	dataA := append(make([]byte, 40), tail...)
	dataB := append(make([]byte, 40), tail...)
	for i := 0; i < 40; i++ {
		dataA[i] = 0xAA
		dataB[i] = 0xBB
	}
	a := &fakeRemote{data: dataA, version: "vA", rng: rand.New(rand.NewSource(32)),
		maxSeg: 64, failAt: map[int]error{2: errInjected}}
	b := &fakeRemote{data: dataB, version: "vB", rng: rand.New(rand.NewSource(33)), maxSeg: 64}
	cp, _, _, err := drive(context.Background(), a, b, 32)
	if err != nil {
		t.Fatal(err)
	}
	checkFinal(t, cp, dataA, dataB, "vA", "vB")
	want := []Interval{{Start: 0, End: 40}}
	if got := intervalsOf(cp.Diffs); !reflect.DeepEqual(got, want) {
		t.Fatalf("diffs got %v want %v", got, want)
	}
}

func TestRemoteVersionSwitchNeverSplicesVersions(t *testing.T) {
	rng := rand.New(rand.NewSource(41))
	dataA1 := randBytes(rng, 300)
	dataB1 := mutate(rng, dataA1, 0.1)
	dataA2 := randBytes(rng, 260) // new generation: different contents and length
	dataB2 := mutate(rng, dataA2, 0.1)
	a := &fakeRemote{data: dataA1, version: "vA1", rng: rand.New(rand.NewSource(42)),
		maxSeg: 9, shortPct: 40,
		switches: []versionSwitch{{atCall: 4, data: dataA2, version: "vA2"}}}
	b := &fakeRemote{data: dataB1, version: "vB1", rng: rand.New(rand.NewSource(43)),
		maxSeg: 8, shortPct: 40,
		switches: []versionSwitch{{atCall: 7, data: dataB2, version: "vB2"}}}
	cp, _, changes, err := drive(context.Background(), a, b, 48)
	if err != nil {
		t.Fatal(err)
	}
	if changes == 0 {
		t.Fatalf("expected at least one version change to be detected")
	}
	// The final conclusion must describe only the final generation and
	// every confirmed interval must carry the final version pair.
	checkFinal(t, cp, dataA2, dataB2, "vA2", "vB2")
}

func TestRemoteSilentMutationInvalidatesCheckpoint(t *testing.T) {
	// Diff run in [0,16); a transient error interrupts the pass, then the
	// entity is silently mutated (same version) inside the confirmed
	// interval. The digest check must reject the stale interval.
	tail := randBytes(rand.New(rand.NewSource(51)), 100)
	dataA := append([]byte{0xAA, 0xAA, 0xAA, 0xAA, 0xAA, 0xAA, 0xAA, 0xAA,
		0xAA, 0xAA, 0xAA, 0xAA, 0xAA, 0xAA, 0xAA, 0xAA}, tail...)
	dataB := append([]byte{0xBB, 0xBB, 0xBB, 0xBB, 0xBB, 0xBB, 0xBB, 0xBB,
		0xBB, 0xBB, 0xBB, 0xBB, 0xBB, 0xBB, 0xBB, 0xBB}, tail...)
	a := &fakeRemote{data: dataA, version: "vA", rng: rand.New(rand.NewSource(52)),
		maxSeg: 64, failAt: map[int]error{5: errInjected}}
	b := &fakeRemote{data: dataB, version: "vB", rng: rand.New(rand.NewSource(53)), maxSeg: 64}

	ctx := context.Background()
	cp1, _, err := CompareRemote(ctx, a, b, 32, nil)
	if err == nil {
		t.Fatalf("expected transient error")
	}
	if len(cp1.Diffs) == 0 {
		t.Fatalf("expected confirmed diffs before interruption")
	}
	// Silent mutation: byte 5 becomes equal on both sides, version kept.
	a.mu.Lock()
	a.data[5] = 0xBB
	a.mu.Unlock()
	mutatedA := append([]byte(nil), dataA...)
	mutatedA[5] = 0xBB

	cp2, stats, err := CompareRemote(ctx, a, b, 32, &cp1)
	if err != nil {
		t.Fatal(err)
	}
	if stats.ReusedDiffs != 0 {
		t.Fatalf("stale interval was reused despite digest mismatch")
	}
	checkFinal(t, cp2, mutatedA, dataB, "vA", "vB")
}
