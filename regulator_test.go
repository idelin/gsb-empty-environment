package adaptivebatch

import "testing"

func TestShrinkOnWideRows(t *testing.T) {
	cfg := DefaultConfig()
	s := NewAdaptiveBatchSizer(cfg)
	if got := s.BatchSize(); got != 1024 {
		t.Fatalf("initial batch = %d, want 1024", got)
	}
	// 128-byte rows: 1024-row batch = 128 KiB >> 32 KiB L1, thrashing.
	rowWidth := 128
	footprint := func(b int) int { return b * rowWidth }
	for i := 0; i < 4; i++ {
		l1 := NewCacheModel(cfg.L1SizeBytes).HitRate(footprint(s.BatchSize()))
		s.Observe(Sample{RowWidthBytes: rowWidth, L1HitRate: l1, L2HitRate: 1})
	}
	if got := s.BatchSize(); got != 256 {
		t.Fatalf("batch after shrink = %d, want 256", got)
	}
	if fp := footprint(s.BatchSize()); fp > cfg.L1SizeBytes {
		t.Fatalf("batch footprint %d exceeds L1 %d", fp, cfg.L1SizeBytes)
	}
}

func TestGrowOnNarrowRows(t *testing.T) {
	cfg := DefaultConfig()
	s := NewAdaptiveBatchSizer(cfg)
	rowWidth := 4 // extremely narrow rows
	for i := 0; i < 8; i++ {
		s.Observe(Sample{RowWidthBytes: rowWidth, L1HitRate: 1, L2HitRate: 1})
	}
	if got := s.BatchSize(); got != 4096 {
		t.Fatalf("batch after growth = %d, want 4096", got)
	}
}

func TestNeverBelowMinBatch(t *testing.T) {
	cfg := DefaultConfig()
	s := NewAdaptiveBatchSizer(cfg)
	// 4 KiB rows: L1 capacity is 8 rows, far below the 256 floor.
	for i := 0; i < 10; i++ {
		s.Observe(Sample{RowWidthBytes: 4096, L1HitRate: 0, L2HitRate: 0})
	}
	if got := s.BatchSize(); got != 256 {
		t.Fatalf("batch = %d, want floor 256", got)
	}
}

func TestNeverAboveMaxBatch(t *testing.T) {
	cfg := DefaultConfig()
	s := NewAdaptiveBatchSizer(cfg)
	for i := 0; i < 20; i++ {
		s.Observe(Sample{RowWidthBytes: 1, L1HitRate: 1, L2HitRate: 1})
	}
	if got := s.BatchSize(); got != 4096 {
		t.Fatalf("batch = %d, want ceiling 4096", got)
	}
}

func TestStableWhenHot(t *testing.T) {
	cfg := DefaultConfig()
	s := NewAdaptiveBatchSizer(cfg)
	// 32-byte rows: L1 capacity is exactly 1024 = initial batch; the
	// regulator should stay put while the cache is hot.
	for i := 0; i < 5; i++ {
		s.Observe(Sample{RowWidthBytes: 32, L1HitRate: 0.99, L2HitRate: 1})
	}
	if got := s.BatchSize(); got != 1024 {
		t.Fatalf("batch = %d, want stable 1024", got)
	}
}
