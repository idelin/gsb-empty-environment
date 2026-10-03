package adaptivebatch

import "time"

// CacheModel simulates a two-level CPU cache hierarchy plus main memory,
// producing the same feedback a real executor would read from hardware
// performance counters. It lets the controller be exercised end-to-end
// in tests and demos without PMU access.
type CacheModel struct {
	L1Size int // bytes
	L2Size int // bytes

	L1Bandwidth  float64 // bytes per second
	L2Bandwidth  float64 // bytes per second
	MemBandwidth float64 // bytes per second
}

// DefaultCacheModel returns a model resembling a typical server core:
// 32 KiB L1d, 1 MiB L2, with roughly an order-of-magnitude bandwidth
// drop per level.
func DefaultCacheModel() *CacheModel {
	return &CacheModel{
		L1Size:       DefaultL1Size,
		L2Size:       DefaultL2Size,
		L1Bandwidth:  200e9,
		L2Bandwidth:  60e9,
		MemBandwidth: 12e9,
	}
}

// BatchResult is the simulated outcome of running one batch.
type BatchResult struct {
	Duration    time.Duration
	L1Hits      uint64
	CacheHits   uint64
	CacheMisses uint64
}

// RunBatch simulates scanning batchSize rows of rowBytes bytes each.
// Every row is touched once; a touch is served from L1 if the batch
// footprint fits in L1, otherwise only an L1Size/footprint fraction hits
// in L1, the next L2-resident fraction hits in L2, and the rest goes to
// main memory (cache thrashing).
func (m *CacheModel) RunBatch(batchSize, rowBytes int) BatchResult {
	if batchSize <= 0 || rowBytes <= 0 {
		return BatchResult{}
	}
	footprint := float64(batchSize) * float64(rowBytes)

	l1Frac := fraction(float64(m.L1Size), footprint)
	l2Frac := fraction(float64(m.L2Size), footprint) - l1Frac
	if l2Frac < 0 {
		l2Frac = 0
	}
	memFrac := 1 - l1Frac - l2Frac

	l1Bytes := footprint * l1Frac
	l2Bytes := footprint * l2Frac
	memBytes := footprint * memFrac

	seconds := l1Bytes/m.L1Bandwidth + l2Bytes/m.L2Bandwidth + memBytes/m.MemBandwidth

	accesses := uint64(batchSize)
	l1Hits := uint64(float64(accesses) * l1Frac)
	hits := l1Hits + uint64(float64(accesses)*l2Frac)
	return BatchResult{
		Duration:    time.Duration(seconds * float64(time.Second)),
		L1Hits:      l1Hits,
		CacheHits:   hits,
		CacheMisses: accesses - hits,
	}
}

func fraction(capacity, footprint float64) float64 {
	if footprint <= 0 {
		return 1
	}
	if capacity >= footprint {
		return 1
	}
	return capacity / footprint
}
