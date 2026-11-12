package oit

import (
	"errors"
	"math"
	"math/rand"
	"sort"
	"sync"
	"testing"
)

// naiveReference is an independent one-shot implementation used to validate
// the Compositor: group fragments per pixel, sort by (depth, PrimID, color
// bits), blend front-to-back.
func naiveReference(w, h int, fs []Fragment) []Pixel {
	byPix := make(map[int][]Fragment)
	for _, f := range fs {
		p := int(f.Y)*w + int(f.X)
		byPix[p] = append(byPix[p], f)
	}
	dst := make([]Pixel, w*h)
	for p, run := range byPix {
		sort.Slice(run, func(i, j int) bool {
			a, b := run[i], run[j]
			if a.Depth != b.Depth {
				return a.Depth < b.Depth
			}
			if a.PrimID != b.PrimID {
				return a.PrimID < b.PrimID
			}
			if a.R != b.R {
				return math.Float32bits(a.R) < math.Float32bits(b.R)
			}
			if a.G != b.G {
				return math.Float32bits(a.G) < math.Float32bits(b.G)
			}
			if a.B != b.B {
				return math.Float32bits(a.B) < math.Float32bits(b.B)
			}
			return math.Float32bits(a.A) < math.Float32bits(b.A)
		})
		dst[p] = Composite(run)
	}
	return dst
}

func requireEqualPixels(t *testing.T, name string, got, want []Pixel) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s: len %d != %d", name, len(got), len(want))
	}
	for i := range got {
		if got[i] != want[i] {
			t.Fatalf("%s: pixel %d = %+v, want %+v", name, i, got[i], want[i])
		}
	}
}

// intersectingScene returns two full-screen transparent quads whose depths
// cross at the horizontal midpoint, so no per-primitive order is correct.
func intersectingScene(w, h int) []Fragment {
	var fs []Fragment
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			fx := float32(x) / float32(w-1)
			fs = append(fs,
				Fragment{X: int32(x), Y: int32(y), R: 0.6, G: 0.1, B: 0.1, A: 0.6,
					Depth: 0.2 + 0.6*fx, PrimID: 1},
				Fragment{X: int32(x), Y: int32(y), R: 0.1, G: 0.1, B: 0.6, A: 0.6,
					Depth: 0.8 - 0.6*fx, PrimID: 2},
			)
		}
	}
	return fs
}

func TestIntersectingSurfacesMatchReference(t *testing.T) {
	const w, h = 32, 24
	fs := intersectingScene(w, h)

	c := New(w, h, len(fs))
	if _, err := c.AddAll(fs); err != nil {
		t.Fatalf("AddAll: %v", err)
	}
	got := make([]Pixel, w*h)
	if err := c.Resolve(got); err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	requireEqualPixels(t, "compositor vs naive reference", got, naiveReference(w, h, fs))

	// Sanity: the per-pixel result must actually differ from the classic
	// buggy approach of sorting whole primitives by centroid depth, proving
	// the test scene exercises the intersection.
	avg := map[uint64]float32{}
	for _, f := range fs {
		avg[f.PrimID] += f.Depth
	}
	for id := range avg {
		avg[id] /= float32(w * h)
	}
	centroidOrdered := append([]Fragment(nil), fs...)
	sort.SliceStable(centroidOrdered, func(i, j int) bool {
		a, b := centroidOrdered[i], centroidOrdered[j]
		if avg[a.PrimID] != avg[b.PrimID] {
			return avg[a.PrimID] < avg[b.PrimID]
		}
		return a.PrimID < b.PrimID
	})
	byPix := make(map[int][]Fragment)
	for _, f := range centroidOrdered {
		p := int(f.Y)*w + int(f.X)
		byPix[p] = append(byPix[p], f)
	}
	diff := 0
	for p, run := range byPix {
		if Composite(run) != got[p] {
			diff++
		}
	}
	if diff == 0 {
		t.Fatal("centroid-sorted compositing unexpectedly matches per-pixel sort; scene does not exercise the bug")
	}
	t.Logf("centroid sort mismatches per-pixel sort at %d/%d pixels", diff, w*h)
}

