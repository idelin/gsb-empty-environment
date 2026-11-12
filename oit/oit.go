// Package oit implements per-pixel exact transparency compositing for an
// offscreen renderer. Instead of sorting transparent triangles by their
// centroid (which produces wrong occlusion when surfaces intersect), all
// rasterized fragments are collected, sorted per pixel by (depth, primitive
// ID) and composited front-to-back with premultiplied alpha. The result is
// exact: no weighted-average approximations are used, and exhausting the
// fragment budget is reported as an incomplete result rather than silently
// dropping fragments.
package oit

import (
	"errors"
	"fmt"
	"math"
	"runtime"
	"sort"
	"sync"
	"sync/atomic"
)

// ErrIncomplete is returned when the fragment budget was exhausted. Fragments
// that did not fit are never stored, so a result carrying this error is
// known-incomplete rather than silently wrong.
var ErrIncomplete = errors.New("oit: fragment budget exhausted, result incomplete")

// ErrOutOfBounds is returned for fragments outside the framebuffer.
var ErrOutOfBounds = errors.New("oit: fragment outside framebuffer")

// Fragment is a single rasterized transparent fragment.
type Fragment struct {
	X, Y int32
	// R, G, B, A are linear, premultiplied-alpha color components.
	R, G, B, A float32
	// Depth is the fragment depth; smaller values are nearer the viewer.
	Depth float32
	// PrimID is a stable primitive identifier used as the sort tiebreaker
	// for equal depths.
	PrimID uint64
}

// Pixel is a linear, premultiplied-alpha composited pixel.
type Pixel struct{ R, G, B, A float32 }

// numShards is the number of independently locked fragment stores, so
// producers on different tiles can stream fragments concurrently.
const numShards = 64

type shard struct {
	mu    sync.Mutex
	frags []Fragment
}

// Compositor accumulates fragments for a width x height framebuffer within a
// fixed fragment budget and composites them exactly. It is safe for
// concurrent use: streaming producers may call Add from multiple goroutines.
type Compositor struct {
	w, h     int
	cap      int64
	count    atomic.Int64
	overflow atomic.Bool
	shards   [numShards]shard
}

// New returns a Compositor for a width x height framebuffer that stores at
// most capacity fragments.
func New(width, height, capacity int) *Compositor {
	if width <= 0 || height <= 0 {
		panic(fmt.Sprintf("oit: invalid framebuffer size %dx%d", width, height))
	}
	if capacity < 0 {
		panic(fmt.Sprintf("oit: negative capacity %d", capacity))
	}
	return &Compositor{w: width, h: height, cap: int64(capacity)}
}

// Width returns the framebuffer width in pixels.
func (c *Compositor) Width() int { return c.w }

// Height returns the framebuffer height in pixels.
func (c *Compositor) Height() int { return c.h }

// Add streams one fragment into the compositor. It returns ErrOutOfBounds for
// fragments outside the framebuffer and ErrIncomplete when the fragment
// budget is exhausted; in the latter case the fragment is not stored and the
// compositor is permanently flagged incomplete.
func (c *Compositor) Add(f Fragment) error {
	if f.X < 0 || int(f.X) >= c.w || f.Y < 0 || int(f.Y) >= c.h {
		return fmt.Errorf("%w: (%d,%d) not in %dx%d", ErrOutOfBounds, f.X, f.Y, c.w, c.h)
	}
	if c.count.Add(1) > c.cap {
		c.count.Add(-1)
		c.overflow.Store(true)
		return ErrIncomplete
	}
	sh := &c.shards[uint(c.pix(f.X, f.Y))%numShards]
	sh.mu.Lock()
	sh.frags = append(sh.frags, f)
	sh.mu.Unlock()
	return nil
}

// AddAll streams a batch of fragments, stopping at the first error. It
// returns the number of fragments accepted.
func (c *Compositor) AddAll(fs []Fragment) (int, error) {
	for i, f := range fs {
		if err := c.Add(f); err != nil {
			return i, err
		}
	}
	return len(fs), nil
}

// Stream consumes fragments from ch until the channel is closed or an error
// occurs. It is the streaming-input counterpart of Add.
func (c *Compositor) Stream(ch <-chan Fragment) error {
	for f := range ch {
		if err := c.Add(f); err != nil {
			return err
		}
	}
	return nil
}

// Complete reports whether every offered fragment fit within the budget.
func (c *Compositor) Complete() bool { return !c.overflow.Load() }

// Fragments returns the number of stored fragments.
func (c *Compositor) Fragments() int { return int(c.count.Load()) }

func (c *Compositor) pix(x, y int32) int { return int(y)*c.w + int(x) }

