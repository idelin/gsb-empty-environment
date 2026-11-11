// Package gifcompose composites the raw frame blocks of an animated GIF into
// complete logical-screen frames suitable for thumbnail extraction.
//
// image/gif only returns the individual frame blocks: each *image.Paletted
// carries its own rectangle, palette and transparent index, and says nothing
// about how the frame overlays the ones before it. This package implements the
// GIF89a rendering model on top of a canvas sized to the logical screen.
//
// Disposal semantics:
//   - DisposalNone (0) and DisposalDoNotDispose (1): the rendered frame stays
//     on the canvas and is reused by later frames.
//   - DisposalBackground (2): the frame's rectangle is restored to a fully
//     transparent background when the frame is done. GIF's background index
//     has no defined display color, so transparent is used.
//   - DisposalPrevious (3): the canvas is restored to the state it had
//     immediately before the frame was rendered.
//
// Frame rectangles are clipped to the logical screen, including partially or
// fully out-of-bounds rectangles. A frame's local palette and transparent
// color only affect that frame's own pixels. The initial canvas is fully
// transparent.
package gifcompose

import (
	"errors"
	"image"
	"image/draw"
	"image/gif"
)

// GIF disposal methods, matching the values stored in gif.GIF.Disposal and
// the image/gif constants.
const (
	// DisposalNone means that no disposal was specified. It behaves like
	// DisposalDoNotDispose.
	DisposalNone = gif.DisposalNone - 1 // 0
	// DisposalDoNotDispose leaves the frame in place (GIF value 1).
	DisposalDoNotDispose = gif.DisposalNone // 1
	// DisposalBackground restores the frame area to transparent (GIF value 2).
	DisposalBackground = gif.DisposalBackground // 2
	// DisposalPrevious restores the pre-frame canvas (GIF value 3).
	DisposalPrevious = gif.DisposalPrevious // 3
)

var (
	// ErrNoFrames is returned when a GIF contains no frame blocks.
	ErrNoFrames = errors.New("gifcompose: gif has no frames")
	// ErrFrameIndex is returned when a requested frame index is out of range.
	ErrFrameIndex = errors.New("gifcompose: frame index out of range")
)

// engine holds the decoded GIF and the mutable render state. The same render
// rules are shared by the sequential Sequencer and the random-access
// Compositor so that both code paths produce identical frames.
type engine struct {
	g      *gif.GIF
	screen image.Rectangle
	canvas *image.NRGBA
	// prev holds the canvas snapshot required by DisposalPrevious. It is only
	// valid while the current frame uses that disposal method.
	prev *image.NRGBA
}

func newEngine(g *gif.GIF) (*engine, error) {
	if g == nil || len(g.Image) == 0 {
		return nil, ErrNoFrames
	}
	width, height := g.Config.Width, g.Config.Height
	if width <= 0 || height <= 0 {
		// Mirror image/gif.EncodeAll: a zero config derives the logical screen
		// from the first frame's maximum point.
		first := g.Image[0].Bounds()
		if width <= 0 {
			width = first.Max.X
		}
		if height <= 0 {
			height = first.Max.Y
		}
	}
	if width <= 0 || height <= 0 {
		return nil, errors.New("gifcompose: non-positive logical screen size")
	}
	return &engine{
		g:      g,
		screen: image.Rect(0, 0, width, height),
		canvas: image.NewNRGBA(image.Rect(0, 0, width, height)),
	}, nil
}

// ScreenSize reports the logical-screen dimensions every composited frame is
// rendered at.
func (e *engine) ScreenSize() (width, height int) {
	return e.screen.Dx(), e.screen.Dy()
}

func (e *engine) disposal(i int) byte {
	if i < len(e.g.Disposal) {
		return e.g.Disposal[i]
	}
	return DisposalNone
}

// snapshot returns an independent copy of the current canvas.
func (e *engine) snapshot() *image.NRGBA {
	return cloneNRGBA(e.canvas)
}

// restore replaces the canvas pixels with the given snapshot. The snapshot is
// copied so callers may keep reusing their copy afterwards.
func (e *engine) restore(s *image.NRGBA) {
	copy(e.canvas.Pix, s.Pix)
}

// reset clears the canvas back to the fully transparent initial state and
// drops any pending previous-canvas snapshot.
func (e *engine) reset() {
	clear(e.canvas.Pix)
	e.prev = nil
}

// renderFrame composites frame i onto the canvas. A DisposalPrevious
// snapshot is taken before rendering when needed; the frame's own disposal is
// applied separately by applyDisposal so callers can observe the displayed
// frame before it is torn down.
func (e *engine) renderFrame(i int) {
	frame := e.g.Image[i]
	switch e.disposal(i) {
	case DisposalPrevious:
		e.prev = e.snapshot()
	}
	e.drawFrame(frame)
}

// applyDisposal performs frame i's teardown after the displayed frame has
// been observed, leaving the canvas where the next frame must start.
func (e *engine) applyDisposal(i int) {
	frame := e.g.Image[i]
	switch e.disposal(i) {
	case DisposalBackground:
		e.eraseArea(frame.Bounds())
	case DisposalPrevious:
		e.restore(e.prev)
		e.prev = nil
	}
}

// drawFrame overlays one frame block onto the canvas with source-over alpha
// blending. The frame rectangle is clipped to the logical screen, so
// partially or fully out-of-bounds frames are handled without touching pixels
// outside the canvas. Transparent palette entries are skipped because the
// frame's own palette gives them zero alpha.
func (e *engine) drawFrame(frame *image.Paletted) {
	bounds := frame.Bounds()
	if bounds.Intersect(e.screen).Empty() {
		return
	}
	// Pass the unclipped frame rectangle with a matching source point:
	// draw.Draw clips both the destination and source rectangles itself, which
	// keeps each canvas pixel aligned with the same coordinate in the frame
	// block even when the frame starts at a negative left/top offset.
	draw.Draw(e.canvas, bounds, frame, bounds.Min, draw.Over)
}

// eraseArea clears the intersection of rect and the logical screen to fully
// transparent pixels, implementing DisposalBackground.
func (e *engine) eraseArea(rect image.Rectangle) {
	rect = rect.Intersect(e.screen)
	if rect.Empty() {
		return
	}
	for y := rect.Min.Y; y < rect.Max.Y; y++ {
		start := e.canvas.PixOffset(rect.Min.X, y)
		clear(e.canvas.Pix[start : start+4*rect.Dx()])
	}
}

// reusable reports whether the canvas state after frame i was rendered is a
// valid starting point for rendering later frames. Only "leave in place"
// frames (disposal 0/1) qualify: disposal 2 requires remembering the area to
// erase and disposal 3 requires the pre-frame canvas, so their boundary
// states cannot be reused on their own.
//
// Values 4-7 are reserved by the spec; browsers treat them as "do not
// dispose", so they are reusable here as well.
func reusable(disposal byte) bool {
	return disposal != DisposalBackground && disposal != DisposalPrevious
}

func cloneNRGBA(src *image.NRGBA) *image.NRGBA {
	dst := image.NewNRGBA(src.Rect)
	copy(dst.Pix, src.Pix)
	return dst
}
