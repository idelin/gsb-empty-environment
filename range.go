package paradl

import "fmt"

// ByteRange is a half-open byte interval [Start, End).
type ByteRange struct {
	Start int64 `json:"start"`
	End   int64 `json:"end"`
}

func (r ByteRange) Len() int64 { return r.End - r.Start }

func (r ByteRange) Empty() bool { return r.End <= r.Start }

func (r ByteRange) String() string { return fmt.Sprintf("[%d,%d)", r.Start, r.End) }

// Overlap returns the intersection of r and o, which may be empty.
func (r ByteRange) Overlap(o ByteRange) ByteRange {
	s := max(r.Start, o.Start)
	e := min(r.End, o.End)
	if e < s {
		e = s
	}
	return ByteRange{Start: s, End: e}
}

// RangeEntry is a confirmed range plus the checksum of its contents.
type RangeEntry struct {
	ByteRange
	Checksum string `json:"checksum"` // sha256 hex of [Start,End)
}

// ConfirmedSet tracks the ranges that have been fully written and verified.
// Overlap consistency is enforced by the caller before Add.
type ConfirmedSet struct {
	entries []RangeEntry // sorted by Start, deduplicated
}

// Entries returns a copy of the confirmed entries, sorted by start offset.
func (c *ConfirmedSet) Entries() []RangeEntry {
	out := make([]RangeEntry, len(c.entries))
	copy(out, c.entries)
	return out
}

// Covered reports whether [0, size) is fully confirmed.
func (c *ConfirmedSet) Covered(size int64) bool {
	var pos int64
	for _, e := range c.entries {
		if e.Start > pos {
			return false
		}
		pos = max(pos, e.End)
	}
	return pos >= size
}

// Missing returns the gaps within [0, size) that are not yet confirmed.
func (c *ConfirmedSet) Missing(size int64) []ByteRange {
	var out []ByteRange
	var pos int64
	for _, e := range c.entries {
		if e.Start > pos {
			out = append(out, ByteRange{Start: pos, End: e.Start})
		}
		pos = max(pos, e.End)
	}
	if pos < size {
		out = append(out, ByteRange{Start: pos, End: size})
	}
	return out
}

// Overlaps returns the confirmed entries overlapping r.
func (c *ConfirmedSet) Overlaps(r ByteRange) []RangeEntry {
	var out []RangeEntry
	for _, e := range c.entries {
		if !e.Overlap(r).Empty() {
			out = append(out, e)
		}
	}
	return out
}

// Add inserts e into the set, keeping entries sorted and deduplicated.
func (c *ConfirmedSet) Add(e RangeEntry) {
	c.entries = append(c.entries, e)
	for i := len(c.entries) - 1; i > 0 && c.entries[i-1].Start > c.entries[i].Start; i-- {
		c.entries[i-1], c.entries[i] = c.entries[i], c.entries[i-1]
	}
	out := c.entries[:0]
	for _, cur := range c.entries {
		if len(out) > 0 && out[len(out)-1].ByteRange == cur.ByteRange && out[len(out)-1].Checksum == cur.Checksum {
			continue
		}
		out = append(out, cur)
	}
	c.entries = out
}
