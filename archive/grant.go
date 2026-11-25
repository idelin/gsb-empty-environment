package archive

import (
	"encoding/binary"
	"errors"
	"fmt"
	"sort"
)

// Interval is a half-open root-coordinate interval [Start, End).
type Interval struct {
	Start uint64
	End   uint64
}

// Length returns End-Start.  A malformed interval (End < Start) returns 0.
func (i Interval) Length() uint64 {
	if i.End < i.Start {
		return 0
	}
	return i.End - i.Start
}

// NewInterval constructs an interval.
func NewInterval(start, end uint64) Interval { return Interval{Start: start, End: end} }

// Grant is a tenant's read permission over root coordinates.
//
// Authorization is expressed only in expanded/root coordinates.  It is never
// attached to a physical block, so two manifest references that point at the
// same physical dictionary block do not share permission.
type Grant struct {
	Tenant    string
	Intervals []Interval
}

var ErrInvalidGrant = errors.New("archive: invalid grant")

// NewGrant constructs a grant.  Intervals are normalized on use.
func NewGrant(tenant string, intervals ...Interval) Grant {
	return Grant{Tenant: tenant, Intervals: intervals}
}

// Validate checks that every interval is well formed.
func (g Grant) Validate() error {
	for i, in := range g.Intervals {
		if in.End < in.Start {
			return fmt.Errorf("%w: interval %d has end before start", ErrInvalidGrant, i)
		}
	}
	return nil
}

// Normalized returns a grant with sorted, merged, non-empty intervals.
func (g Grant) Normalized() Grant {
	out := Grant{Tenant: g.Tenant}
	in := append([]Interval(nil), g.Intervals...)
	for _, iv := range in {
		if iv.End < iv.Start {
			continue
		}
		if iv.End == iv.Start {
			continue
		}
		out.Intervals = append(out.Intervals, iv)
	}
	sort.Slice(out.Intervals, func(i, j int) bool {
		if out.Intervals[i].Start != out.Intervals[j].Start {
			return out.Intervals[i].Start < out.Intervals[j].Start
		}
		return out.Intervals[i].End < out.Intervals[j].End
	})
	merged := out.Intervals[:0]
	for _, iv := range out.Intervals {
		if len(merged) == 0 || iv.Start > merged[len(merged)-1].End {
			merged = append(merged, iv)
			continue
		}
		if iv.End > merged[len(merged)-1].End {
			merged[len(merged)-1].End = iv.End
		}
	}
	out.Intervals = merged
	return out
}

// Covers reports whether [start,end) is fully contained in one of the merged
// authorized intervals.  An empty interval is always covered.
func (g Grant) Covers(start, end uint64) bool {
	if end < start {
		return false
	}
	if end == start {
		return true
	}
	n := g.Normalized()
	// Find the last interval whose start is <= start.
	idx := sort.Search(len(n.Intervals), func(i int) bool {
		return n.Intervals[i].Start > start
	}) - 1
	if idx < 0 {
		return false
	}
	return start >= n.Intervals[idx].Start && end <= n.Intervals[idx].End
}

// UnionLength returns the length of the union of the grant's intervals.
func (g Grant) UnionLength() uint64 {
	n := g.Normalized()
	var total uint64
	for _, iv := range n.Intervals {
		if iv.Length() > ^uint64(0)-total {
			return ^uint64(0)
		}
		total += iv.Length()
	}
	return total
}

// Digest returns a stable digest of the tenant and normalized intervals.
func (g Grant) Digest() Digest {
	n := g.Normalized()
	buf := make([]byte, 0, 64+len(n.Intervals)*16)
	buf = append(buf, "ps-archive-grant/1\x00"...)
	buf = append(buf, n.Tenant...)
	buf = append(buf, 0)
	var b [8]byte
	for _, iv := range n.Intervals {
		binary.BigEndian.PutUint64(b[:], iv.Start)
		buf = append(buf, b[:]...)
		binary.BigEndian.PutUint64(b[:], iv.End)
		buf = append(buf, b[:]...)
	}
	return sha256Digest(buf)
}
