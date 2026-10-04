package lsm

import "sort"

// SegmentRange is an inclusive key range.
type SegmentRange struct {
	Lo, Hi string
}

// Covers reports whether key is inside the range.
func (r SegmentRange) Covers(key string) bool { return r.Lo <= key && key <= r.Hi }

// RangesOf extracts the key ranges of the given segments.
func RangesOf(segs ...*Segment) []SegmentRange {
	var out []SegmentRange
	for _, s := range segs {
		if lo, hi, ok := s.Range(); ok {
			out = append(out, SegmentRange{Lo: lo, Hi: hi})
		}
	}
	return out
}

// Allocator hands out fresh segment IDs.
type Allocator struct{ next uint64 }

// Next returns a new unique segment ID.
func (a *Allocator) Next() uint64 { a.next++; return a.next }

// Compaction is a selected set of input segments together with everything
// needed to garbage-collect versions safely. Building outputs is fully
// independent from publishing them: the manifest is untouched by Build.
type Compaction struct {
	Inputs      []*Segment
	OutputLevel int
	// Snapshots lists the sequence numbers pinned by active readers.
	Snapshots []uint64
	// Lower holds the key ranges of lower-level segments that are NOT
	// part of this compaction. A tombstone may only be dropped for keys
	// no Lower range covers.
	Lower []SegmentRange
	// MaxRecordsPerSegment bounds output segment size; splits never
	// separate versions of one key, so output ranges never overlap.
	MaxRecordsPerSegment int

	snapshots []uint64 // sorted copy, built lazily
}

func (c *Compaction) lowerCovers(key string) bool {
	for _, r := range c.Lower {
		if r.Covers(key) {
			return true
		}
	}
	return false
}

// Build merges the input segments and returns new output segments whose
// key ranges are pairwise non-overlapping. It does not modify any live
// state; the inputs remain fully readable until a manifest commit.
func (c *Compaction) Build(alloc *Allocator) ([]*Segment, error) {
	c.snapshots = append([]uint64(nil), c.Snapshots...)
	sort.Slice(c.snapshots, func(i, j int) bool { return c.snapshots[i] < c.snapshots[j] })

	var all []Record
	for _, in := range c.Inputs {
		all = append(all, in.Records...)
	}
	sortRecords(all)

	var merged []Record
	for i := 0; i < len(all); {
		j := i
		for j < len(all) && all[j].Key == all[i].Key {
			j++
		}
		kept, err := c.compactKey(all[i:j])
		if err != nil {
			return nil, err
		}
		merged = append(merged, kept...)
		i = j
	}

	size := c.MaxRecordsPerSegment
	if size <= 0 {
		size = 16
	}
	var out []*Segment
	for i := 0; i < len(merged); {
		j := i + size
		if j > len(merged) {
			j = len(merged)
		}
		// Never split between versions of the same key, otherwise two
		// output segments would share that key in their ranges.
		for j < len(merged) && merged[j].Key == merged[j-1].Key {
			j++
		}
		out = append(out, NewSegment(alloc.Next(), c.OutputLevel, merged[i:j]))
		i = j
	}
	return out, nil
}

// RunCompaction builds the outputs of c and atomically publishes them to
// the manifest: input segments stay live until Commit succeeds, so readers
// either see the old set or the new set, never a mix. On a publish failure
// the manifest is unchanged and the old segments remain readable.
func RunCompaction(m *Manifest, c *Compaction, alloc *Allocator) ([]*Segment, error) {
	outs, err := c.Build(alloc)
	if err != nil {
		return nil, err
	}
	remove := make([]uint64, 0, len(c.Inputs))
	for _, in := range c.Inputs {
		remove = append(remove, in.ID)
	}
	if err := m.Commit(m.Version(), outs, remove); err != nil {
		return nil, err
	}
	return outs, nil
}
