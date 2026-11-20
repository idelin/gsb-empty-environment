package cgocheck

import (
	"fmt"
	"reflect"
	"sort"
	"unsafe"
)

// Layout is the memory layout of a C block.  Fields may either carry a Go
// mirror type, in which case pointer slots are discovered from that type, or
// be marked explicitly for layouts for which no Go type exists.
type Layout []Field

// FieldLayout is an alias for Layout.
type FieldLayout = Layout

// CField is an alias for Field.
type CField = Field

// Field describes one field in a C-allocated block.
//
// Offset is relative to the start of the registered C block.  Size is the
// byte span occupied by the field.  Type is optional and is useful when the C
// structure has a Go mirror type.  Pointer marks the field as containing one
// or more pointer-sized pointer words; Count controls how many words are
// present and defaults to one.  HasPointer is an alias for Pointer.
type Field struct {
	Name       string
	Offset     uintptr
	Size       uintptr
	Type       reflect.Type
	Pointer    bool
	HasPointer bool
	IsPointer  bool
	Count      uintptr
}

// PtrField returns an explicit pointer field at offset.
func PtrField(name string, offset uintptr) Field {
	return Field{
		Name:    name,
		Offset:  offset,
		Size:    unsafe.Sizeof(uintptr(0)),
		Pointer: true,
		Count:   1,
	}
}

// PointerArrayField returns a field containing count consecutive pointers.
func PointerArrayField(name string, offset, count uintptr) Field {
	return Field{
		Name:    name,
		Offset:  offset,
		Size:    count * unsafe.Sizeof(uintptr(0)),
		Pointer: true,
		Count:   count,
	}
}

// DataField returns a non-pointer field at offset.
func DataField(name string, offset, size uintptr) Field {
	return Field{Name: name, Offset: offset, Size: size}
}

// FieldOf returns a field whose pointer slots are inferred from t.
func FieldOf(name string, offset uintptr, t reflect.Type) Field {
	f := Field{Name: name, Offset: offset, Type: t}
	if t != nil {
		f.Size = t.Size()
	}
	return f
}

// TypeLayout converts a Go mirror of a C type into a field layout.
func TypeLayout(t reflect.Type) Layout {
	if t == nil {
		return nil
	}
	if t.Kind() != reflect.Struct {
		return Layout{{Size: t.Size(), Type: t}}
	}
	layout := make(Layout, 0, t.NumField())
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		layout = append(layout, Field{
			Name:   f.Name,
			Offset: f.Offset,
			Size:   f.Type.Size(),
			Type:   f.Type,
		})
	}
	return layout
}

// LayoutFromType is an alias for TypeLayout.
func LayoutFromType(t reflect.Type) Layout {
	return TypeLayout(t)
}

// normalizeLayout accepts the convenient forms a wrapper commonly has at
// hand: Field values, a Layout, []Field, and reflect.Type.
func normalizeLayout(layout []any) ([]Field, error) {
	var fields []Field
	for _, item := range layout {
		switch v := item.(type) {
		case nil:
			continue
		case Field:
			fields = append(fields, v)
		case Layout:
			fields = append(fields, v...)
		case []Field:
			fields = append(fields, v...)
		case []uintptr:
			for i, offset := range v {
				fields = append(fields, Field{
					Name:    fmt.Sprintf("pointer%d", i),
					Offset:  offset,
					Size:    unsafe.Sizeof(uintptr(0)),
					Pointer: true,
					Count:   1,
				})
			}
		case map[uintptr]bool:
			offsets := make([]uintptr, 0, len(v))
			for offset, isPointer := range v {
				if isPointer {
					offsets = append(offsets, offset)
				}
			}
			sort.Slice(offsets, func(i, j int) bool { return offsets[i] < offsets[j] })
			for i, offset := range offsets {
				fields = append(fields, Field{
					Name:    fmt.Sprintf("pointer%d", i),
					Offset:  offset,
					Size:    unsafe.Sizeof(uintptr(0)),
					Pointer: true,
					Count:   1,
				})
			}
		case reflect.Type:
			fields = append(fields, TypeLayout(v)...)
		default:
			return nil, fmt.Errorf("cgocheck: unsupported layout item %T", item)
		}
	}
	return fields, nil
}