func TestSameDepthOrderedByPrimID(t *testing.T) {
	c := New(8, 8, 4)
	// Same pixel, identical depth: PrimID 3 must blend before PrimID 7.
	near := Fragment{X: 3, Y: 2, R: 0, G: 0.5, B: 0, A: 0.5, Depth: 0.5, PrimID: 3}
	far := Fragment{X: 3, Y: 2, R: 0.4, G: 0, B: 0, A: 0.4, Depth: 0.5, PrimID: 7}
	// Deliberately offer the larger ID first.
	if err := c.Add(far); err != nil {
		t.Fatal(err)
	}
	if err := c.Add(near); err != nil {
		t.Fatal(err)
	}
	dst := make([]Pixel, 64)
	if err := c.Resolve(dst); err != nil {
		t.Fatal(err)
	}
	var want Pixel
	tr := float32(1)
	for _, f := range []Fragment{near, far} {
		want.R += tr * f.R
		want.G += tr * f.G
		want.B += tr * f.B
		want.A += tr * f.A
		tr *= 1 - f.A
	}
	if got := dst[2*8+3]; got != want {
		t.Fatalf("got %+v, want %+v (ID-ordered front-to-back)", got, want)
	}
}

func randomScene(r *rand.Rand, w, h, n int) []Fragment {
	fs := make([]Fragment, n)
	for i := range fs {
		fs[i] = Fragment{
			X:      int32(r.Intn(w)),
			Y:      int32(r.Intn(h)),
			R:      r.Float32() * 0.8,
			G:      r.Float32() * 0.8,
			B:      r.Float32() * 0.8,
			A:      r.Float32(),
			Depth:  float32(r.Intn(8)) / 8, // few distinct depths -> many ties
			PrimID: uint64(r.Intn(16)),
		}
	}
	return fs
}

func TestInputPermutationInvariant(t *testing.T) {
	const w, h = 16, 16
	base := randomScene(rand.New(rand.NewSource(42)), w, h, 2000)
	want := naiveReference(w, h, base)
	for seed := int64(1); seed <= 8; seed++ {
		fs := append([]Fragment(nil), base...)
		rand.New(rand.NewSource(seed)).Shuffle(len(fs), func(i, j int) { fs[i], fs[j] = fs[j], fs[i] })
		c := New(w, h, len(fs))
		if _, err := c.AddAll(fs); err != nil {
			t.Fatalf("seed %d: %v", seed, err)
		}
		got := make([]Pixel, w*h)
		if err := c.Resolve(got); err != nil {
			t.Fatalf("seed %d: %v", seed, err)
		}
		requireEqualPixels(t, "permuted input", got, want)
	}
}

func TestTiledParallelMatchesSequentialAndReference(t *testing.T) {
	const w, h = 64, 48
	fs := randomScene(rand.New(rand.NewSource(7)), w, h, 12000)
	c := New(w, h, len(fs))
	if _, err := c.AddAll(fs); err != nil {
		t.Fatal(err)
	}
	seq := make([]Pixel, w*h)
	if err := c.Resolve(seq); err != nil {
		t.Fatal(err)
	}
	for _, cfg := range []struct{ tile, workers int }{{1, 4}, {8, 4}, {16, 8}, {0, 0}, {100, 3}} {
		got := make([]Pixel, w*h)
		if err := c.ResolveTiled(got, cfg.tile, cfg.workers); err != nil {
			t.Fatalf("tile=%d workers=%d: %v", cfg.tile, cfg.workers, err)
		}
		requireEqualPixels(t, "tiled vs sequential", got, seq)
	}
	ref, err := Reference(w, h, fs)
	if err != nil {
		t.Fatal(err)
	}
	requireEqualPixels(t, "sequential vs one-shot reference", seq, ref)
	requireEqualPixels(t, "sequential vs naive reference", seq, naiveReference(w, h, fs))
}

