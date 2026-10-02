package adaptivebatch

// CacheModel simulates the behaviour of one cache level for a streaming
// scan: rows touched by the current batch stay resident only if the whole
// batch footprint fits in the level; the excess fraction misses.
//
// It is a deterministic stand-in for hardware performance counters, used
// by the executor in tests and the demo. A real deployment would feed
// Observe with PMU-derived hit rates instead.
type CacheModel struct {
	sizeBytes int
}

// NewCacheModel models a cache level of sizeBytes.
func NewCacheModel(sizeBytes int) *CacheModel {
	return &CacheModel{sizeBytes: sizeBytes}
}

// HitRate returns the expected hit rate when scanning footprintBytes with
// a working set of that size: fully hot when the footprint fits, otherwise
// only the resident fraction hits.
func (c *CacheModel) HitRate(footprintBytes int) float64 {
	if footprintBytes <= 0 {
		return 1
	}
	if footprintBytes <= c.sizeBytes {
		return 1
	}
	return float64(c.sizeBytes) / float64(footprintBytes)
}
