package adaptivebatch

import "time"

// BatchTrace records what happened on each executed batch.
type BatchTrace struct {
	BatchSize  int
	Footprint  int // BatchSize * RowBytes, in bytes
	Duration   time.Duration
	L1HitRatio float64
	MissRatio  float64
	Cumulative time.Duration
}

// ScanReport summarizes a full adaptive scan.
type ScanReport struct {
	RowBytes    int
	TotalRows   int
	Batches     []BatchTrace
	TotalTime   time.Duration
	FinalBatch  int
	Throughput  float64 // rows per second over the whole scan
}

// RunScan executes a vectorized scan of totalRows rows of rowBytes bytes
// each, letting the controller re-tune the batch size after every batch
// using feedback from the cache model.
func RunScan(c *Controller, m *CacheModel, totalRows, rowBytes int) *ScanReport {
	rep := &ScanReport{RowBytes: rowBytes, TotalRows: totalRows}
	remaining := totalRows
	for remaining > 0 {
		size := c.BatchSize()
		if size > remaining {
			size = remaining
		}
		res := m.RunBatch(size, rowBytes)
		obs := Observation{
			BatchSize:   size,
			RowBytes:    rowBytes,
			Duration:    res.Duration,
			CacheHits:   res.CacheHits,
			CacheMisses: res.CacheMisses,
		}
		rep.TotalTime += res.Duration
		l1HitRatio := 0.0
		if size > 0 {
			l1HitRatio = float64(res.L1Hits) / float64(size)
		}
		rep.Batches = append(rep.Batches, BatchTrace{
			BatchSize:  size,
			Footprint:  size * rowBytes,
			Duration:   res.Duration,
			L1HitRatio: l1HitRatio,
			MissRatio:  obs.MissRatio(),
			Cumulative: rep.TotalTime,
		})
		c.Observe(obs)
		remaining -= size
	}
	rep.FinalBatch = c.BatchSize()
	if rep.TotalTime > 0 {
		rep.Throughput = float64(totalRows) / rep.TotalTime.Seconds()
	}
	return rep
}
