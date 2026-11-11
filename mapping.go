package fieldbinding

import (
	"fmt"
	"math"
	"slices"
)

// Binding maps one table column, identified by its stable field ID, to its
// source field in the batch schema.
type Binding struct {
	FieldID   int
	BatchPath []int // field IDs from the batch root; nil when absent from the batch
	TablePath []int // field IDs from the table root
	From      Type  // batch-side type; zero value when absent from the batch
	To        Type  // table-side type
	Derived   bool  // struct node absent from the batch; state derived from children
}

// Mapping is a validated plan from a batch schema to the table schema.
type Mapping struct {
	table    *Schema
	bindings map[int]*Binding
}

// Binding returns the binding for a table field ID.
func (m *Mapping) Binding(id int) (*Binding, bool) {
	b, ok := m.bindings[id]
	return b, ok
}

// Plan validates a batch schema against a table schema and returns the
// resulting column mapping. Fields are bound exclusively by stable ID:
// a field that keeps its name but changes ID is treated as a different
// field, and a field that changes its name but keeps its ID is the same
// field (a rename).
func Plan(batch, table *Schema) (*Mapping, error) {
	if err := batch.validate(); err != nil {
		return nil, fmt.Errorf("batch schema: %w", err)
	}
	if err := table.validate(); err != nil {
		return nil, fmt.Errorf("table schema: %w", err)
	}
	bIdx := batch.index()
	tIdx := table.index()

	// Reject batch leaf fields with no table counterpart. Struct nodes are
	// exempt: they only contribute paths, and a moved subtree legitimately
	// sits under a parent the table does not know.
	for id, ref := range bIdx {
		if ref.field.Type.Kind == Struct {
			continue
		}
		if _, ok := tIdx[id]; !ok {
			return nil, fmt.Errorf("field %d (%q): %w", id, ref.field.Name, ErrUnknownField)
		}
	}

	m := &Mapping{table: table, bindings: make(map[int]*Binding, len(tIdx))}
	for id, dst := range tIdx {
		b := &Binding{FieldID: id, TablePath: pathIDs(dst.path), To: dst.field.Type}
		src, ok := bIdx[id]
		if !ok {
			switch {
			case dst.field.Type.Kind == Struct:
				// Introduced by a subtree move; its state is derived
				// from its children at projection time.
				b.Derived = true
			case dst.field.Type.Nullable:
				// Column unknown to the batch: stays Missing, never Null.
			default:
				return nil, fmt.Errorf("field %d (%q): %w", id, dst.field.Name, ErrMissingField)
			}
			m.bindings[id] = b
			continue
		}
		b.BatchPath = pathIDs(src.path)
		b.From = src.field.Type
		if err := checkType(src.field, dst.field); err != nil {
			return nil, err
		}
		if err := checkMove(src, dst); err != nil {
			return nil, err
		}
		m.bindings[id] = b
	}
	return m, nil
}

func checkType(src, dst Field) error {
	st, dt := src.Type, dst.Type
	if st.Kind != dt.Kind {
		numeric := st.Kind != Struct && dt.Kind != Struct
		if !numeric || !lossless(st.Kind, dt.Kind) {
			return fmt.Errorf("field %d (%q): %s -> %s: %w",
				src.ID, src.Name, st.Kind, dt.Kind, ErrIncompatibleType)
		}
	}
	if st.Nullable && !dt.Nullable {
		return fmt.Errorf("field %d (%q): %w", src.ID, src.Name, ErrNullability)
	}
	return nil
}

// checkMove permits a subtree move only when every ancestor on both the
// batch and table paths is nullable, and the moved root itself is nullable.
func checkMove(src, dst fieldRef) error {
	sp, dp := pathIDs(src.path), pathIDs(dst.path)
	if slices.Equal(sp, dp) {
		return nil
	}
	ancestorsNullable := func(chain []Field) bool {
		for _, f := range chain[:len(chain)-1] {
			if !f.Type.Nullable {
				return false
			}
		}
		return true
	}
	if !ancestorsNullable(src.path) || !ancestorsNullable(dst.path) {
		return fmt.Errorf("field %d (%q): %w", src.field.ID, src.field.Name, ErrIllegalMove)
	}
	if parentID(sp) != parentID(dp) && !src.field.Type.Nullable {
		return fmt.Errorf("field %d (%q): %w", src.field.ID, src.field.Name, ErrIllegalMove)
	}
	return nil
}

