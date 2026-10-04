package main

// Record is the unit of merging: a key read from one input source, tagged with
// the source index and its sequence number (0-based ordinal) within that source.
type Record struct {
	Key    string
	Source int
	Seq    int
}

// lessRec is the total order used everywhere: key, then source, then sequence.
// (key, source, seq) is unique, so the order is total and deterministic.
func lessRec(a, b Record) bool {
	if a.Key != b.Key {
		return a.Key < b.Key
	}
	if a.Source != b.Source {
		return a.Source < b.Source
	}
	return a.Seq < b.Seq
}

// recordOverhead approximates the per-record bookkeeping cost (source, seq,
// slice/header amortised) so the memory limit reflects actual buffered bytes.
const recordOverhead = 16

func (r Record) bufferedBytes() int64 {
	return int64(len(r.Key)) + recordOverhead
}
