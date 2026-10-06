// Package layout computes byte offsets, sizes, and alignments for
// C-like value types: primitives, structs, unions, and arrays.
//
// Rules:
//   - A struct's alignment is the maximum of its field alignments;
//     each field is placed at the next offset that is a multiple of
//     its own alignment, and the struct size is padded up to a
//     multiple of the struct alignment (tail padding).
//   - All union members start at offset 0; the union's size is the
//     maximum member size padded up to the union alignment, which is
//     the maximum member alignment.
//   - An array's stride is its element size rounded up to the element
//     alignment, so the element tail padding is included in the step.
//     A zero-length array has size 0 but keeps its element alignment.
//
// Compute reports an error on integer overflow and on recursive value
// types (a type that contains itself by value).
package layout

import (
	"errors"
	"fmt"
	"math"
	"strings"
)

// Kind enumerates the supported type constructors.
type Kind int

const (
	Prim Kind = iota
	Struct
	Union
	Array
)

// Field is a named member of a struct or union.
type Field struct {
	Name string
	Type *Type
}

// Type describes a value type. Exactly the fields relevant to Kind
// are used:
//   - Prim:   Size, Align
//   - Struct: Fields
//   - Union:  Fields
//   - Array:  Elem, Count
type Type struct {
	Kind   Kind
	Name   string
	Size   int64
	Align  int64
	Fields []Field
	Elem   *Type
	Count  int64
}

// FieldLayout is the computed placement of one struct field or union
// member.
type FieldLayout struct {
	Name   string
	Offset int64
	Size   int64
	Align  int64
}

// Layout is the computed size, alignment, and per-field offsets of a
// type.
type Layout struct {
	Size   int64
	Align  int64
	Fields []FieldLayout
}

// String renders the layout with one line per field, e.g.
//
//	size=16 align=8
//	  +0 a size=1 align=1
//	  +8 b size=8 align=8
func (l Layout) String() string {
	var b strings.Builder
	fmt.Fprintf(&b, "size=%d align=%d", l.Size, l.Align)
	for _, f := range l.Fields {
		fmt.Fprintf(&b, "\n  +%d %s size=%d align=%d", f.Offset, f.Name, f.Size, f.Align)
	}
	return b.String()
}

// Compute resolves the layout of t. It is safe to share *Type values
// between fields; results are memoized per pointer.
func Compute(t *Type) (Layout, error) {
	c := &computer{
		active: make(map[*Type]bool),
		memo:   make(map[*Type]Layout),
	}
	return c.compute(t)
}

type computer struct {
	active map[*Type]bool // types on the current expansion stack
	memo   map[*Type]Layout
}

func (c *computer) compute(t *Type) (Layout, error) {
	if t == nil {
		return Layout{}, errors.New("layout: nil type")
	}
	if l, ok := c.memo[t]; ok {
		return l, nil
	}
	if c.active[t] {
		return Layout{}, fmt.Errorf("layout: recursive value type %q", typeName(t))
	}
	c.active[t] = true
	defer delete(c.active, t)

	var l Layout
	var err error
	switch t.Kind {
	case Prim:
		l, err = layoutPrim(t)
	case Struct:
		l, err = c.layoutStruct(t)
	case Union:
		l, err = c.layoutUnion(t)
	case Array:
		l, err = c.layoutArray(t)
	default:
		err = fmt.Errorf("layout: unknown kind %d for %q", t.Kind, typeName(t))
	}
	if err != nil {
		return Layout{}, err
	}
	c.memo[t] = l
	return l, nil
}

func typeName(t *Type) string {
	if t.Name != "" {
		return t.Name
	}
	return "<unnamed>"
}

// alignUp rounds n up to a multiple of align, reporting overflow.
func alignUp(n, align int64) (int64, error) {
	if rem := n % align; rem != 0 {
		add := align - rem
		if n > math.MaxInt64-add {
			return 0, fmt.Errorf("layout: size overflow aligning %d to %d", n, align)
		}
		n += add
	}
	return n, nil
}

func validAlign(align int64) bool {
	return align > 0 && align&(align-1) == 0
}

func layoutPrim(t *Type) (Layout, error) {
	if t.Size < 0 {
		return Layout{}, fmt.Errorf("layout: negative size %d for %q", t.Size, typeName(t))
	}
	if !validAlign(t.Align) {
		return Layout{}, fmt.Errorf("layout: invalid alignment %d for %q", t.Align, typeName(t))
	}
	return Layout{Size: t.Size, Align: t.Align}, nil
}

func (c *computer) layoutStruct(t *Type) (Layout, error) {
	l := Layout{Align: 1}
	off := int64(0)
	for _, f := range t.Fields {
		if f.Type == nil {
			return Layout{}, fmt.Errorf("layout: nil type for field %q of %q", f.Name, typeName(t))
		}
		fl, err := c.compute(f.Type)
		if err != nil {
			return Layout{}, err
		}
		off, err = alignUp(off, fl.Align)
		if err != nil {
			return Layout{}, fmt.Errorf("field %q of %q: %v", f.Name, typeName(t), err)
		}
		l.Fields = append(l.Fields, FieldLayout{
			Name:   f.Name,
			Offset: off,
			Size:   fl.Size,
			Align:  fl.Align,
		})
		if fl.Size > 0 && off > math.MaxInt64-fl.Size {
			return Layout{}, fmt.Errorf("layout: size overflow at field %q of %q", f.Name, typeName(t))
		}
		off += fl.Size
		if fl.Align > l.Align {
			l.Align = fl.Align
		}
	}
	size, err := alignUp(off, l.Align)
	if err != nil {
		return Layout{}, fmt.Errorf("struct %q: %v", typeName(t), err)
	}
	l.Size = size
	return l, nil
}

func (c *computer) layoutUnion(t *Type) (Layout, error) {
	l := Layout{Align: 1}
	for _, f := range t.Fields {
		if f.Type == nil {
			return Layout{}, fmt.Errorf("layout: nil type for member %q of %q", f.Name, typeName(t))
		}
		fl, err := c.compute(f.Type)
		if err != nil {
			return Layout{}, err
		}
		l.Fields = append(l.Fields, FieldLayout{
			Name:   f.Name,
			Offset: 0,
			Size:   fl.Size,
			Align:  fl.Align,
		})
		if fl.Size > l.Size {
			l.Size = fl.Size
		}
		if fl.Align > l.Align {
			l.Align = fl.Align
		}
	}
	size, err := alignUp(l.Size, l.Align)
	if err != nil {
		return Layout{}, fmt.Errorf("union %q: %v", typeName(t), err)
	}
	l.Size = size
	return l, nil
}

func (c *computer) layoutArray(t *Type) (Layout, error) {
	if t.Elem == nil {
		return Layout{}, fmt.Errorf("layout: array %q has nil element type", typeName(t))
	}
	if t.Count < 0 {
		return Layout{}, fmt.Errorf("layout: negative count %d for array %q", t.Count, typeName(t))
	}
	el, err := c.compute(t.Elem)
	if err != nil {
		return Layout{}, err
	}
	// The element size is already a multiple of its alignment (tail
	// padding included), so the stride equals the element size.
	stride := el.Size
	if t.Count > 0 && stride > math.MaxInt64/t.Count {
		return Layout{}, fmt.Errorf("layout: size overflow for array %q of %d x %d bytes", typeName(t), t.Count, stride)
	}
	return Layout{Size: stride * t.Count, Align: el.Align}, nil
}
