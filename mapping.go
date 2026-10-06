package diagnostics

// Edit replaces the half-open range [Start, End) with NewLen bytes of new
// text. Offsets are expressed in the coordinates of the version the edit
// applies to; within a batch passed to File.ApplyEdits, each edit is
// expressed in the coordinates produced by the previous edit.
type Edit struct {
	Start, End int
	NewLen     int
}

func (e Edit) delta() int { return e.NewLen - (e.End - e.Start) }

// MapRange maps r through a single edit. ok is false when the edit touches
// the content of r, invalidating any diagnostic anchored there.
//
// An edit touches r when their half-open intervals overlap. Insertions
// exactly at r.Start or r.End are considered outside the diagnostic: the
// range shifts or stays put but is never extended and never invalidated.
func MapRange(r Range, e Edit) (mapped Range, ok bool) {
	if e.Start == e.End && e.NewLen == 0 {
		return r, true
	}
	if e.End <= r.Start {
		// Edit entirely before r (including insertion at r.Start).
		return Range{r.Start + e.delta(), r.End + e.delta()}, true
	}
	if e.Start >= r.End {
		// Edit entirely after r (including insertion at r.End).
		return r, true
	}
	return Range{}, false
}

// MapThroughEdits maps r through a chain of edits applied in order. ok is
// false as soon as any edit in the chain invalidates the range.
func MapThroughEdits(r Range, edits []Edit) (Range, bool) {
	var ok bool
	for _, e := range edits {
		r, ok = MapRange(r, e)
		if !ok {
			return Range{}, false
		}
	}
	return r, true
}
