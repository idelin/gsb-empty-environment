package gifcompose

import (
	"bytes"
	"image"
	"image/color"
	"image/gif"
	"math/rand"
	"testing"
)

const seed = 20261109

// randomGIF builds an in-memory GIF with random frame rectangles (frequently
// partly or fully outside the logical screen), random local palettes with
// per-frame transparent entries, and random disposal methods.
func randomGIF(rng *rand.Rand, width, height, n int) *gif.GIF {
	global := color.Palette{red, green, blue, gray}
	frames := make([]*image.Paletted, n)
	disposal := make([]byte, n)
	allMethods := []byte{DisposalNone, DisposalDoNotDispose, DisposalBackground, DisposalPrevious}

	for i := 0; i < n; i++ {
		w := 1 + rng.Intn(width+2)
		h := 1 + rng.Intn(height+2)
		// Allow offsets from -2 up to width-1, so roughly half the frames clip
		// an edge and some are entirely outside the screen.
		x0 := rng.Intn(width+4) - 2
		y0 := rng.Intn(height+4) - 2
		rect := image.Rect(x0, y0, x0+w, y0+h)

		locals := color.Palette{
			color.NRGBA{}, // index 0: frame-local transparent entry
			color.NRGBA{R: uint8(rng.Intn(256)), G: uint8(rng.Intn(256)), B: uint8(rng.Intn(256)), A: 255},
			color.NRGBA{R: uint8(rng.Intn(256)), G: uint8(rng.Intn(256)), B: uint8(rng.Intn(256)), A: 255},
			color.NRGBA{R: uint8(rng.Intn(256)), G: uint8(rng.Intn(256)), B: uint8(rng.Intn(256)), A: 255},
		}
		m := image.NewPaletted(rect, locals)
		for p := range m.Pix {
			if rng.Intn(4) == 0 {
				m.Pix[p] = 0 // transparent pixel
			} else {
				m.Pix[p] = uint8(1 + rng.Intn(len(locals)-1))
			}
		}
		frames[i] = m
		disposal[i] = allMethods[rng.Intn(len(allMethods))]
	}

	return &gif.GIF{
		Config:   image.Config{ColorModel: global, Width: width, Height: height},
		Image:    frames,
		Delay:    make([]int, n),
		Disposal: disposal,
	}
}

// TestRandomizedSequentialVersusRandom compares every frame produced by the
// streaming Sequencer, the random-access Compositor and the independent
// oracle across many random animations and access patterns.
func TestRandomizedSequentialVersusRandom(t *testing.T) {
	rng := rand.New(rand.NewSource(seed))
	for trial := 0; trial < 40; trial++ {
		width := 1 + rng.Intn(8)
		height := 1 + rng.Intn(8)
		n := 1 + rng.Intn(20)
		g := randomGIF(rng, width, height, n)
		want := oracleRender(g)

		seq, err := NewSequencer(g)
		if err != nil {
			t.Fatalf("trial %d: NewSequencer: %v", trial, err)
		}
		comp, err := NewCompositor(g)
		if err != nil {
			t.Fatalf("trial %d: NewCompositor: %v", trial, err)
		}

		for i := 0; i < n; i++ {
			seqFrame, err := seq.Next()
			if err != nil {
				t.Fatalf("trial %d: sequencer ended early at %d: %v", trial, i, err)
			}
			assertNRGBAEqual(t, "trial "+itoa(trial)+" seq "+itoa(i), want[i], seqFrame)

			// Query frames in shuffled order so later requests must jump
			// backward and forward using the keyframe cache.
			j := rng.Intn(n)
			randomFrame, err := comp.Frame(j)
			if err != nil {
				t.Fatalf("trial %d: Frame(%d): %v", trial, j, err)
			}
			assertNRGBAEqual(t, "trial "+itoa(trial)+" random "+itoa(j), want[j], randomFrame)
		}

		// Every frame requested once more in reverse order must still match.
		for i := n - 1; i >= 0; i-- {
			randomFrame, err := comp.Frame(i)
			if err != nil {
				t.Fatalf("trial %d: reverse Frame(%d): %v", trial, i, err)
			}
			assertNRGBAEqual(t, "trial "+itoa(trial)+" reverse "+itoa(i), want[i], randomFrame)
		}
	}
}

