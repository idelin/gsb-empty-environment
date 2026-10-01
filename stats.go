package crset

// RepStats aggregates byte/element counts for one physical representation.
type RepStats struct {
	Buckets  int
	Elements int
	Bytes    int // container headers + payloads
}

// Stats reports the exact encoded size of a Set. Layout accounted:
//
//	directory:  2 bytes per bucket (uint16 high key)
//	container:  3 byte header (1 byte tag + 2 bytes cardinality) + payload
//	sparse:     2 bytes per element (sorted uint16 array)
//	bitmap:     65536 bytes flat (8192 x uint64)
//	run:        4 bytes per run (uint16 start + uint16 length)
type Stats struct {
	Sparse         RepStats
	Bitmap         RepStats
	Run            RepStats
	Buckets        int
	Elements       int
	DirectoryBytes int
	TotalBytes     int
	Conversions    uint64
}

// Stats computes exact byte statistics for the set's current encoding.
func (s *Set) Stats() Stats {
	var st Stats
	st.Buckets = len(s.buckets)
	st.Elements = s.card
	st.Conversions = s.conversions
	st.DirectoryBytes = bucketKeyBytes * len(s.buckets)
	add := func(r *RepStats, c *container) {
		r.Buckets++
		r.Elements += c.card
		r.Bytes += containerHeaderBytes + c.payloadBytes()
	}
	for i := range s.buckets {
		c := s.buckets[i].c
		switch c.rep {
		case repSparse:
			add(&st.Sparse, c)
		case repBitmap:
			add(&st.Bitmap, c)
		case repRun:
			add(&st.Run, c)
		}
	}
	st.TotalBytes = st.DirectoryBytes + st.Sparse.Bytes + st.Bitmap.Bytes + st.Run.Bytes
	return st
}
