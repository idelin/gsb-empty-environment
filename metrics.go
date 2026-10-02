package stcs

import "sync/atomic"

// Metrics tracks the I/O counters used to compute read and write
// amplification. All fields are updated atomically.
type Metrics struct {
	UserBytes         int64 // logical bytes ingested via Put
	FlushBytes        int64 // physical bytes written by memtable flushes
	FlushCount        int64
	CompactReadBytes  int64 // physical bytes read from compaction inputs
	CompactWriteBytes int64 // physical bytes written as compaction outputs
	Compactions       int64
	DedupedEntries    int64 // stale versions dropped during merges
}

// WriteAmplification returns the total bytes physically written to disk
// (flushes + compaction outputs) divided by the logical user bytes
// ingested. For a single-tier STCS run with threshold T, every byte is
// rewritten at most once per tier, so a single tier bounds this at 2.0.
func (m *Metrics) WriteAmplification() float64 {
	user := atomic.LoadInt64(&m.UserBytes)
	if user == 0 {
		return 0
	}
	written := atomic.LoadInt64(&m.FlushBytes) + atomic.LoadInt64(&m.CompactWriteBytes)
	return float64(written) / float64(user)
}

// CompactionReadAmplification returns the bytes read by compactions per
// byte they wrote back out. A full merge reads all of its inputs, so this
// is inputBytes/outputBytes and stays close to 1.0 (slightly above when
// duplicate versions are dropped).
func (m *Metrics) CompactionReadAmplification() float64 {
	out := atomic.LoadInt64(&m.CompactWriteBytes)
	if out == 0 {
		return 0
	}
	return float64(atomic.LoadInt64(&m.CompactReadBytes)) / float64(out)
}

func (m *Metrics) snapshot() (user, flush, flushN, cr, cw, comp, dedup int64) {
	return atomic.LoadInt64(&m.UserBytes),
		atomic.LoadInt64(&m.FlushBytes),
		atomic.LoadInt64(&m.FlushCount),
		atomic.LoadInt64(&m.CompactReadBytes),
		atomic.LoadInt64(&m.CompactWriteBytes),
		atomic.LoadInt64(&m.Compactions),
		atomic.LoadInt64(&m.DedupedEntries)
}