// TestCacheReplayBehavior verifies that random access restarts from the
// nearest reusable complete frame and never caches disposal 2/3 boundaries.
func TestCacheReplayBehavior(t *testing.T) {
	g := randomGIF(rand.New(rand.NewSource(seed+1)), 6, 6, 10)
	comp, err := NewCompositor(g)
	if err != nil {
		t.Fatalf("NewCompositor: %v", err)
	}

	if _, err := comp.Frame(len(g.Image)); err != ErrFrameIndex {
		t.Fatalf("out-of-range Frame: want ErrFrameIndex, got %v", err)
	}
	if _, err := comp.Frame(-1); err != ErrFrameIndex {
		t.Fatalf("negative Frame: want ErrFrameIndex, got %v", err)
	}

	// First request for the last frame replays every frame from the
	// transparent canvas.
	last := len(g.Image) - 1
	if _, err := comp.Frame(last); err != nil {
		t.Fatalf("Frame(%d): %v", last, err)
	}
	stats := comp.SnapshotStats()
	if stats.FramesRendered != len(g.Image) {
		t.Fatalf("first replay rendered %d frames, want %d", stats.FramesRendered, len(g.Image))
	}

	// Cached indices must be exactly the reusable frames reached during replay.
	var reusableCount int
	for i := range g.Image {
		if reusable(comp.disposal(i)) {
			reusableCount++
			if _, ok := comp.cache[i]; !ok {
				t.Fatalf("reusable frame %d missing from cache", i)
			}
		} else if _, ok := comp.cache[i]; ok {
			t.Fatalf("non-reusable frame %d must not be cached", i)
		}
	}
	if stats.CachedFrames != reusableCount {
		t.Fatalf("cached %d frames, want %d", stats.CachedFrames, reusableCount)
	}

	// A second request starts from the nearest earlier keyframe and only
	// replays the non-reusable tail, never the whole animation.
	before := comp.SnapshotStats()
	if _, err := comp.Frame(last); err != nil {
		t.Fatalf("second Frame(%d): %v", last, err)
	}
	after := comp.SnapshotStats()
	if rendered := after.FramesRendered - before.FramesRendered; rendered > tailLength(g, last) {
		t.Fatalf("cache replay rendered %d frames, want at most %d", rendered, tailLength(g, last))
	}
	if after.CacheHits <= before.CacheHits {
		t.Fatalf("expected a cache hit, before=%+v after=%+v", before, after)
	}

	comp.ClearCache()
	if stats := comp.SnapshotStats(); stats.CachedFrames != 0 || stats.FramesRendered != 0 {
		t.Fatalf("ClearCache did not reset stats: %+v", stats)
	}
}

// tailLength counts the consecutive non-reusable frames ending at n.
func tailLength(g *gif.GIF, n int) int {
	length := 0
	for i := n; i >= 0; i-- {
		d := byte(DisposalNone)
		if i < len(g.Disposal) {
			d = g.Disposal[i]
		}
		if !reusable(d) {
			length++
			continue
		}
		break
	}
	return length
}

// TestEncodeDecodeRoundTrip checks frames that survive the standard GIF
// encoder (in-bounds blocks, local palettes, transparency and disposal)
// composite identically after DecodeAll.
func TestEncodeDecodeRoundTrip(t *testing.T) {
	g := &gif.GIF{
		Config:   image.Config{ColorModel: color.Palette{red, green, blue}, Width: 4, Height: 4},
		Delay:    []int{10, 10, 10, 10},
		Disposal: []byte{DisposalDoNotDispose, DisposalBackground, DisposalPrevious, DisposalNone},
		Image: []*image.Paletted{
			palFrame(image.Rect(0, 0, 3, 3), 1, red, green),
			palFrame(image.Rect(1, 1, 4, 4), 1, green, blue),
			palFrame(image.Rect(0, 1, 2, 3), 1, blue, red),
			palFrame(image.Rect(0, 0, 4, 1), 1, red, green),
		},
	}
	var buf bytes.Buffer
	if err := gif.EncodeAll(&buf, g); err != nil {
		t.Fatalf("EncodeAll: %v", err)
	}
	decoded, err := gif.DecodeAll(bytes.NewReader(buf.Bytes()))
	if err != nil {
		t.Fatalf("DecodeAll: %v", err)
	}

	want, err := CompositeAll(g)
	if err != nil {
		t.Fatalf("CompositeAll original: %v", err)
	}
	got, err := CompositeAll(decoded)
	if err != nil {
		t.Fatalf("CompositeAll decoded: %v", err)
	}
	if len(want) != len(got) {
		t.Fatalf("frame count mismatch: %d vs %d", len(want), len(got))
	}
	for i := range want {
		assertNRGBAEqual(t, "roundtrip "+itoa(i), want[i], got[i])
	}
}

