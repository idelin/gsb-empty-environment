package diag

import "fmt"

// FileVersion identifies a revision of a document. Version 1 is the
// initial text; every applied edit increments it by one.
type FileVersion uint64

// Edit replaces the half-open byte range [Start, End) with Text.
// A pure insertion has Start == End; a pure deletion has Text == "".
type Edit struct {
	Start, End int
	Text       string
}

// Document is a text plus the chain of edits that produced its current
// version. The chain maps diagnostic ranges from the version they were
// analyzed at to the current version; a range that intersects any
// edit's replaced region is invalidated (its text may have changed),
// while untouched ranges keep their identity and only shift position.
type Document struct {
	text    string
	version FileVersion
	edits   []Edit // edits[i] transitions version i+1 to i+2
}

// NewDocument returns a document holding text at version 1.
func NewDocument(text string) *Document {
	return &Document{text: text, version: 1}
}

// Version returns the current file version.
func (d *Document) Version() FileVersion { return d.version }

// Text returns the current text.
func (d *Document) Text() string { return d.text }

// ApplyEdit applies e to the current text and records it in the edit
// chain, bumping the version by one.
func (d *Document) ApplyEdit(e Edit) error {
	if e.Start < 0 || e.End < e.Start || e.End > len(d.text) {
		return fmt.Errorf("diag: edit range [%d,%d) out of bounds for document of length %d", e.Start, e.End, len(d.text))
	}
	d.text = d.text[:e.Start] + e.Text + d.text[e.End:]
	d.edits = append(d.edits, e)
	d.version++
	return nil
}

// MapRange maps r, expressed in the coordinates of file version from,
// forward through the edit chain to the document's current version.
// It reports ok == false when from is unknown or when r intersects the
// replaced region of some edit in the chain, i.e. when the diagnostic
// touches content that changed and must be considered invalidated.
//
// Boundary convention for a pure insertion at position p: ranges
// ending exactly at p are untouched and keep their span (the inserted
// text lands after them); ranges starting exactly at p are untouched
// and shift right (the inserted text lands before them).
func (d *Document) MapRange(from FileVersion, r Range) (Range, bool) {
	if from < 1 || from > d.version {
		return Range{}, false
	}
	cur := r
	for i := int(from) - 1; i < len(d.edits); i++ {
		next, ok := mapThroughEdit(d.edits[i], cur)
		if !ok {
			return Range{}, false
		}
		cur = next
	}
	return cur, true
}

// mapThroughEdit maps r across a single edit.
func mapThroughEdit(e Edit, r Range) (Range, bool) {
	delta := len(e.Text) - (e.End - e.Start)
	switch {
	case r.End <= e.Start:
		// Entirely before the replaced region (or ending exactly at
		// an insertion point): untouched, no shift.
		return r, true
	case r.Start >= e.End:
		// Entirely after the replaced region (or starting exactly at
		// an insertion point): untouched, shifted by the length delta.
		return Range{Start: r.Start + delta, End: r.End + delta}, true
	default:
		// Intersects the replaced region: the covered text may have
		// changed, so the diagnostic is invalidated.
		return Range{}, false
	}
}
