package logtrunc

import "sort"

// Dependency kinds used by the reference model.
const (
	DepConsumer = "consumer/"
	DepSnapshot = "snapshot/"
)

// Reference is an intentionally simple per-record model used to
// cross-check the segment-level Planner. Instead of reasoning about
// whole segments, it computes, for every individual record, the set of
// entities that still depend on it:
//
//   - consumer/<name> for every consumer whose contiguous confirmed
//     prefix does not cover the record;
//   - snapshot/<id> for every snapshot pinning the record's segment.
//
// A record is deletable iff its dependency set is empty; a segment is
// deletable iff every record in it is deletable.
type Reference struct {
	segments map[string]Segment
	// confirmed maps consumer name -> set of individually confirmed
	// record offsets. The watermark is derived by scanning for the
	// first unconfirmed offset, so gaps can never be hidden.
	confirmed map[string]map[uint64]bool
	snapshots map[string]map[string]bool
}

func NewReference() *Reference {
	return &Reference{
		segments:  make(map[string]Segment),
		confirmed: make(map[string]map[uint64]bool),
		snapshots: make(map[string]map[string]bool),
	}
}

func (r *Reference) AddSegment(s Segment)    { r.segments[s.ID] = s }
func (r *Reference) RemoveSegment(id string) { delete(r.segments, id) }

func (r *Reference) AddConsumer(name string) {
	r.confirmed[name] = make(map[uint64]bool)
}

func (r *Reference) RemoveConsumer(name string) { delete(r.confirmed, name) }

func (r *Reference) Confirm(name string, offset uint64) {
	r.confirmed[name][offset] = true
}

func (r *Reference) AddSnapshot(id string, segmentIDs []string) {
	pins := make(map[string]bool, len(segmentIDs))
	for _, s := range segmentIDs {
		pins[s] = true
	}
	r.snapshots[id] = pins
}

func (r *Reference) RemoveSnapshot(id string) { delete(r.snapshots, id) }

// Watermark derives the contiguous confirmed prefix of a consumer by
// scanning from offset 0 for the first gap.
func (r *Reference) Watermark(name string) uint64 {
	conf := r.confirmed[name]
	w := uint64(0)
	for conf[w] {
		w++
	}
	return w
}

// Consumers returns the registered consumer names, sorted.
func (r *Reference) Consumers() []string {
	out := make([]string, 0, len(r.confirmed))
	for name := range r.confirmed {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// RecordDeps returns the sorted dependency set of one record.
func (r *Reference) RecordDeps(offset uint64) []string {
	var deps []string
	for name := range r.confirmed {
		if r.Watermark(name) <= offset {
			deps = append(deps, DepConsumer+name)
		}
	}
	if segID, ok := r.segmentOf(offset); ok {
		for snapID, pins := range r.snapshots {
			if pins[segID] {
				deps = append(deps, DepSnapshot+snapID)
			}
		}
	}
	sort.Strings(deps)
	return deps
}

// RecordDeletable reports whether a single record may be dropped.
func (r *Reference) RecordDeletable(offset uint64) bool {
	return len(r.RecordDeps(offset)) == 0
}

// SegmentDeletable reports whether every record of the segment may be
// dropped.
func (r *Reference) SegmentDeletable(id string) bool {
	s, ok := r.segments[id]
	if !ok {
		return false
	}
	for off := s.Base; off < s.End; off++ {
		if !r.RecordDeletable(off) {
			return false
		}
	}
	return true
}

func (r *Reference) segmentOf(offset uint64) (string, bool) {
	for id, s := range r.segments {
		if offset >= s.Base && offset < s.End {
			return id, true
		}
	}
	return "", false
}