func TestErrorsAndConvenience(t *testing.T) {
	if _, err := NewCompositor(nil); err != ErrNoFrames {
		t.Fatalf("nil gif: want ErrNoFrames, got %v", err)
	}
	if _, err := NewSequencer(&gif.GIF{}); err != ErrNoFrames {
		t.Fatalf("empty gif: want ErrNoFrames, got %v", err)
	}
	if _, err := CompositeAll(nil); err != ErrNoFrames {
		t.Fatalf("CompositeAll nil: want ErrNoFrames, got %v", err)
	}

	g := &gif.GIF{
		Config: image.Config{Width: 2, Height: 2},
		Image: []*image.Paletted{
			palFrame(image.Rect(0, 0, 2, 2), 1, red, green),
		},
		Delay: []int{0},
	}
	all, err := CompositeAll(g)
	if err != nil {
		t.Fatalf("CompositeAll: %v", err)
	}
	if len(all) != 1 || all[0].Rect != image.Rect(0, 0, 2, 2) {
		t.Fatalf("unexpected CompositeAll result: %+v", all)
	}

	seq, _ := NewSequencer(g)
	if w, h := seq.ScreenSize(); w != 2 || h != 2 {
		t.Fatalf("ScreenSize = %d,%d, want 2,2", w, h)
	}
	first, err := seq.Next()
	if err != nil {
		t.Fatalf("Next: %v", err)
	}
	// Returned frames are independent copies: mutating one must not corrupt
	// later rendering or cached state.
	first.Pix[0] = 7
	if _, err := seq.Next(); err == nil {
		t.Fatalf("second Next on exhausted sequencer: want error")
	}
	seq.Reset()
	again, err := seq.Next()
	if err != nil {
		t.Fatalf("Next after Reset: %v", err)
	}
	if pixelAt(again, 0, 0) != green {
		t.Fatalf("reset frame corrupted: %v", pixelAt(again, 0, 0))
	}
}

// TestZeroConfigDerivesScreen mirrors image/gif.EncodeAll's zero-config
// behavior by deriving the logical screen from the first frame.
func TestZeroConfigDerivesScreen(t *testing.T) {
	g := &gif.GIF{
		Image: []*image.Paletted{
			palFrame(image.Rect(0, 0, 3, 2), 1, red, green),
		},
		Delay: []int{0},
	}
	comp, err := NewCompositor(g)
	if err != nil {
		t.Fatalf("NewCompositor: %v", err)
	}
	if w, h := comp.ScreenSize(); w != 3 || h != 2 {
		t.Fatalf("derived ScreenSize = %d,%d, want 3,2", w, h)
	}
	frame, err := comp.Frame(0)
	if err != nil {
		t.Fatalf("Frame(0): %v", err)
	}
	if frame.Rect != image.Rect(0, 0, 3, 2) {
		t.Fatalf("frame rect = %v", frame.Rect)
	}
}

// TestUnknownDisposalTreatedAsLeaveInPlace matches common browser behavior for
// reserved disposal values.
func TestUnknownDisposalTreatedAsLeaveInPlace(t *testing.T) {
	g := &gif.GIF{
		Config:   image.Config{ColorModel: color.Palette{red, green}, Width: 2, Height: 2},
		Image:    []*image.Paletted{palFrame(image.Rect(0, 0, 2, 2), 1, red, green)},
		Delay:    []int{0},
		Disposal: []byte{7},
	}
	want := oracleRender(g)
	comp, _ := NewCompositor(g)
	got, err := comp.Frame(0)
	if err != nil {
		t.Fatalf("Frame(0): %v", err)
	}
	assertNRGBAEqual(t, "unknown disposal", want[0], got)
	if stats := comp.SnapshotStats(); stats.CachedFrames != 1 {
		t.Fatalf("unknown disposal frame should be reusable, stats=%+v", stats)
	}
}