// gather collects all shards and sorts fragments into a total order: by
// pixel, then depth (near to far), then stable primitive ID, then color bits
// so the order is fully deterministic and independent of input arrival order.
func (c *Compositor) gather() []Fragment {
	all := make([]Fragment, 0, c.count.Load())
	for i := range c.shards {
		sh := &c.shards[i]
		sh.mu.Lock()
		all = append(all, sh.frags...)
		sh.mu.Unlock()
	}
	sort.Slice(all, func(i, j int) bool { return c.less(all[i], all[j]) })
	return all
}

func (c *Compositor) less(a, b Fragment) bool {
	pa, pb := c.pix(a.X, a.Y), c.pix(b.X, b.Y)
	if pa != pb {
		return pa < pb
	}
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
}

// Composite blends a run of fragments that share a pixel and are already
// sorted near-to-far, accumulating premultiplied color front-to-back.
func Composite(fs []Fragment) Pixel {
	var acc Pixel
	t := float32(1)
	for i := range fs {
		f := &fs[i]
		acc.R += t * f.R
		acc.G += t * f.G
		acc.B += t * f.B
		acc.A += t * f.A
		t *= 1 - f.A
	}
	return acc
}

// Resolve composites the framebuffer sequentially into dst (len must be
// width*height, row-major). It returns ErrIncomplete if the fragment budget
// was exhausted while streaming; dst still holds the composited result of the
// fragments that were stored, but it is flagged as known-incomplete.
func (c *Compositor) Resolve(dst []Pixel) error {
	return c.ResolveTiled(dst, 0, 1)
}

// ResolveTiled composites the framebuffer into dst, processing independent
// tileSize x tileSize tiles on up to workers goroutines. tileSize <= 0 uses a
// single tile spanning the whole framebuffer; workers <= 0 uses
// runtime.NumCPU(). Because pixels are independent, the output is
// bit-identical to Resolve regardless of tiling or worker count.
func (c *Compositor) ResolveTiled(dst []Pixel, tileSize, workers int) error {
	if len(dst) != c.w*c.h {
		return fmt.Errorf("oit: dst has %d pixels, want %d", len(dst), c.w*c.h)
	}
	for i := range dst {
		dst[i] = Pixel{}
	}
	sorted := c.gather()
	if tileSize <= 0 {
		tileSize = c.w
		if c.h > tileSize {
			tileSize = c.h
		}
	}
	type tile struct{ x0, y0, x1, y1 int }
	var tiles []tile
	for y := 0; y < c.h; y += tileSize {
		for x := 0; x < c.w; x += tileSize {
			t := tile{x0: x, y0: y, x1: x + tileSize, y1: y + tileSize}
			if t.x1 > c.w {
				t.x1 = c.w
			}
			if t.y1 > c.h {
				t.y1 = c.h
			}
			tiles = append(tiles, t)
		}
	}
	work := func(t tile) {
		// Fragments are sorted by pixel index. A tile narrower than the
		// framebuffer owns several contiguous row segments, so walk the
		// sorted slice row segment by row segment, skipping fragments that
		// belong to neighbouring tiles.
		idx := sort.Search(len(sorted), func(i int) bool {
			return c.pix(sorted[i].X, sorted[i].Y) >= t.y0*c.w+t.x0
		})
		for py := t.y0; py < t.y1; py++ {
			segStart := py*c.w + t.x0
			for idx < len(sorted) && c.pix(sorted[idx].X, sorted[idx].Y) < segStart {
				idx++
			}
			for px := t.x0; px < t.x1; px++ {
				p := py*c.w + px
				lo := idx
				for idx < len(sorted) && c.pix(sorted[idx].X, sorted[idx].Y) == p {
					idx++
				}
				if idx > lo {
					dst[p] = Composite(sorted[lo:idx])
				}
			}
		}
	}
	if workers <= 0 {
		workers = runtime.NumCPU()
	}
	if workers <= 1 || len(tiles) <= 1 {
		for _, t := range tiles {
			work(t)
		}
	} else {
		if workers > len(tiles) {
			workers = len(tiles)
		}
		ch := make(chan tile)
		var wg sync.WaitGroup
		for k := 0; k < workers; k++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for t := range ch {
					work(t)
				}
			}()
		}
		for _, t := range tiles {
			ch <- t
		}
		close(ch)
		wg.Wait()
	}
	if !c.Complete() {
		return ErrIncomplete
	}
	return nil
}

// Reference composites fragments in one shot — gather everything, sort once,
// blend — and returns the finished framebuffer. It is the ground-truth
// reference that streaming and tiled resolves must match pixel for pixel.
func Reference(width, height int, fs []Fragment) ([]Pixel, error) {
	c := New(width, height, len(fs))
	if _, err := c.AddAll(fs); err != nil {
		return nil, err
	}
	dst := make([]Pixel, width*height)
	if err := c.Resolve(dst); err != nil {
		return nil, err
	}
	return dst, nil
}
