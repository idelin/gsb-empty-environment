package vecpool

import "runtime"

// MemSample is a point-in-time snapshot of the Go heap relevant to the
// zero-allocation guarantee.
type MemSample struct {
	// TotalAlloc is the cumulative bytes allocated on the heap since
	// process start. It stays constant iff nothing allocates.
	TotalAlloc uint64
	// HeapAlloc is the bytes currently allocated on the heap.
	HeapAlloc uint64
	// NumGC is the cumulative number of completed GC cycles.
	NumGC uint32
	// PauseTotalNs is the cumulative GC stop-the-world pause time.
	PauseTotalNs uint64
}

// MemMonitor samples runtime memory statistics so a query engine can prove
// that the execution pipeline is allocation-free and GC-quiet.
type MemMonitor struct {
	samples []MemSample
}

// NewMemMonitor returns a monitor with room for capacity samples.
func NewMemMonitor(capacity int) *MemMonitor {
	return &MemMonitor{samples: make([]MemSample, 0, capacity)}
}

// Sample records the current memory statistics.
func (m *MemMonitor) Sample() {
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	m.samples = append(m.samples, MemSample{
		TotalAlloc:   ms.TotalAlloc,
		HeapAlloc:    ms.HeapAlloc,
		NumGC:        ms.NumGC,
		PauseTotalNs: ms.PauseTotalNs,
	})
}

// Samples returns the recorded samples.
func (m *MemMonitor) Samples() []MemSample { return m.samples }

// Delta returns the difference between the last and first recorded sample:
// heap bytes allocated, GC cycles, and total STW pause nanoseconds.
func (m *MemMonitor) Delta() (allocBytes uint64, gcCycles uint32, pauseNs uint64) {
	if len(m.samples) < 2 {
		return 0, 0, 0
	}
	first, last := m.samples[0], m.samples[len(m.samples)-1]
	return last.TotalAlloc - first.TotalAlloc,
		last.NumGC - first.NumGC,
		last.PauseTotalNs - first.PauseTotalNs
}
