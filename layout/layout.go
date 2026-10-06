// Package layout computes C-like memory layouts for int, struct, union and
// array types: field offsets, alignment, tail padding, with integer-overflow
// and recursive-value-type checks.
package layout

import (
	"errors"
	"fmt"
	"math"
)

// Kind enumerates the supported type forms.
type Kind int

const (
	Int Kind = iota
	Struct
	Union
	Array
)

// Type describes an int, struct, union or array type.
type Type struct {
	Kind    Kind
	Name    string
	Size    int64   // Int only
	Align   int64   // Int only
	Fields  []Field // Struct only
	Members []Field // Union only
	Elem    *Type   // Array only
	Count   int64   // Array only
}

// Field is a named member of a struct or union.
type Field struct {
	Name string
	Type *Type
}

// FieldLayout records one field's offset and size within its parent.
type FieldLayout struct {
	Name   string
	Offset int64
	Size   int64
}

// Layout is the computed size, alignment and field offsets of a type.
type Layout struct {
	Size   int64
	Align  int64
	Fields []FieldLayout // Struct and Union only
}

var (
	errOverflow = errors.New("layout: size overflow")
)

// RecursiveError reports a type that contains itself by value.
type RecursiveError struct{ Name string }

func (e *RecursiveError) Error() string {
	return fmt.Sprintf("layout: recursive value type %q", e.Name)
}

func checkedAdd(a, b int64) (int64, error) {
	if a < 0 || b < 0 || a > math.MaxInt64-b {
		return 0, errOverflow
	}
	return a + b, nil
}

func checkedMul(a, b int64) (int64, error) {
	if a < 0 || b < 0 {
		return 0, errOverflow
	}
	if a != 0 && b > math.MaxInt64/a {
		return 0, errOverflow
	}
	return a * b, nil
}

// alignUp rounds off up to the next multiple of align, checking overflow.
func alignUp(off, align int64) (int64, error) {
	if align <= 0 || align&(align-1) != 0 {
		return 0, fmt.Errorf("layout: invalid alignment %d", align)
	}
	r, err := checkedAdd(off, align-1)
	if err != nil {
		return 0, err
	}
	return r &^ (align - 1), nil
}

// LayoutOf computes the layout of t, rejecting recursive value types and
// size overflows.
func LayoutOf(t *Type) (*Layout, error) {
	if t == nil {
		return nil, errors.New("layout: nil type")
	}
	return compute(t, make(map[*Type]bool))
}

func compute(t *Type, visiting map[*Type]bool) (*Layout, error) {
	if t == nil {
		return nil, errors.New("layout: nil element/field type")
	}
	switch t.Kind {
	case Int:
		if t.Size <= 0 {
			return nil, fmt.Errorf("layout: int %q has non-positive size %d", t.Name, t.Size)
		}
		if t.Align <= 0 || t.Align&(t.Align-1) != 0 {
			return nil, fmt.Errorf("layout: int %q has invalid alignment %d", t.Name, t.Align)
		}
		if t.Align > t.Size {
			return nil, fmt.Errorf("layout: int %q alignment %d exceeds size %d", t.Name, t.Align, t.Size)
		}
		return &Layout{Size: t.Size, Align: t.Align}, nil

	case Array:
		if t.Count < 0 {
			return nil, fmt.Errorf("layout: array %q has negative count %d", t.Name, t.Count)
		}
		el, err := compute(t.Elem, visiting)
		if err != nil {
			return nil, err
		}
		// Stride equals the element size, which already includes the
		// element's tail padding, so the whole array is size*count.
		size, err := checkedMul(el.Size, t.Count)
		if err != nil {
			return nil, err
		}
		return &Layout{Size: size, Align: el.Align}, nil

	case Struct, Union:
		if visiting[t] {
			return nil, &RecursiveError{Name: t.Name}
		}
		visiting[t] = true
		defer delete(visiting, t)

		members := t.Fields
		if t.Kind == Union {
			members = t.Members
		}
		l := &Layout{Align: 1}
		maxAlign := int64(1)
		offset := int64(0)
		maxSize := int64(0)
		for _, m := range members {
			if m.Type == nil {
				return nil, fmt.Errorf("layout: member %q of %q has nil type", m.Name, t.Name)
			}
			ml, err := compute(m.Type, visiting)
			if err != nil {
				return nil, err
			}
			if ml.Align > maxAlign {
				maxAlign = ml.Align
			}
			if t.Kind == Struct {
				off, err := alignUp(offset, ml.Align)
				if err != nil {
					return nil, err
				}
				l.Fields = append(l.Fields, FieldLayout{Name: m.Name, Offset: off, Size: ml.Size})
				offset, err = checkedAdd(off, ml.Size)
				if err != nil {
					return nil, err
				}
			} else {
				// Union members all start at offset 0.
				l.Fields = append(l.Fields, FieldLayout{Name: m.Name, Offset: 0, Size: ml.Size})
				if ml.Size > maxSize {
					maxSize = ml.Size
				}
			}
		}
		var size int64
		if t.Kind == Struct {
			size = offset
		} else {
			size = maxSize
		}
		// Tail padding: round the total up to the struct/union alignment.
		size, err := alignUp(size, maxAlign)
		if err != nil {
			return nil, err
		}
		l.Size = size
		l.Align = maxAlign
		return l, nil
	}
	return nil, fmt.Errorf("layout: unknown kind %d", t.Kind)
}
