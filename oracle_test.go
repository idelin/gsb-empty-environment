package gifcompose

import (
	"image"
	"image/color"
	"image/gif"
	"testing"
)

// oracleRender is an independent reference implementation written directly on
// pixel buffers instead of reusing draw.Draw or the package's engine. Tests
// compare its output against both the sequential and random-access code paths.
func oracleRender(g *gif.GIF) []*image.NRGBA {
	width, height := g.Config.Width, g.Config.Height
	if width <= 0 || height <= 0 {
		first := g.Image[0].Bounds()
		if width <= 0 {
			width = first.Max.X
		}
		if height <= 0 {
			height = first.Max.Y
		}
	}
	screen := image.Rect(0, 0, width, height)
	canvas := image.NewNRGBA(screen)

	frames := make([]*image.NRGBA, 0, len(g.Image))
	var saved *image.NRGBA
	for i, frame := range g.Image {
		disposal := byte(0)
		if i < len(g.Disposal) {
			disposal = g.Disposal[i]
		}
		if disposal == DisposalPrevious {
			saved = cloneNRGBA(canvas)
		}
		oracleDraw(canvas, frame, screen)
		frames = append(frames, cloneNRGBA(canvas))
		switch disposal {
		case DisposalBackground:
			oracleErase(canvas, frame.Bounds(), screen)
		case DisposalPrevious:
			canvas = cloneNRGBA(saved)
		}
	}
	return frames
}

// oracleDraw performs source-over blending of one paletted frame, clipped to
// the logical screen, using explicit pixel math.
func oracleDraw(canvas *image.NRGBA, frame *image.Paletted, screen image.Rectangle) {
	rect := frame.Bounds().Intersect(screen)
	if rect.Empty() {
		return
	}
	for y := rect.Min.Y; y < rect.Max.Y; y++ {
		for x := rect.Min.X; x < rect.Max.X; x++ {
			src := color.NRGBAModel.Convert(frame.At(x, y)).(color.NRGBA)
			if src.A == 0 {
				continue
			}
			dstOff := canvas.PixOffset(x, y)
			dst := color.NRGBA{
				R: canvas.Pix[dstOff],
				G: canvas.Pix[dstOff+1],
				B: canvas.Pix[dstOff+2],
				A: canvas.Pix[dstOff+3],
			}
			sa := int(src.A)
			da := int(dst.A)
			outA := sa + da*(255-sa)/255
			if outA == 0 {
				continue
			}
			blend := func(s, d uint8) uint8 {
				return uint8((int(s)*sa + int(d)*da*(255-sa)/255) / outA)
			}
			canvas.Pix[dstOff] = blend(src.R, dst.R)
			canvas.Pix[dstOff+1] = blend(src.G, dst.G)
			canvas.Pix[dstOff+2] = blend(src.B, dst.B)
			canvas.Pix[dstOff+3] = uint8(outA)
		}
	}
}

func oracleErase(canvas *image.NRGBA, rect, screen image.Rectangle) {
	rect = rect.Intersect(screen)
	for y := rect.Min.Y; y < rect.Max.Y; y++ {
		for x := rect.Min.X; x < rect.Max.X; x++ {
			off := canvas.PixOffset(x, y)
			canvas.Pix[off] = 0
			canvas.Pix[off+1] = 0
			canvas.Pix[off+2] = 0
			canvas.Pix[off+3] = 0
		}
	}
}

func assertNRGBAEqual(t *testing.T, name string, want, got *image.NRGBA) {
	t.Helper()
	if want.Rect != got.Rect {
		t.Fatalf("%s: bounds mismatch: want %v, got %v", name, want.Rect, got.Rect)
	}
	for i := range want.Pix {
		if want.Pix[i] != got.Pix[i] {
			y := i / want.Stride
			x := (i-y*want.Stride)/4 + want.Rect.Min.X
			y += want.Rect.Min.Y
			t.Fatalf("%s: pixel (%d,%d) mismatch: want %v, got %v",
				name, x, y, pixelAt(want, x, y), pixelAt(got, x, y))
		}
	}
}

func pixelAt(m *image.NRGBA, x, y int) color.NRGBA {
	off := m.PixOffset(x, y)
	return color.NRGBA{R: m.Pix[off], G: m.Pix[off+1], B: m.Pix[off+2], A: m.Pix[off+3]}
}

// palFrame builds a paletted frame block whose every pixel uses palette
// index fill. The palette's last entry is transparent so fill == len(colors)
// means a fully transparent frame; this keeps opaque fills starting at 0 like
// a normal GIF palette.
func palFrame(rect image.Rectangle, fill uint8, colors ...color.Color) *image.Paletted {
	pal := color.Palette(append(append(color.Palette(nil), colors...), color.NRGBA{}))
	m := image.NewPaletted(rect, pal)
	for i := range m.Pix {
		m.Pix[i] = fill
	}
	return m
}

var (
	red   = color.NRGBA{R: 255, A: 255}
	green = color.NRGBA{G: 255, A: 255}
	blue  = color.NRGBA{B: 255, A: 255}
	gray  = color.NRGBA{R: 128, G: 128, B: 128, A: 255}
)

