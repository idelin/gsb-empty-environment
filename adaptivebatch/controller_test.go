package adaptivebatch

import (
	"testing"
	"time"
)

// drive feeds n observations of the given row width through the controller,
// using the cache model to produce realistic hit/miss feedback.
func drive(c *Controller, m *CacheModel, rowBytes, n int) {
	for i := 0; i < n; i++ {
		size := c.BatchSize()
		res := m.RunBatch(size, rowBytes)
		c.Observe(Observation{
			BatchSize:   size,
			RowBytes:    rowBytes,
			Duration:    res.Duration,
			CacheHits:   res.CacheHits,
			CacheMisses: res.CacheMisses,
		})
	}
}

// Acceptance: ultra-wide rows (128 B) drive the batch from the initial
// 1024 down to 256, at which point one batch is exactly 32 KiB and fits
// in L1.
func TestWideRowsShrinkBatchTo256(t *testing.T) {
	c := NewController()
	m := DefaultCacheModel()
	if got := c.BatchSize(); got != InitialBatchSize {
		t.Fatalf("initial batch = %d, want %d", got, InitialBatchSize)
	}
	drive(c, m, 128, 8)
	if got := c.BatchSize(); got != MinBatchSize {
		t.Fatalf("batch after adaptation = %d, want %d", got, MinBatchSize)
	}
	if footprint := c.BatchSize() * 128; footprint != c.L1Size {
		t.Fatalf("final footprint = %d B, want exactly L1 size %d B", footprint, c.L1Size)
	}
}

// Narrow rows (8 B) let the batch grow to the 4096 cap while the cache
// stays hot.
func TestNarrowRowsGrowBatchTo4096(t *testing.T) {
	c := NewController()
	m := DefaultCacheModel()
	drive(c, m, 8, 8)
	if got := c.BatchSize(); got != MaxBatchSize {
		t.Fatalf("batch after adaptation = %d, want %d", got, MaxBatchSize)
	}
	if footprint := c.BatchSize() * 8; footprint > c.L1Size {
		t.Fatalf("final footprint = %d B exceeds L1 size %d B", footprint, c.L1Size)
	}
}

// Rows sized so that exactly 1024 fit in L1 must keep the batch stable.
func TestBatchStableWhenAlreadyOptimal(t *testing.T) {
	c := NewController()
	m := DefaultCacheModel()
	drive(c, m, 32, 8) // 32 B/row -> 1024 rows == 32 KiB == L1
	if got := c.BatchSize(); got != InitialBatchSize {
		t.Fatalf("batch = %d, want it to stay at %d", got, InitialBatchSize)
	}
}

// The regulator must never emit sizes outside [MinBatchSize, MaxBatchSize],
// even for absurd row widths.
func TestBatchSizeAlwaysWithinBounds(t *testing.T) {
	for _, rowBytes := range []int{1, 7, 64, 256, 4096, 1 << 20} {
		c := NewController()
		m := DefaultCacheModel()
		for i := 0; i < 12; i++ {
			size := c.BatchSize()
			if size < MinBatchSize || size > MaxBatchSize {
				t.Fatalf("rowBytes=%d: batch %d out of bounds [%d, %d]",
					rowBytes, size, MinBatchSize, MaxBatchSize)
			}
			res := m.RunBatch(size, rowBytes)
			c.Observe(Observation{
				BatchSize:   size,
				RowBytes:    rowBytes,
				Duration:    res.Duration,
				CacheHits:   res.CacheHits,
				CacheMisses: res.CacheMisses,
			})
		}
	}
}

// End-to-end acceptance scenario: an ultra-wide table scan detects the
// memory-bandwidth bottleneck, steps 1024 -> 512 -> 256, keeps every
// steady-state batch inside L1, and finishes faster than a fixed-1024
// baseline that thrashes the cache.
func TestAcceptanceUltraWideScan(t *testing.T) {
	const (
		rowBytes  = 128
		totalRows = 1 << 20
	)
	c := NewController()
	m := DefaultCacheModel()
	rep := RunScan(c, m, totalRows, rowBytes)

	if len(rep.Batches) < 3 {
		t.Fatalf("expected at least 3 batches to observe adaptation, got %d", len(rep.Batches))
	}
	if rep.Batches[0].BatchSize != 1024 {
		t.Fatalf("first batch = %d, want 1024", rep.Batches[0].BatchSize)
	}
	if rep.Batches[1].BatchSize != 512 {
		t.Fatalf("second batch = %d, want 512 (geometric shrink)", rep.Batches[1].BatchSize)
	}
	if rep.Batches[2].BatchSize != 256 {
		t.Fatalf("third batch = %d, want 256 (fits L1 exactly)", rep.Batches[2].BatchSize)
	}
	if rep.FinalBatch != MinBatchSize {
		t.Fatalf("final batch = %d, want %d", rep.FinalBatch, MinBatchSize)
	}

	// Steady state: every batch after convergence fits in L1 with zero misses.
	for i := 2; i < len(rep.Batches); i++ {
		b := rep.Batches[i]
		if b.Footprint > c.L1Size {
			t.Fatalf("batch %d footprint %d B exceeds L1 (%d B)", i+1, b.Footprint, c.L1Size)
		}
		if b.MissRatio != 0 {
			t.Fatalf("batch %d miss ratio = %.2f, want 0 (cache hot)", i+1, b.MissRatio)
		}
	}

	// Adaptive scan must beat the fixed-1024 baseline that thrashes L1.
	var baseline time.Duration
	remaining := totalRows
	for remaining > 0 {
		size := 1024
		if size > remaining {
			size = remaining
		}
		baseline += m.RunBatch(size, rowBytes).Duration
		remaining -= size
	}
	if rep.TotalTime >= baseline {
		t.Fatalf("adaptive scan (%v) not faster than fixed-1024 baseline (%v)",
			rep.TotalTime, baseline)
	}
	t.Logf("adaptive: %v, fixed-1024 baseline: %v, speedup: %.2fx",
		rep.TotalTime, baseline, baseline.Seconds()/rep.TotalTime.Seconds())
}

// High miss ratios reported by hardware counters alone (even without an
// oversize footprint) must trigger shrinking.
func TestShrinksOnReportedCacheMisses(t *testing.T) {
	c := NewController()
	// 16 B/row: 1024 rows = 16 KiB, fits L1, but counters report 50% misses.
	for i := 0; i < 4; i++ {
		size := c.BatchSize()
		c.Observe(Observation{
			BatchSize:   size,
			RowBytes:    16,
			CacheHits:   uint64(size / 2),
			CacheMisses: uint64(size / 2),
		})
	}
	if got := c.BatchSize(); got >= InitialBatchSize {
		t.Fatalf("batch = %d, want shrink below %d on high miss ratio", got, InitialBatchSize)
	}
}
