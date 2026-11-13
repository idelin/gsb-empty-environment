package netsec

import "fmt"

// ValueKind classifies the type of a struct field.
type ValueKind int

const (
	I32 ValueKind = iota
	I64
	F64
	// Ref is a reference to another struct type; FieldType.Ref holds the
	// absolute type index of the referent.
	Ref
)

// FieldType describes the type of one struct field. Ref is meaningful only
// when Kind == Ref and holds the absolute index of the target type in the
// enclosing type space.
type FieldType struct {
	Kind ValueKind
	Ref  int
}

// Field is one struct field. Field order within a struct is significant.
type Field struct {
	Type    FieldType
	Mutable bool
}

// NoSuper marks a struct without a declared supertype.
const NoSuper = -1

// StructType is one member of a recursive group. Super is the absolute
// index of the declared supertype, which must be an earlier type, or
// NoSuper.
type StructType struct {
	Final  bool
	Super  int
	Fields []Field
}

// RecGroup is one recursive group of struct types. Base is the absolute
// type index of Members[0] in the enclosing type space; a reference target
// t names a member of this group iff Base <= t < Base+len(Members), and an
// earlier type iff t < Base.
type RecGroup struct {
	Base    int
	Members []StructType
}

// Validate reports whether the group is well formed: non-empty, non-negative
// base, supertypes referring only to earlier types, and reference targets in
// range.
func (g RecGroup) Validate() error {
	if g.Base < 0 {
		return fmt.Errorf("netsec: negative base index %d", g.Base)
	}
	if len(g.Members) == 0 {
		return fmt.Errorf("netsec: empty rec group")
	}
	limit := g.Base + len(g.Members)
	for i, st := range g.Members {
		if st.Super != NoSuper && (st.Super < 0 || st.Super >= g.Base) {
			return fmt.Errorf("netsec: member %d: supertype index %d is not an earlier type", i, st.Super)
		}
		for j, f := range st.Fields {
			if f.Type.Kind == Ref && (f.Type.Ref < 0 || f.Type.Ref >= limit) {
				return fmt.Errorf("netsec: member %d field %d: ref target %d out of range", i, j, f.Type.Ref)
			}
		}
	}
	return nil
}

// intra reports whether absolute type index t names a member of g.
func (g RecGroup) intra(t int) bool {
	return g.Base <= t && t < g.Base+len(g.Members)
}
