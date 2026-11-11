package structmut

import (
	"encoding/binary"
	"fmt"
)

// SectionInfo is one region reconstructed by the independent reader.
type SectionInfo struct {
	ID     SectionID
	Start  int
	Length int
	Align  int
}

// Reference is one cross-section reference decoded straight from the blob.
// Value is unsigned for KindLength/KindAbsOffset and signed for
// KindRelOffset (negative values report the real signed displacement).
type Reference struct {
	Owner    SectionID
	Field    SectionID
	Kind     FieldKind
	Target   SectionID
	Position int
	Value    int64
}

// Report is the reader's reconstruction. TargetCounts is >= 2 for targets
// referenced more than once; ZeroLength lists sections whose length is zero.
type Report struct {
	Sections     []SectionInfo
	References   []Reference
	TargetCounts map[SectionID]int
	ZeroLength   []SectionID
}

// Read independently parses blob against schema and verifies every embedded
// reference. It re-derives layout with its own topological sort and placement
// code and decodes integers directly with encoding/binary, never trusting the
// mutator's in-memory values. It reads blob and schema without mutating them.
func Read(blob []byte, schema *Schema) (*Report, error) {
	if err := verifySchema(schema); err != nil {
		return nil, err
	}
	order, err := readOrder(schema)
	if err != nil {
		return nil, err
	}
	starts, total, err := readPlace(schema, order)
	if err != nil {
		return nil, err
	}
	if total != len(blob) {
		return nil, fmt.Errorf("%w: blob length %d, expected %d", ErrVerification, len(blob), total)
	}

	report := &Report{TargetCounts: map[SectionID]int{}}
	cursor := 0
	for _, id := range order {
		sec := sectionByID(schema, id)
		start := starts[id]
		for pad := cursor; pad < start; pad++ {
			if blob[pad] != 0 {
				return nil, fmt.Errorf("%w: non-zero padding byte at %d", ErrVerification, pad)
			}
		}
		end := start + len(sec.Body)
		if end > len(blob) {
			return nil, fmt.Errorf("%w: section %q runs past blob", ErrFieldOutOfBounds, id)
		}
		report.Sections = append(report.Sections, SectionInfo{
			ID: id, Start: start, Length: len(sec.Body), Align: sec.Align,
		})
		if len(sec.Body) == 0 {
			report.ZeroLength = append(report.ZeroLength, id)
		}
		cursor = end

		for _, f := range sec.Fields {
			pos := start + f.Offset
			if pos < 0 || pos+int(f.Width) > len(blob) || pos+int(f.Width) > end {
				return nil, fmt.Errorf("%w: field %q/%q", ErrFieldOutOfBounds, id, f.ID)
			}
			raw := decodeUint(blob[pos:], f.Width)
			targetStart := starts[f.Target]
			targetLen := len(sectionByID(schema, f.Target).Body)
			ref := Reference{Owner: id, Field: f.ID, Kind: f.Kind, Target: f.Target, Position: pos}

			switch f.Kind {
			case KindLength:
				if raw != uint64(targetLen) {
					return nil, fmt.Errorf("%w: field %q/%q reads %d, target length %d",
						ErrVerification, id, f.ID, raw, targetLen)
				}
				ref.Value = int64(raw)
			case KindAbsOffset:
				if raw != uint64(targetStart) {
					return nil, fmt.Errorf("%w: field %q/%q reads %d, target starts %d",
						ErrVerification, id, f.ID, raw, targetStart)
				}
				ref.Value = int64(raw)
			case KindRelOffset:
				rel := signExtend(raw, f.Width)
				want := int64(targetStart) - int64(pos)
				if rel != want {
					return nil, fmt.Errorf("%w: rel field %q/%q reads %d, want %d",
						ErrVerification, id, f.ID, rel, want)
				}
				ref.Value = rel
			}
			report.References = append(report.References, ref)
			report.TargetCounts[f.Target]++
		}
	}
	return report, nil
}

