// Package adaptivebatch implements an adaptive batch-size regulator for a
// vectorized execution engine.
//
// The controller observes per-batch feedback (row width, L1/L2 cache
// hit/miss counts, wall-clock duration) and adjusts the number of rows
// processed per batch so that a single batch stays resident in the CPU L1
// cache. Wide rows shrink the batch (down to MinBatchSize), narrow rows
// grow it (up to MaxBatchSize), keeping the cache hot and avoiding
// cache thrashing when the workload becomes memory-bandwidth bound.
package adaptivebatch

import "time"

const (
	// MinBatchSize is the smallest batch the regulator will ever emit.
	MinBatchSize = 256
	// MaxBatchSize is the largest batch the regulator will ever emit.
	MaxBatchSize = 4096
	// InitialBatchSize is the batch size used before any feedback arrives.
	InitialBatchSize = 1024
)

const (
	// DefaultL1Size is the assumed L1 data-cache size in bytes (32 KiB).
	DefaultL1Size = 32 * 1024
	// DefaultL2Size is the assumed L2 cache size in bytes (1 MiB).
	DefaultL2Size = 1024 * 1024
)

const (
	// shrinkMissRatio: an (EMA-smoothed) cache miss ratio at or above this
	// watermark means the pipeline is memory-bandwidth bound -> shrink.
	shrinkMissRatio = 0.20
	// growMissRatio: a miss ratio at or below this watermark means the
	// cache is hot and there is headroom -> grow.
	growMissRatio = 0.05
	// emaAlpha is the smoothing factor for the miss-ratio EMA.
	emaAlpha = 0.5
)

// Observation is the feedback collected after executing one batch.
type Observation struct {
	BatchSize   int           // rows actually processed in the batch
	RowBytes    int           // average on-the-wire size of one row, in bytes
	Duration    time.Duration // wall-clock time spent on the batch
	CacheHits   uint64        // L1/L2 cache hits observed during the batch
	CacheMisses uint64        // cache misses observed during the batch
}

// MissRatio returns the cache miss ratio of the observation (0 if no
// cache references were recorded).
func (o Observation) MissRatio() float64 {
	total := o.CacheHits + o.CacheMisses
	if total == 0 {
		return 0
	}
	return float64(o.CacheMisses) / float64(total)
}

// Controller adaptively regulates the vectorized batch size.
type Controller struct {
	// L1Size and L2Size describe the cache hierarchy in bytes.
	L1Size int
	L2Size int

	current int
	missEMA float64
	warmed  bool
}

// NewController returns a Controller with default cache sizes, starting
// at InitialBatchSize.
func NewController() *Controller {
	return &Controller{
		L1Size:  DefaultL1Size,
		L2Size:  DefaultL2Size,
		current: InitialBatchSize,
	}
}

// BatchSize returns the batch size the executor should use right now.
func (c *Controller) BatchSize() int { return c.current }

// MissRatio returns the smoothed cache miss ratio seen so far.
func (c *Controller) MissRatio() float64 { return c.missEMA }

// targetForRows returns the ideal batch size for the given row width:
// as many rows as fit into L1, clamped to [MinBatchSize, MaxBatchSize].
func (c *Controller) targetForRows(rowBytes int) int {
	if rowBytes <= 0 {
		rowBytes = 1
	}
	return clamp(c.L1Size/rowBytes, MinBatchSize, MaxBatchSize)
}

// Observe ingests the feedback of one executed batch and returns the
// batch size to use for the next batch.
func (c *Controller) Observe(obs Observation) int {
	if !c.warmed {
		c.missEMA = obs.MissRatio()
		c.warmed = true
	} else {
		c.missEMA = emaAlpha*obs.MissRatio() + (1-emaAlpha)*c.missEMA
	}

	rowBytes := obs.RowBytes
	if rowBytes <= 0 {
		rowBytes = 1
	}
	target := c.targetForRows(rowBytes)
	footprint := c.current * rowBytes

	// Memory-bandwidth bound: either the hardware counters report a high
	// miss ratio, or the batch footprint provably exceeds L1.
	bandwidthBound := c.missEMA >= shrinkMissRatio || footprint > c.L1Size

	switch {
	case bandwidthBound && c.current > MinBatchSize:
		// Shrink geometrically (1024 -> 512 -> 256). When the footprint
		// overshoots L1, do not go below the L1-resident target: that
		// size already fills the cache exactly.
		next := c.current / 2
		if footprint > c.L1Size && next < target {
			next = target
		}
		c.current = clamp(next, MinBatchSize, MaxBatchSize)
	case !bandwidthBound && c.missEMA <= growMissRatio && c.current < target:
		// Cache is hot and rows are narrow: grow geometrically toward
		// the target to amortize per-batch scheduling overhead.
		next := c.current * 2
		if next > target {
			next = target
		}
		c.current = clamp(next, MinBatchSize, MaxBatchSize)
	}
	return c.current
}

func clamp(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}