// TestDisposalScenario hand-builds an animation exercising every disposal
// method, partial overlap, out-of-bounds clipping (including negative
// offsets), per-frame local palettes and transparent entries.
func TestDisposalScenario(t *testing.T) {
	const size = 4
	global := color.Palette{red, green, blue}
	g := &gif.GIF{
		Config: image.Config{ColorModel: global, Width: size, Height: size},
		Image: []*image.Paletted{
			palFrame(image.Rect(0, 0, 2, 2), 1, red, green),     // 0: do-not-dispose, green (green at 1)
			palFrame(image.Rect(1, 1, 3, 3), 1, blue, red),      // 1: background, red (red at 1)
			palFrame(image.Rect(2, 2, 5, 5), 1, green, blue),    // 2: previous, blue; clipped (blue at 1)
			palFrame(image.Rect(-1, -1, 1, 1), 1, gray, green),  // 3: do-not-dispose, green; negative (green at 1)
			palFrame(image.Rect(1, 0, 3, 1), 1, red, blue),      // 4: do-not-dispose, blue strip (blue at 1)
			palFrame(image.Rect(10, 10, 12, 12), 1, red, green), // 5: fully outside, background
			palFrame(image.Rect(0, 0, 2, 2), 1, red),            // 6: all-transparent frame (only entry is transparent)
		},
		Delay:    make([]int, 7),
		Disposal: []byte{DisposalDoNotDispose, DisposalBackground, DisposalPrevious, DisposalDoNotDispose, DisposalDoNotDispose, DisposalBackground, DisposalNone},
	}

	want := oracleRender(g)

	seq, err := NewSequencer(g)
	if err != nil {
		t.Fatalf("NewSequencer: %v", err)
	}
	var seqFrames []*image.NRGBA
	for {
		frame, err := seq.Next()
		if err != nil {
			break
		}
		seqFrames = append(seqFrames, frame)
	}
	if len(seqFrames) != len(want) {
		t.Fatalf("sequencer produced %d frames, want %d", len(seqFrames), len(want))
	}

	comp, err := NewCompositor(g)
	if err != nil {
		t.Fatalf("NewCompositor: %v", err)
	}
	for i := range want {
		assertNRGBAEqual(t, "sequencer frame "+itoa(i), want[i], seqFrames[i])
		got, err := comp.Frame(i)
		if err != nil {
			t.Fatalf("Frame(%d): %v", i, err)
		}
		assertNRGBAEqual(t, "random frame "+itoa(i), want[i], got)
	}

	// Hand-checked canvas points for the trickiest transitions.
	// Frame 1: frame 0 stays visible; the background-disposed red block is on
	// screen while frame 1 is displayed.
	if pixelAt(want[1], 0, 0) != green {
		t.Fatalf("frame 1 (0,0): want green, got %v", pixelAt(want[1], 0, 0))
	}
	if pixelAt(want[1], 1, 1) != red {
		t.Fatalf("frame 1 (1,1): want red, got %v", pixelAt(want[1], 1, 1))
	}
	// After frame 1's disposal its rectangle (including 1,1) is transparent
	// again when frame 2 is displayed.
	if pixelAt(want[2], 1, 1) != (color.NRGBA{}) {
		t.Fatalf("frame 2 (1,1): want transparent, got %v", pixelAt(want[2], 1, 1))
	}
	// Frame 2 uses disposal previous: frame 3's canvas must not retain the
	// clipped blue block, but frame 0's green corner must survive.
	if pixelAt(want[3], 3, 3) != (color.NRGBA{}) {
		t.Fatalf("frame 3 (3,3): want transparent, got %v", pixelAt(want[3], 3, 3))
	}
	if pixelAt(want[3], 0, 0) != green {
		t.Fatalf("frame 3 (0,0): want green, got %v", pixelAt(want[3], 0, 0))
	}
	// The fully outside frame changes nothing.
	assertNRGBAEqual(t, "out-of-bounds frame", want[4], want[5])
	// The all-transparent frame changes nothing either.
	assertNRGBAEqual(t, "transparent frame", want[5], want[6])
}

// TestLocalPaletteIsolation verifies that a frame's local palette and
// transparent index only influence that frame and cannot bleed into frames
// that reuse the same palette slice with a different transparent index.
func TestLocalPaletteIsolation(t *testing.T) {
	shared := color.Palette{red, green, blue}
	frameA := image.NewPaletted(image.Rect(0, 0, 2, 2), shared)
	frameB := image.NewPaletted(image.Rect(0, 0, 2, 2), shared)
	// Local copies: frame A treats index 0 as transparent, frame B treats
	// index 1 as transparent.
	palA := append(color.Palette(nil), shared...)
	palA[0] = color.NRGBA{}
	frameA.Palette = palA
	palB := append(color.Palette(nil), shared...)
	palB[1] = color.NRGBA{}
	frameB.Palette = palB
	for i := range frameA.Pix {
		frameA.Pix[i] = 0
		frameB.Pix[i] = 1
	}
	g := &gif.GIF{
		Config: image.Config{ColorModel: shared, Width: 2, Height: 2},
		Image:  []*image.Paletted{frameA, frameB},
		Delay:  []int{0, 0},
	}
	want := oracleRender(g)
	comp, _ := NewCompositor(g)
	got, err := comp.Frame(1)
	if err != nil {
		t.Fatalf("Frame(1): %v", err)
	}
	assertNRGBAEqual(t, "local palette", want[1], got)
	if pixelAt(got, 0, 0) != (color.NRGBA{}) {
		t.Fatalf("frame B pixel: want transparent, got %v", pixelAt(got, 0, 0))
	}
	// The shared palette must be untouched by rendering.
	if shared[0] != red || shared[1] != green {
		t.Fatalf("shared palette mutated: %v", shared)
	}
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var buf [20]byte
	pos := len(buf)
	for i > 0 {
		pos--
		buf[pos] = byte('0' + i%10)
		i /= 10
	}
	return string(buf[pos:])
}