func TestConcurrentStreamingProducers(t *testing.T) {
	const w, h = 32, 32
	fs := randomScene(rand.New(rand.NewSource(99)), w, h, 8000)
	c := New(w, h, len(fs))
	// Partition fragments into 4 tile-like streams fed from 4 goroutines.
	var wg sync.WaitGroup
	errs := make([]error, 4)
	for k := 0; k < 4; k++ {
		wg.Add(1)
		go func(k int) {
			defer wg.Done()
			for i := k; i < len(fs); i += 4 {
				if err := c.Add(fs[i]); err != nil {
					errs[k] = err
					return
				}
			}
		}(k)
	}
	wg.Wait()
	for _, err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	got := make([]Pixel, w*h)
	if err := c.ResolveTiled(got, 8, 4); err != nil {
		t.Fatal(err)
	}
	requireEqualPixels(t, "concurrent stream vs naive reference", got, naiveReference(w, h, fs))
}

func TestStreamChannel(t *testing.T) {
	const w, h = 16, 16
	fs := randomScene(rand.New(rand.NewSource(5)), w, h, 500)
	c := New(w, h, len(fs))
	ch := make(chan Fragment, 16)
	go func() {
		for _, f := range fs {
			ch <- f
		}
		close(ch)
	}()
	if err := c.Stream(ch); err != nil {
		t.Fatal(err)
	}
	got := make([]Pixel, w*h)
	if err := c.Resolve(got); err != nil {
		t.Fatal(err)
	}
	requireEqualPixels(t, "streamed vs naive reference", got, naiveReference(w, h, fs))
}

func TestBudgetExhaustionReportsIncomplete(t *testing.T) {
	const w, h = 8, 8
	fs := randomScene(rand.New(rand.NewSource(1)), w, h, 10)

	c := New(w, h, 7)
	n, err := c.AddAll(fs)
	if !errors.Is(err, ErrIncomplete) {
		t.Fatalf("AddAll err = %v, want ErrIncomplete", err)
	}
	if n != 7 || c.Fragments() != 7 {
		t.Fatalf("accepted %d fragments (stored %d), want exactly 7: budget must not silently drop", n, c.Fragments())
	}
	if c.Complete() {
		t.Fatal("Complete() = true after overflow")
	}
	// Further adds keep failing and nothing more is stored.
	if err := c.Add(fs[0]); !errors.Is(err, ErrIncomplete) {
		t.Fatalf("post-overflow Add err = %v, want ErrIncomplete", err)
	}
	if c.Fragments() != 7 {
		t.Fatal("fragment count changed after rejected Add")
	}
	dst := make([]Pixel, w*h)
	if err := c.Resolve(dst); !errors.Is(err, ErrIncomplete) {
		t.Fatalf("Resolve err = %v, want ErrIncomplete", err)
	}

	// An exactly-sized budget completes cleanly and matches the reference.
	full := New(w, h, len(fs))
	if _, err := full.AddAll(fs); err != nil {
		t.Fatal(err)
	}
	got := make([]Pixel, w*h)
	if err := full.Resolve(got); err != nil {
		t.Fatal(err)
	}
	requireEqualPixels(t, "exact budget vs naive reference", got, naiveReference(w, h, fs))
}

func TestOutOfBounds(t *testing.T) {
	c := New(4, 4, 8)
	for _, f := range []Fragment{
		{X: -1, Y: 0}, {X: 0, Y: -1}, {X: 4, Y: 0}, {X: 0, Y: 4},
	} {
		if err := c.Add(f); !errors.Is(err, ErrOutOfBounds) {
			t.Fatalf("Add(%+v) err = %v, want ErrOutOfBounds", f, err)
		}
	}
	if c.Fragments() != 0 {
		t.Fatal("out-of-bounds fragments were stored")
	}
}

func TestEmptyAndDstValidation(t *testing.T) {
	c := New(4, 4, 0)
	dst := make([]Pixel, 16)
	if err := c.Resolve(dst); err != nil {
		t.Fatal(err)
	}
	requireEqualPixels(t, "empty", dst, make([]Pixel, 16))
	if err := c.Resolve(make([]Pixel, 15)); err == nil {
		t.Fatal("expected error for wrong dst length")
	}
}
