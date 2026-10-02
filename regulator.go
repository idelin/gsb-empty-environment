// Package adaptivebatch implements an adaptive batch-size regulator for a
// vectorized execution engine. It watches L1/L2 cache hit rates and the
// row width of the current query, then shrinks or grows the batch size so
// that a single batch stays cache-resident and avoids cache thrashing.
package adaptivebatch

// Config tunes an AdaptiveBatchSizer.
type Config struct {
	// L1SizeBytes / L2SizeBytes describe the CPU cache hierarchy the
	// regulator tries to keep the working set inside of.
	L1SizeBytes int
	L2SizeBytes int

	// MinBatch is the floor for the batch size (256 per spec): even for
	// very wide rows the regulator never goes below it.
	MinBatch int
	// MaxBatch is the ceiling for the batch size (4096 per spec): very
	// narrow rows are processed in batches up to this size.
	MaxBatch int
	// InitialBatch is the batch size the engine starts with (1024).
	InitialBatch int

	// ShrinkFactor multiplies the batch size when the L1 hit rate drops
	// below LowHitRate (memory-bandwidth bound / cache thrashing).
	ShrinkFactor float64
	// GrowFactor multiplies the batch size when the L1 hit rate stays
	// above HighHitRate and the L1 capacity allows a bigger batch.
	GrowFactor float64

	// LowHitRate is the L1 hit-rate threshold that triggers a shrink.
	LowHitRate float64
	// HighHitRate is the L1 hit-rate threshold that permits growth.
	HighHitRate float64
}

// DefaultConfig returns a configuration matching a typical CPU:
// 32 KiB L1d, 1 MiB L2, batch clamped to [256, 4096], starting at 1024.
func DefaultConfig() Config {
	return Config{
		L1SizeBytes:  32 * 1024,
		L2SizeBytes:  1024 * 1024,
		MinBatch:     256,
		MaxBatch:     4096,
		InitialBatch: 1024,
		ShrinkFactor: 0.5,
		GrowFactor:   1.5,
		LowHitRate:   0.80,
		HighHitRate:  0.98,
	}
}

// Sample is one observation reported by the execution engine after it has
// processed a batch.
type Sample struct {
	// RowWidthBytes is the average in-memory width of one row.
	RowWidthBytes int
	// L1HitRate is the observed L1 cache hit rate in [0, 1].
	L1HitRate float64
	// L2HitRate is the observed L2 cache hit rate in [0, 1].
	L2HitRate float64
}

// AdaptiveBatchSizer is the regulator. It is driven by the engine calling
// Observe after every batch; BatchSize returns the current decision.
type AdaptiveBatchSizer struct {
	cfg     Config
	current int
}

// NewAdaptiveBatchSizer creates a regulator starting at cfg.InitialBatch.
func NewAdaptiveBatchSizer(cfg Config) *AdaptiveBatchSizer {
	return &AdaptiveBatchSizer{cfg: cfg, current: cfg.InitialBatch}
}

// BatchSize returns the batch size the engine should use for the next batch.
func (s *AdaptiveBatchSizer) BatchSize() int { return s.current }

// l1Capacity returns how many rows of the given width fit inside L1.
func (s *AdaptiveBatchSizer) l1Capacity(rowWidthBytes int) int {
	if rowWidthBytes <= 0 {
		return s.cfg.MaxBatch
	}
	return s.cfg.L1SizeBytes / rowWidthBytes
}

// clamp bounds the batch size to [MinBatch, min(MaxBatch, l1Capacity)],
// never letting the L1 capacity push the size below the 256-row floor.
func (s *AdaptiveBatchSizer) clamp(batch, rowWidthBytes int) int {
	upper := s.cfg.MaxBatch
	if capacity := s.l1Capacity(rowWidthBytes); capacity < upper && capacity >= s.cfg.MinBatch {
		upper = capacity
	}
	if batch > upper {
		batch = upper
	}
	if batch < s.cfg.MinBatch {
		batch = s.cfg.MinBatch
	}
	return batch
}

// Observe ingests one engine sample, adjusts the batch size, and returns
// the new decision. The policy:
//
//   - L1 hit rate below LowHitRate: the batch working set overflows L1 and
//     the query is memory-bandwidth bound; shrink multiplicatively.
//   - L1 hit rate above HighHitRate and L1 has spare capacity: grow
//     multiplicatively to amortize per-batch overhead.
//   - The result is always clamped so one batch fits in L1 when possible
//     and stays within [MinBatch, MaxBatch].
func (s *AdaptiveBatchSizer) Observe(sm Sample) int {
	next := s.current
	switch {
	case sm.L1HitRate < s.cfg.LowHitRate:
		next = int(float64(next) * s.cfg.ShrinkFactor)
	case sm.L1HitRate >= s.cfg.HighHitRate && s.current < s.l1Capacity(sm.RowWidthBytes):
		next = int(float64(next)*s.cfg.GrowFactor) + 1
	}
	s.current = s.clamp(next, sm.RowWidthBytes)
	return s.current
}