// Project validates one batch record against the mapping and converts it
// into a table record. Missing and Null states are preserved end to end
// and are never merged.
func (m *Mapping) Project(rec Value) (Value, error) {
	flat := make(map[int]Value, len(m.bindings))
	for id, b := range m.bindings {
		if b.Derived {
			continue
		}
		src, inherited := resolve(rec, b.BatchPath)
		out, err := convert(b, src, inherited)
		if err != nil {
			return Value{}, err
		}
		flat[id] = out
	}
	return Record(assemble(m.table.Fields, m.bindings, flat)), nil
}

// resolve walks the batch record along path. inherited reports whether the
// returned state was dictated by an ancestor (or by a column absent from
// the batch) rather than by the field itself.
func resolve(rec Value, path []int) (Value, bool) {
	if path == nil {
		return Value{}, true
	}
	cur := rec
	for _, id := range path[:len(path)-1] {
		next, ok := cur.Kids[id]
		if !ok {
			return Value{}, true
		}
		if next.State != Present {
			return Value{State: next.State}, true
		}
		cur = next
	}
	self, ok := cur.Kids[path[len(path)-1]]
	if !ok {
		return Value{}, false
	}
	return self, false
}

func convert(b *Binding, v Value, inherited bool) (Value, error) {
	if inherited {
		// State inherited from an ancestor or from a column the batch
		// does not know: propagate untouched, never coerce to Null.
		return v, nil
	}
	switch v.State {
	case Missing:
		if !b.To.Nullable {
			return Value{}, fmt.Errorf("field %d: %w", b.FieldID, ErrMissingValue)
		}
		return Value{}, nil
	case Null:
		if !b.To.Nullable {
			return Value{}, fmt.Errorf("field %d: %w", b.FieldID, ErrUnexpectedNull)
		}
		return ExplicitNull(), nil
	}
	return convertPresent(b, v)
}

func convertPresent(b *Binding, v Value) (Value, error) {
	switch b.To.Kind {
	case Int32, Int64:
		if v.K != Int64 {
			return kindErr(b, v)
		}
		if b.To.Kind == Int32 && (v.I64 < math.MinInt32 || v.I64 > math.MaxInt32) {
			return Value{}, fmt.Errorf("field %d: %d: %w", b.FieldID, v.I64, ErrRange)
		}
		return Int(v.I64), nil
	case Float32, Float64:
		var f float64
		switch v.K {
		case Int64:
			f = float64(v.I64)
			if int64(f) != v.I64 {
				return Value{}, fmt.Errorf("field %d: %d: %w", b.FieldID, v.I64, ErrRange)
			}
		case Float64:
			f = v.F64
		default:
			return kindErr(b, v)
		}
		if b.To.Kind == Float32 && !math.IsNaN(f) && f != float64(float32(f)) {
			return Value{}, fmt.Errorf("field %d: %v: %w", b.FieldID, f, ErrRange)
		}
		return Float(f), nil
	case String:
		if v.K != String {
			return kindErr(b, v)
		}
		return Str(v.S), nil
	case Bool:
		if v.K != Bool {
			return kindErr(b, v)
		}
		return Boolean(v.B), nil
	case Struct:
		if v.K != Struct {
			return kindErr(b, v)
		}
		return Record(nil), nil // children are attached during assembly
	}
	return kindErr(b, v)
}

func kindErr(b *Binding, v Value) (Value, error) {
	return Value{}, fmt.Errorf("field %d: value kind %s: %w", b.FieldID, v.K, ErrKind)
}

// assemble rebuilds the table record tree from the flat projected values.
func assemble(fields []Field, bindings map[int]*Binding, flat map[int]Value) map[int]Value {
	out := make(map[int]Value, len(fields))
	for _, f := range fields {
		if f.Type.Kind == Struct {
			kids := assemble(f.Type.Fields, bindings, flat)
			if b := bindings[f.ID]; b != nil && b.Derived {
				if anySet(kids) {
					out[f.ID] = Record(kids)
				} else {
					out[f.ID] = Value{} // Missing
				}
				continue
			}
			v := flat[f.ID]
			if v.State == Present {
				v.Kids = kids
			}
			out[f.ID] = v
			continue
		}
		out[f.ID] = flat[f.ID]
	}
	return out
}

func anySet(kids map[int]Value) bool {
	for _, v := range kids {
		if v.State != Missing {
			return true
		}
	}
	return false
}
