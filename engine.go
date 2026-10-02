package adaptivebatch

// Table describes a relation scanned by the vectorized executor.
type Table struct {
	Name          string
	NumRows       int
	RowWidthBytes int
}

// BatchTrace records one executed batch for observability.
type BatchTrace struct {
	Index      int
	BatchSize  int
	Footprint  int
	L1HitRate  float64
	L2HitRate  float64
}

// Stats summarizes one executor run.
type Stats struct {
	Table        Table
	Batches      int
	InitialBatch int
	FinalBatch   int
	AvgL1HitRate float64
	Traces       []BatchTrace
}

// Executor is a minimal vectorized scan operator. It processes the table
// in batches, sizes each batch through the regulator, and reports cache
// behaviour back after every batch.
type Executor struct {
	Sizer *AdaptiveBatchSizer
	L1    *CacheModel
	L2    *CacheModel
}

// NewExecutor wires a regulator to a simulated L1/L2 hierarchy.
func NewExecutor(cfg Config) *Executor {
	return &Executor{
		Sizer: NewAdaptiveBatchSizer(cfg),
		L1:    NewCacheModel(cfg.L1SizeBytes),
		L2:    NewCacheModel(cfg.L2SizeBytes),
	}
}

// Run scans the whole table, adapting the batch size as it goes.
func (e *Executor) Run(t Table) Stats {
	stats := Stats{Table: t, InitialBatch: e.Sizer.BatchSize()}
	batch := e.Sizer.BatchSize()
	var hitSum float64
	for offset, idx := 0, 0; offset < t.NumRows; offset, idx = offset+batch, idx+1 {
		n := t.NumRows - offset
		if n > batch {
			n = batch
		}
		footprint := n * t.RowWidthBytes
		l1Hit := e.L1.HitRate(footprint)
		l2Hit := e.L2.HitRate(footprint)
		stats.Traces = append(stats.Traces, BatchTrace{
			Index:     idx,
			BatchSize: n,
			Footprint: footprint,
			L1HitRate: l1Hit,
			L2HitRate: l2Hit,
		})
		hitSum += l1Hit
		stats.Batches++
		// Feed the observation back; the next iteration uses the new size.
		batch = e.Sizer.Observe(Sample{
			RowWidthBytes: t.RowWidthBytes,
			L1HitRate:     l1Hit,
			L2HitRate:     l2Hit,
		})
	}
	stats.FinalBatch = e.Sizer.BatchSize()
	if stats.Batches > 0 {
		stats.AvgL1HitRate = hitSum / float64(stats.Batches)
	}
	return stats
}
