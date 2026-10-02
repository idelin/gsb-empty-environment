package adaptivebatch

import "testing"

// Acceptance boundary: an ultra-wide table scan starts at 1024 rows per
// batch, the regulator detects the memory-bandwidth-bound L1 misses and
// steps down to 256 rows per batch, whose footprint fits exactly in L1.
func TestExecutorUltraWideTable(t *testing.T) {
	cfg := DefaultConfig()
	ex := NewExecutor(cfg)
	wide := Table{Name: "ultra_wide", NumRows: 1 << 20, RowWidthBytes: 128}
	stats := ex.Run(wide)

	if stats.InitialBatch != 1024 {
		t.Fatalf("initial batch = %d, want 1024", stats.InitialBatch)
	}
	if stats.FinalBatch != 256 {
		t.Fatalf("final batch = %d, want 256", stats.FinalBatch)
	}
	if fp := stats.FinalBatch * wide.RowWidthBytes; fp > cfg.L1SizeBytes {
		t.Fatalf("final footprint %d exceeds L1 %d", fp, cfg.L1SizeBytes)
	}
	// The first batch must thrash (1024*128 = 128 KiB > 32 KiB L1),
	// and once converged every batch must be fully L1-hot.
	if stats.Traces[0].L1HitRate >= cfg.LowHitRate {
		t.Fatalf("first batch L1 hit rate = %v, want < %v (thrashing)",
			stats.Traces[0].L1HitRate, cfg.LowHitRate)
	}
	for _, tr := range stats.Traces {
		if tr.BatchSize == 256 && tr.L1HitRate < 1 {
			t.Fatalf("batch %d: 256-row batch not L1-resident, hit=%v", tr.Index, tr.L1HitRate)
		}
	}
	// Convergence must be quick: 1024 -> 512 -> 256 within 2 observations.
	if stats.Traces[2].BatchSize != 256 {
		t.Fatalf("batch[2] = %d, want converged 256", stats.Traces[2].BatchSize)
	}
	t.Logf("batches=%d avgL1Hit=%.3f finalBatch=%d", stats.Batches, stats.AvgL1HitRate, stats.FinalBatch)
}

func TestExecutorNarrowTable(t *testing.T) {
	ex := NewExecutor(DefaultConfig())
	narrow := Table{Name: "narrow", NumRows: 1 << 20, RowWidthBytes: 4}
	stats := ex.Run(narrow)
	if stats.FinalBatch != 4096 {
		t.Fatalf("final batch = %d, want 4096", stats.FinalBatch)
	}
	if stats.AvgL1HitRate < 0.99 {
		t.Fatalf("avg L1 hit rate = %v, want hot cache", stats.AvgL1HitRate)
	}
}
