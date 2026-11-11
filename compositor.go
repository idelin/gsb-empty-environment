package gifcompose

import (
	"image"
	"image/gif"
	"sync"
)

// Stats reports Compositor cache activity since creation or the last
// ClearCache call.
type Stats struct {
	// FramesRendered is the number of frame render operations performed while
	// replaying from a cached complete frame to the requested frame.
	FramesRendered int
	// CacheHits is the number of Frame calls that started from a cached
	// complete frame instead of the transparent initial canvas.
	CacheHits int
	// CachedFrames is the number of complete-frame snapshots currently held.
	CachedFrames int
}

// Compositor supports random access to any composited frame. When frame n is
// requested, rendering restarts from the nearest earlier frame whose
// post-render canvas is self-contained and reusable (disposal 0 or 1), or
// from the transparent initial canvas when none exists. Reusable canvases
// reached during replay are cached so later requests can jump to the closest
// one instead of replaying the whole animation.
type Compositor struct {
	mu sync.Mutex
	engine

	// cache maps a frame index to the complete canvas produced after that
	// frame was rendered (and before its disposal was applied). Only frames
	// whose disposal leaves the canvas reusable are stored.
	cache map[int]*image.NRGBA
	stats Stats
}

// NewCompositor returns a random-access compositor over g. The GIF must
// outlive the compositor and must not be mutated while it is in use.
func NewCompositor(g *gif.GIF) (*Compositor, error) {
	e, err := newEngine(g)
	if err != nil {
		return nil, err
	}
	return &Compositor{
		engine: *e,
		cache:  make(map[int]*image.NRGBA),
	}, nil
}

// Len reports the number of frame blocks in the GIF.
func (c *Compositor) Len() int {
	return len(c.g.Image)
}

// Frame composites and returns frame n (0-based). It always returns an
// independent copy of the rendered frame.
func (c *Compositor) Frame(n int) (*image.NRGBA, error) {
	if n < 0 || n >= c.Len() {
		return nil, ErrFrameIndex
	}
	c.mu.Lock()
	defer c.mu.Unlock()

	start := c.nearestCached(n)
	if start >= 0 {
		c.restore(c.cache[start])
		c.stats.CacheHits++
		if start == n {
			return c.snapshot(), nil
		}
	} else {
		c.reset()
	}

	// Replay frames strictly after the cached boundary. The displayed canvas
	// is snapshotted before teardown; reusable boundaries are cached for
	// subsequent requests, then the frame's own disposal prepares the canvas
	// for the next frame.
	var rendered *image.NRGBA
	for i := start + 1; i <= n; i++ {
		c.renderFrame(i)
		c.stats.FramesRendered++
		if i == n {
			rendered = c.snapshot()
		}
		if reusable(c.disposal(i)) {
			c.cacheFrame(i)
		}
		if i < n {
			c.applyDisposal(i)
		}
	}
	return rendered, nil
}

// nearestCached returns the largest reusable cached frame index not greater
// than n, or -1 when the replay must start from the transparent canvas.
func (c *Compositor) nearestCached(n int) int {
	best := -1
	for i := range c.cache {
		if i <= n && i > best {
			best = i
		}
	}
	return best
}

// cacheFrame stores an independent snapshot of the current canvas for frame i.
func (c *Compositor) cacheFrame(i int) {
	if _, ok := c.cache[i]; !ok {
		c.cache[i] = c.snapshot()
	}
}

// ClearCache drops every cached complete frame and resets the activity
// counters.
func (c *Compositor) ClearCache() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.cache = make(map[int]*image.NRGBA)
	c.stats = Stats{}
}

// SnapshotStats returns a copy of the current cache activity counters.
func (c *Compositor) SnapshotStats() Stats {
	c.mu.Lock()
	defer c.mu.Unlock()
	stats := c.stats
	stats.CachedFrames = len(c.cache)
	return stats
}