// verifySchema is the reader's own structural check; it intentionally does
// not call the mutator's Schema.validate.
func verifySchema(schema *Schema) error {
	if schema == nil {
		return ErrNilSchema
	}
	if len(schema.Sections) == 0 {
		return ErrEmptySchema
	}
	known := map[SectionID]struct{}{}
	for _, sec := range schema.Sections {
		switch {
		case sec == nil:
			return ErrNilSection
		case sec.ID == "":
			return ErrEmptyID
		}
		if _, dup := known[sec.ID]; dup {
			return ErrDuplicateSectionID
		}
		known[sec.ID] = struct{}{}
	}
	for _, sec := range schema.Sections {
		if sec.Align < 1 || sec.Align > 64 || sec.Align&(sec.Align-1) != 0 {
			return ErrInvalidAlignment
		}
		seenField := map[SectionID]struct{}{}
		for _, f := range sec.Fields {
			if f.ID == "" {
				return ErrEmptyID
			}
			if _, dup := seenField[f.ID]; dup {
				return ErrDuplicateFieldID
			}
			seenField[f.ID] = struct{}{}
			if !f.Kind.valid() {
				return ErrInvalidKind
			}
			if !validWidth(f.Width) {
				return ErrInvalidWidth
			}
			if _, ok := known[f.Target]; !ok {
				return ErrUnknownTarget
			}
			if f.Offset < 0 || f.Offset+int(f.Width) > len(sec.Body) {
				return ErrFieldOutOfBounds
			}
		}
		for _, dep := range sec.After {
			if _, ok := known[dep]; !ok {
				return ErrUnknownTarget
			}
		}
	}
	return nil
}

// readOrder is the reader's own Kahn implementation with schema-order tie
// breaking. It rejects layout dependency cycles.
func readOrder(schema *Schema) ([]SectionID, error) {
	indeg := map[SectionID]int{}
	adj := map[SectionID][]SectionID{}
	for _, sec := range schema.Sections {
		indeg[sec.ID] = 0
	}
	for _, sec := range schema.Sections {
		seen := map[SectionID]struct{}{}
		for _, dep := range sec.After {
			if _, dup := seen[dep]; dup {
				continue
			}
			seen[dep] = struct{}{}
			adj[dep] = append(adj[dep], sec.ID)
			indeg[sec.ID]++
		}
	}
	ready := []SectionID{}
	for _, sec := range schema.Sections {
		if indeg[sec.ID] == 0 {
			ready = append(ready, sec.ID)
		}
	}
	order := make([]SectionID, 0, len(schema.Sections))
	for len(ready) > 0 {
		id := ready[0]
		ready = ready[1:]
		order = append(order, id)
		for _, next := range adj[id] {
			indeg[next]--
			if indeg[next] == 0 {
				ready = append(ready, next)
			}
		}
	}
	if len(order) != len(schema.Sections) {
		return nil, ErrLayoutCycle
	}
	return order, nil
}

// readPlace independently computes section starts with alignment padding.
func readPlace(schema *Schema, order []SectionID) (map[SectionID]int, int, error) {
	starts := make(map[SectionID]int, len(order))
	cursor := 0
	for _, id := range order {
		sec := sectionByID(schema, id)
		align := sec.Align
		if align > 1 {
			cursor = (cursor + align - 1) &^ (align - 1)
		}
		starts[id] = cursor
		cursor += len(sec.Body)
		if cursor < 0 {
			return nil, 0, ErrOffsetOverflow
		}
	}
	return starts, cursor, nil
}

func sectionByID(schema *Schema, id SectionID) *Section {
	for _, sec := range schema.Sections {
		if sec.ID == id {
			return sec
		}
	}
	return nil
}

// decodeUint reads a little-endian unsigned integer without going through
// the mutator's codec helpers.
func decodeUint(b []byte, w Width) uint64 {
	switch w {
	case 1:
		return uint64(b[0])
	case 2:
		return uint64(binary.LittleEndian.Uint16(b))
	case 4:
		return uint64(binary.LittleEndian.Uint32(b))
	default:
		return binary.LittleEndian.Uint64(b)
	}
}

// signExtend re-interprets an unsigned decoded value as a signed two's
// complement integer of the given width.
func signExtend(v uint64, w Width) int64 {
	if w == 8 {
		return int64(v)
	}
	bits := 8 * uint(w)
	if v&(uint64(1)<<(bits-1)) != 0 {
		v |= ^uint64(0) << bits
	}
	return int64(v)
}
