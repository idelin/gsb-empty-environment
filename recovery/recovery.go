// Package recovery rebuilds a log archive from redundant segments.
//
// A Segment is a contiguous run of records identified by the sequence
// number of its first record. Segments may overlap arbitrarily and may
// have a torn (truncated) tail, which is detected through per-record
// checksums. Recovery merges segments only where identical sequence
// numbers carry identical contents, reports the earliest conflicting
// sequence number, and publishes only the contiguous, checksum-verified
// prefix that starts at a caller-chosen sequence number. Content beyond
// a gap or a conflict is never silently joined onto the prefix.
package recovery

import (
	"bytes"
	"hash/crc32"
)

// Record is a single log entry. Sum must be Checksum(Data).
type Record struct {
	Data []byte
	Sum  uint32
}

// Checksum returns the checksum stored in Record.Sum.
func Checksum(data []byte) uint32 { return crc32.ChecksumIEEE(data) }

// NewRecord builds a Record with a valid checksum.
func NewRecord(data []byte) Record {
	return Record{Data: data, Sum: Checksum(data)}
}

// Valid reports whether the record's checksum matches its data.
func (r Record) Valid() bool { return r.Sum == Checksum(r.Data) }

// Segment is a contiguous run of records: Records[i] carries sequence
// number Start+i. A segment read from storage may be truncated at the
// tail; everything from the first checksum failure onward is discarded.
type Segment struct {
	Start   uint64
	Records []Record
}

// End returns the sequence number one past the last record.
func (s Segment) End() uint64 { return s.Start + uint64(len(s.Records)) }

// validPrefix returns the records up to (excluding) the first checksum
// failure, i.e. the trustworthy head of a possibly torn segment.
func (s Segment) validPrefix() []Record {
	n := 0
	for n < len(s.Records) && s.Records[n].Valid() {
		n++
	}
	return s.Records[:n]
}

// Conflict describes the earliest sequence number at which two segments
// disagree on the content.
type Conflict struct {
	Seq      uint64
	Segments [2]int // indices (into the Recover input) of two disagreeing segments
}

// Result is the outcome of Recover.
type Result struct {
	From     uint64
	Records  []Record  // published contiguous, checksum-verified prefix
	Sources  []int     // Sources[i] is the segment index that supplied Records[i]
	Conflict *Conflict // earliest content conflict anywhere in the input, if any
}

// Seq returns the sequence number of published record i.
func (r *Result) Seq(i int) uint64 { return r.From + uint64(i) }

// Recover merges segs and publishes the verified prefix starting at from.
//
// Merge rule: for any sequence number covered by more than one segment,
// the contents must be identical; the first disagreement is reported as
// a Conflict carrying the earliest such sequence number.
//
// Publish rule: records are emitted for consecutive sequence numbers
// from, from+1, ... and the walk stops at the first sequence number that
// is covered by no segment (a gap) or that is conflicted. Nothing past
// that point is joined onto the result.
func Recover(from uint64, segs []Segment) Result {
	type cell struct {
		rec Record
		src int
	}
	cells := make(map[uint64]cell)
	conflicted := make(map[uint64]bool)
	var conflict *Conflict

	for i := range segs {
		valid := segs[i].validPrefix()
		for j, rec := range valid {
			seq := segs[i].Start + uint64(j)
			c, seen := cells[seq]
			switch {
			case !seen:
				cells[seq] = cell{rec: rec, src: i}
			case !bytes.Equal(c.rec.Data, rec.Data):
				conflicted[seq] = true
				if conflict == nil || seq < conflict.Seq {
					conflict = &Conflict{Seq: seq, Segments: [2]int{c.src, i}}
				}
			}
		}
	}

	res := Result{From: from, Conflict: conflict}
	for seq := from; ; seq++ {
		c, ok := cells[seq]
		if !ok {
			break // gap: never join across missing sequence numbers
		}
		if conflicted[seq] {
			break // undecidable content ends the prefix
		}
		res.Records = append(res.Records, c.rec)
		res.Sources = append(res.Sources, c.src)
	}
	return res
}
