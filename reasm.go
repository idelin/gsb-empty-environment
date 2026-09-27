package mtusim

// segment is one received, non-overlapping run of datagram bytes.
type segment struct {
	lo  int
	buf []byte
}

func (s segment) hi() int { return s.lo + len(s.buf) }

// Reassembler collects fragments of one datagram in one epoch. It detects
// exact duplicates, rejects byte-conflicting overlaps, and refuses to mix
// fragments from different re-fragmentation epochs.
type Reassembler struct {
	PacketID int
	Epoch    int

	total    int // -1 until the final fragment fixes the datagram length
	segs     []segment
	received int // payload bytes accepted (excluding duplicates)

	// Duplicates counts exact retransmissions absorbed without effect.
	Duplicates int
	// Complete is true once [0,total) is covered and the last fragment seen.
	Complete bool
}

// NewReassembler starts a reassembly context for one packet and epoch.
func NewReassembler(packetID, epoch int) *Reassembler {
	return &Reassembler{PacketID: packetID, Epoch: epoch, total: -1}
}

// Received returns the number of distinct payload bytes accepted so far.
func (r *Reassembler) Received() int { return r.received }

// Add offers one fragment with its payload bytes.
func (r *Reassembler) Add(f Fragment, payload []byte) error {
	if f.PacketID != r.PacketID {
		return errf(ErrKindPacketMismatch,
			"fragment packet %d, reassembly packet %d", f.PacketID, r.PacketID)
	}
	if f.Epoch != r.Epoch {
		return errf(ErrKindEpochMismatch,
			"fragment epoch %d, reassembly epoch %d: epochs must not mix",
			f.Epoch, r.Epoch)
	}
	if f.Len < 0 || f.Offset < 0 {
		return errf(ErrKindNegativeLength, "fragment has negative offset/len")
	}
	if len(payload) != f.Len {
		return errf(ErrKindMalformed,
			"fragment declares %d payload bytes, got %d", f.Len, len(payload))
	}
	lo, hi := f.Range()
	if f.Last {
		if r.total >= 0 && r.total != hi {
			return errf(ErrKindOverlapConflict,
				"conflicting datagram totals: %d vs %d", r.total, hi)
		}
		r.total = hi
	}
	if r.total >= 0 && hi > r.total {
		return errf(ErrKindMalformed,
			"fragment [%d,%d) extends past datagram total %d", lo, hi, r.total)
	}
	if r.Complete {
		// Late retransmission of an already completed datagram.
		r.Duplicates++
		return nil
	}

	// Any overlap must agree byte-for-byte, otherwise the two fragments
	// claim different contents for the same original range.
	for _, seg := range r.segs {
		olo, ohi := maxInt2(lo, seg.lo), minInt2(hi, seg.hi())
		for i := olo; i < ohi; i++ {
			if seg.buf[i-seg.lo] != payload[i-lo] {
				return errf(ErrKindOverlapConflict,
					"byte %d: existing %#02x vs new %#02x",
					i, seg.buf[i-seg.lo], payload[i-lo])
			}
		}
	}
	if r.covered(lo, hi) {
		r.Duplicates++
		return nil
	}

	// Merge with every overlapping segment (contents already verified equal).
	nlo, nhi := lo, hi
	for _, seg := range r.segs {
		if seg.hi() > nlo && seg.lo < nhi {
			nlo = minInt2(nlo, seg.lo)
			nhi = maxInt2(nhi, seg.hi())
		}
	}
	merged := make([]byte, nhi-nlo)
	copy(merged[lo-nlo:], payload)
	kept := r.segs[:0]
	for _, seg := range r.segs {
		if seg.hi() > nlo && seg.lo < nhi {
			copy(merged[seg.lo-nlo:], seg.buf)
			r.received -= len(seg.buf)
		} else {
			kept = append(kept, seg)
		}
	}
	r.segs = insertSegment(kept, segment{lo: nlo, buf: merged})
	r.received += len(merged)

	if r.total >= 0 && r.covered(0, r.total) {
		r.Complete = true
	}
	return nil
}

// Bytes returns the reassembled datagram once Complete.
func (r *Reassembler) Bytes() []byte {
	if !r.Complete {
		return nil
	}
	out := make([]byte, r.total)
	for _, seg := range r.segs {
		copy(out[seg.lo:], seg.buf)
	}
	return out
}

// covered reports whether [lo,hi) is already fully covered by segments.
func (r *Reassembler) covered(lo, hi int) bool {
	expect := lo
	for _, seg := range r.segs {
		if seg.lo > expect {
			return false
		}
		if seg.hi() > expect {
			expect = seg.hi()
		}
		if expect >= hi {
			return true
		}
	}
	return expect >= hi
}

func insertSegment(segs []segment, s segment) []segment {
	i := 0
	for i < len(segs) && segs[i].lo < s.lo {
		i++
	}
	segs = append(segs, segment{})
	copy(segs[i+1:], segs[i:])
	segs[i] = s
	return segs
}

func minInt2(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func maxInt2(a, b int) int {
	if a > b {
		return a
	}
	return b
}
