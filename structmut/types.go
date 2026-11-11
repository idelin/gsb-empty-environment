package structmut

import "errors"

// SectionID identifies a section or a field. Field IDs only need to be unique
// within their owning section.
type SectionID string

// FieldKind is the structural role of an integer field.
type FieldKind int

const (
	// KindLength stores the unsigned byte length of the target section.
	KindLength FieldKind = iota + 1
	// KindAbsOffset stores the unsigned absolute blob offset of the target
	// section's first byte.
	KindAbsOffset
	// KindRelOffset stores the signed offset of the target section's first
	// byte relative to the field's own first byte; negative values are legal.
	KindRelOffset
)

// Width is the byte width of an integer field. Only 1, 2, 4 and 8 are valid.
type Width int

// Field is an integer value embedded at Offset inside its owning section's
// body. The mutator treats the byte range [Offset, Offset+Width) as reserved:
// body edits may not touch it, and insert/delete operations relocate Offset.
type Field struct {
	ID     SectionID
	Offset int
	Kind   FieldKind
	Width  Width
	Target SectionID
}

// Section is one contiguous, alignment-padded region of the blob. Fields are
// embedded inside Body at their Field.Offset. After lists sections that must
// be placed before this one; it expresses layout dependencies only and is
// independent of field references.
type Section struct {
	ID     SectionID
	Body   []byte
	Align  int
	After  []SectionID
	Fields []Field
}

// Schema describes a binary format instance.
type Schema struct {
	Sections []*Section
}

// Sentinel errors. Callers use errors.Is to classify rejected candidates.
var (
	// ErrNilSchema is returned when a nil schema is used.
	ErrNilSchema = errors.New("structmut: nil schema")
	// ErrEmptySchema is returned when a schema has no sections.
	ErrEmptySchema = errors.New("structmut: schema has no sections")
	// ErrNilSection is returned when the section list contains a nil entry.
	ErrNilSection = errors.New("structmut: nil section")
	// ErrEmptyID is returned when a section or field has an empty ID.
	ErrEmptyID = errors.New("structmut: empty section or field id")
	// ErrDuplicateSectionID is returned when two sections share an ID.
	ErrDuplicateSectionID = errors.New("structmut: duplicate section id")
	// ErrDuplicateFieldID is returned when two fields in one section share an ID.
	ErrDuplicateFieldID = errors.New("structmut: duplicate field id in section")
	// ErrUnknownTarget is returned when a field or dependency names a missing section.
	ErrUnknownTarget = errors.New("structmut: unknown reference target")
	// ErrUnknownSection is returned when an op names a missing section.
	ErrUnknownSection = errors.New("structmut: unknown section in op")
	// ErrInvalidAlignment is returned when Align is not a power of two in [1,64].
	ErrInvalidAlignment = errors.New("structmut: alignment must be a power of two in [1,64]")
	// ErrInvalidWidth is returned when a field width is not 1, 2, 4 or 8.
	ErrInvalidWidth = errors.New("structmut: invalid field width")
	// ErrInvalidKind is returned when a field kind is unknown.
	ErrInvalidKind = errors.New("structmut: invalid field kind")
	// ErrFieldOutOfBounds is returned when a field range leaves its body/blob.
	ErrFieldOutOfBounds = errors.New("structmut: field range out of bounds")
	// ErrFieldOverlap is returned when fields overlap or an edit touches a field.
	ErrFieldOverlap = errors.New("structmut: field range overlaps another field or edit")
	// ErrInvalidOffset is returned when an op offset is outside the body.
	ErrInvalidOffset = errors.New("structmut: op offset out of range")
	// ErrNilOp is returned when a nil op is passed to Apply.
	ErrNilOp = errors.New("structmut: nil op")
	// ErrLayoutCycle is returned when the Section.After graph has a cycle.
	ErrLayoutCycle = errors.New("structmut: layout dependency cycle")
	// ErrLengthOverflow is returned when a length does not fit its field width.
	ErrLengthOverflow = errors.New("structmut: section length overflows its length field")
	// ErrOffsetOverflow is returned when an offset does not fit its field width.
	ErrOffsetOverflow = errors.New("structmut: offset overflows its offset field")
	// ErrVerification is returned when the independent read-back check fails.
	ErrVerification = errors.New("structmut: independent read-back verification failed")
)

func (k FieldKind) valid() bool {
	switch k {
	case KindLength, KindAbsOffset, KindRelOffset:
		return true
	default:
		return false
	}
}

func (sch *Schema) find(id SectionID) *Section {
	for _, sec := range sch.Sections {
		if sec.ID == id {
			return sec
		}
	}
	return nil
}

// validate checks every structural invariant of the schema.
func (sch *Schema) validate() error {
	if sch == nil {
		return ErrNilSchema
	}
	if len(sch.Sections) == 0 {
		return ErrEmptySchema
	}
	known := make(map[SectionID]struct{}, len(sch.Sections))
	for _, sec := range sch.Sections {
		if sec == nil {
			return ErrNilSection
		}
		if sec.ID == "" {
			return ErrEmptyID
		}
		if _, dup := known[sec.ID]; dup {
			return ErrDuplicateSectionID
		}
		known[sec.ID] = struct{}{}
	}
	for _, sec := range sch.Sections {
		if sec.Align < 0 || sec.Align > 64 || (sec.Align > 1 && sec.Align&(sec.Align-1) != 0) {
			return ErrInvalidAlignment
		}
		for _, dep := range sec.After {
			if _, ok := known[dep]; !ok {
				return ErrUnknownTarget
			}
		}
		fieldIDs := make(map[SectionID]struct{}, len(sec.Fields))
		for i, f := range sec.Fields {
			if f.ID == "" {
				return ErrEmptyID
			}
			if _, dup := fieldIDs[f.ID]; dup {
				return ErrDuplicateFieldID
			}
			fieldIDs[f.ID] = struct{}{}
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
			for j := 0; j < i; j++ {
				g := sec.Fields[j]
				if f.Offset < g.Offset+int(g.Width) && g.Offset < f.Offset+int(f.Width) {
					return ErrFieldOverlap
				}
			}
		}
	}
	return nil
}

// clone returns a deep copy so mutation never aliases the receiver's slices.
func (sch *Schema) clone() *Schema {
	cp := &Schema{Sections: make([]*Section, len(sch.Sections))}
	for i, sec := range sch.Sections {
		cp.Sections[i] = &Section{
			ID:     sec.ID,
			Body:   append([]byte(nil), sec.Body...),
			Align:  sec.Align,
			After:  append([]SectionID(nil), sec.After...),
			Fields: append([]Field(nil), sec.Fields...),
		}
	}
	return cp
}
