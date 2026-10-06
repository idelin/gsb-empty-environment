package layout

import (
	"fmt"
	"math"
	"strings"
	"testing"
)

func prim(name string, size, align int64) *Type {
	return &Type{Kind: Prim, Name: name, Size: size, Align: align}
}

// byteModel re-derives a type's layout by placing one placeholder per
// byte into an occupancy map, independently of Compute's arithmetic.
// It verifies:
//   - every field offset is a multiple of the field's alignment;
//   - struct fields never overlap (union members may, all at base 0);
//   - the highest occupied byte plus tail padding equals Size;
//   - Size is a multiple of Align.
// Zero-length arrays occupy no bytes but still force alignment of
// whatever follows them.
func byteModel(t *testing.T, typ *Type, l Layout) {
	t.Helper()
	if l.Size%l.Align != 0 {
		t.Errorf("%s: size %d not a multiple of align %d", typ.Name, l.Size, l.Align)
	}
	switch typ.Kind {
	case Prim:
		return
	case Array:
		el, err := Compute(typ.Elem)
		if err != nil {
			t.Fatalf("byteModel: %v", err)
		}
		if typ.Count == 0 {
			if l.Size != 0 {
				t.Errorf("%s: zero-length array has size %d, want 0", typ.Name, l.Size)
			}
			if l.Align != el.Align {
				t.Errorf("%s: zero-length array align %d, want element align %d", typ.Name, l.Align, el.Align)
			}
			return
		}
		if l.Size != el.Size*typ.Count {
			t.Errorf("%s: array size %d, want stride %d x count %d = %d",
				typ.Name, l.Size, el.Size, typ.Count, el.Size*typ.Count)
		}
		if l.Align != el.Align {
			t.Errorf("%s: array align %d, want %d", typ.Name, l.Align, el.Align)
		}
		return
	}

	occ := make(map[int64]string)
	top := int64(0)
	for i, f := range typ.Fields {
		fl := l.Fields[i]
		if fl.Offset%fl.Align != 0 {
			t.Errorf("%s.%s: offset %d not aligned to %d", typ.Name, fl.Name, fl.Offset, fl.Align)
		}
		if typ.Kind == Union && fl.Offset != 0 {
			t.Errorf("%s.%s: union member offset %d, want 0", typ.Name, fl.Name, fl.Offset)
		}
		ft, err := Compute(f.Type)
		if err != nil {
			t.Fatalf("byteModel: %v", err)
		}
		if fl.Size != ft.Size || fl.Align != ft.Align {
			t.Errorf("%s.%s: recorded size/align %d/%d, want %d/%d",
				typ.Name, fl.Name, fl.Size, fl.Align, ft.Size, ft.Align)
		}
		// Place each byte of the field's footprint.
		for b := fl.Offset; b < fl.Offset+fl.Size; b++ {
			if prev, dup := occ[b]; dup && typ.Kind == Struct {
				t.Errorf("%s: byte %d claimed by both %s and %s", typ.Name, b, prev, fl.Name)
			}
			occ[b] = fl.Name
		}
		if end := fl.Offset + fl.Size; end > top {
			top = end
		}
	}
	// The footprint top, padded to the type alignment, must equal Size.
	want := top
	if rem := want % l.Align; rem != 0 {
		want += l.Align - rem
	}
	if l.Size != want {
		t.Errorf("%s: size %d, byte model wants %d (footprint top %d, align %d)",
			typ.Name, l.Size, want, top, l.Align)
	}
	// Recurse so nested structs/unions/arrays are checked too.
	for _, f := range typ.Fields {
		sub, err := Compute(f.Type)
		if err != nil {
			t.Fatalf("byteModel: %v", err)
		}
		byteModel(t, f.Type, sub)
	}
}

func check(t *testing.T, typ *Type, wantSize, wantAlign int64, wantOffsets ...int64) Layout {
	t.Helper()
	l, err := Compute(typ)
	if err != nil {
		t.Fatalf("Compute(%s): %v", typ.Name, err)
	}
	if l.Size != wantSize || l.Align != wantAlign {
		t.Errorf("%s: size=%d align=%d, want size=%d align=%d",
			typ.Name, l.Size, l.Align, wantSize, wantAlign)
	}
	if len(l.Fields) != len(wantOffsets) {
		t.Fatalf("%s: %d fields, want %d", typ.Name, len(l.Fields), len(wantOffsets))
	}
	for i, off := range wantOffsets {
		if l.Fields[i].Offset != off {
			t.Errorf("%s.%s: offset %d, want %d", typ.Name, l.Fields[i].Name, l.Fields[i].Offset, off)
		}
	}
	byteModel(t, typ, l)
	return l
}

func TestStructPaddingAndAlignment(t *testing.T) {
	s := &Type{Kind: Struct, Name: "S", Fields: []Field{
		{Name: "a", Type: prim("i8", 1, 1)},
		{Name: "b", Type: prim("i64", 8, 8)},
		{Name: "c", Type: prim("i16", 2, 2)},
	}}
	// a@0, pad to 8, b@8, c@16, tail pad 18 -> 24.
	check(t, s, 24, 8, 0, 8, 16)
}

func TestNestedStruct(t *testing.T) {
	inner := &Type{Kind: Struct, Name: "Inner", Fields: []Field{
		{Name: "x", Type: prim("i32", 4, 4)},
		{Name: "y", Type: prim("i8", 1, 1)},
	}}
	// Inner: x@0, y@4, size 8 (tail pad), align 4.
	check(t, inner, 8, 4, 0, 4)
	outer := &Type{Kind: Struct, Name: "Outer", Fields: []Field{
		{Name: "tag", Type: prim("i8", 1, 1)},
		{Name: "in", Type: inner},
		{Name: "n", Type: prim("i16", 2, 2)},
	}}
	// tag@0, pad to 4, in@4..12, n@12, size 16 (align 4).
	check(t, outer, 16, 4, 0, 4, 12)
}

func TestUnion(t *testing.T) {
	u := &Type{Kind: Union, Name: "U", Fields: []Field{
		{Name: "i", Type: prim("i32", 4, 4)},
		{Name: "d", Type: prim("f64", 8, 8)},
		{Name: "buf", Type: &Type{Kind: Array, Name: "buf3", Elem: prim("u8", 1, 1), Count: 3}},
	}}
	// All members at 0; size = max(4,8,3) = 8, align 8.
	check(t, u, 8, 8, 0, 0, 0)
}

func TestNestedUnionInStruct(t *testing.T) {
	inner := &Type{Kind: Union, Name: "Val", Fields: []Field{
		{Name: "small", Type: prim("i16", 2, 2)},
		{Name: "big", Type: &Type{Kind: Struct, Name: "Big", Fields: []Field{
			{Name: "p", Type: prim("i64", 8, 8)},
			{Name: "q", Type: prim("i8", 1, 1)},
		}}},
	}}
	// Big: p@0, q@8, size 16 align 8. Val: size 16 align 8, both at 0.
	check(t, inner, 16, 8, 0, 0)
	s := &Type{Kind: Struct, Name: "Rec", Fields: []Field{
		{Name: "kind", Type: prim("u8", 1, 1)},
		{Name: "val", Type: inner},
		{Name: "next", Type: prim("u32", 4, 4)},
	}}
	// kind@0, pad to 8, val@8..24, next@24, size 32 align 8.
	check(t, s, 32, 8, 0, 8, 24)
}

func TestArrayStrideIncludesTailPadding(t *testing.T) {
	elem := &Type{Kind: Struct, Name: "E", Fields: []Field{
		{Name: "v", Type: prim("i32", 4, 4)},
		{Name: "tag", Type: prim("u8", 1, 1)},
	}}
	// E: size 8 (4+1 padded to align 4), align 4.
	check(t, elem, 8, 4, 0, 4)
	arr := &Type{Kind: Array, Name: "E3", Elem: elem, Count: 3}
	// Stride 8 (tail padding included): total 24.
	l, err := Compute(arr)
	if err != nil {
		t.Fatal(err)
	}
	if l.Size != 24 || l.Align != 4 {
		t.Errorf("array: size=%d align=%d, want 24/4", l.Size, l.Align)
	}
	byteModel(t, arr, l)
}

func TestZeroLengthArray(t *testing.T) {
	z := &Type{Kind: Array, Name: "z0", Elem: prim("i32", 4, 4), Count: 0}
	s := &Type{Kind: Struct, Name: "Z", Fields: []Field{
		{Name: "a", Type: prim("u8", 1, 1)},
		{Name: "z", Type: z},
		{Name: "b", Type: prim("i32", 4, 4)},
	}}
	// z occupies no bytes but still forces i32 alignment:
	// a@0, z@4 (size 0), b@4, size 8.
	l := check(t, s, 8, 4, 0, 4, 4)
	if l.Fields[1].Size != 0 {
		t.Errorf("zero-length array field size %d, want 0", l.Fields[1].Size)
	}

	// Trailing zero-length array: size still padded to element align.
	s2 := &Type{Kind: Struct, Name: "Z2", Fields: []Field{
		{Name: "a", Type: prim("u8", 1, 1)},
		{Name: "z", Type: z},
	}}
	check(t, s2, 4, 4, 0, 4)

	// Zero-length array of zero-length arrays.
	zz := &Type{Kind: Array, Name: "zz", Elem: z, Count: 0}
	l2, err := Compute(zz)
	if err != nil {
		t.Fatal(err)
	}
	if l2.Size != 0 || l2.Align != 4 {
		t.Errorf("nested zero array: size=%d align=%d, want 0/4", l2.Size, l2.Align)
	}
	byteModel(t, zz, l2)
}

func TestZeroLengthArrayInUnion(t *testing.T) {
	u := &Type{Kind: Union, Name: "UZ", Fields: []Field{
		{Name: "z", Type: &Type{Kind: Array, Elem: prim("i64", 8, 8), Count: 0}},
		{Name: "w", Type: prim("u16", 2, 2)},
	}}
	// Zero-size member contributes alignment 8; size = max(0,2)=2
	// padded to 8.
	check(t, u, 8, 8, 0, 0)
}

func TestRecursiveValueType(t *testing.T) {
	node := &Type{Kind: Struct, Name: "Node"}
	node.Fields = []Field{
		{Name: "v", Type: prim("i32", 4, 4)},
		{Name: "next", Type: node}, // by-value self-reference
	}
	if _, err := Compute(node); err == nil || !strings.Contains(err.Error(), "recursive") {
		t.Errorf("want recursive value type error, got %v", err)
	}

	// Indirect recursion through an array and a union.
	a := &Type{Kind: Struct, Name: "A"}
	b := &Type{Kind: Union, Name: "B", Fields: []Field{
		{Name: "arr", Type: &Type{Kind: Array, Elem: a, Count: 2}},
	}}
	a.Fields = []Field{{Name: "b", Type: b}}
	if _, err := Compute(a); err == nil || !strings.Contains(err.Error(), "recursive") {
		t.Errorf("want indirect recursion error, got %v", err)
	}
}

func TestOverflow(t *testing.T) {
	cases := []*Type{
		// Array count x stride overflows.
		{Kind: Array, Name: "big", Elem: prim("i64", 8, 8), Count: math.MaxInt64 / 4},
		// Array of huge arrays overflows in multiplication.
		{Kind: Array, Name: "big2",
			Elem:  &Type{Kind: Array, Elem: prim("i64", 8, 8), Count: 1 << 40},
			Count: 1 << 40},
		// Struct offset addition overflows.
		{Kind: Struct, Name: "big3", Fields: []Field{
			{Name: "a", Type: &Type{Kind: Array, Elem: prim("u8", 1, 1), Count: math.MaxInt64 - 3}},
			{Name: "b", Type: prim("i64", 8, 8)},
		}},
	}
	for _, typ := range cases {
		if _, err := Compute(typ); err == nil || !strings.Contains(err.Error(), "overflow") {
			t.Errorf("%s: want overflow error, got %v", typ.Name, err)
		}
	}
}

func TestInvalidInputs(t *testing.T) {
	if _, err := Compute(nil); err == nil {
		t.Error("nil type: want error")
	}
	if _, err := Compute(prim("bad", -1, 4)); err == nil {
		t.Error("negative size: want error")
	}
	if _, err := Compute(prim("bad", 4, 3)); err == nil {
		t.Error("non-power-of-two align: want error")
	}
	if _, err := Compute(&Type{Kind: Array, Elem: prim("u8", 1, 1), Count: -1}); err == nil {
		t.Error("negative count: want error")
	}
	s := &Type{Kind: Struct, Name: "S", Fields: []Field{{Name: "x"}}}
	if _, err := Compute(s); err == nil {
		t.Error("nil field type: want error")
	}
}

func TestSharedSubtypesMemoized(t *testing.T) {
	shared := &Type{Kind: Struct, Name: "Shared", Fields: []Field{
		{Name: "x", Type: prim("i16", 2, 2)},
	}}
	s := &Type{Kind: Struct, Name: "Twice", Fields: []Field{
		{Name: "first", Type: shared},
		{Name: "second", Type: shared},
	}}
	// Same *Type used twice is reuse, not recursion.
	check(t, s, 4, 2, 0, 2)
}

func TestLayoutString(t *testing.T) {
	s := &Type{Kind: Struct, Name: "S", Fields: []Field{
		{Name: "a", Type: prim("i8", 1, 1)},
		{Name: "b", Type: prim("i64", 8, 8)},
	}}
	l, err := Compute(s)
	if err != nil {
		t.Fatal(err)
	}
	got := l.String()
	for _, want := range []string{"size=16 align=8", "+0 a", "+8 b"} {
		if !strings.Contains(got, want) {
			t.Errorf("String() missing %q:\n%s", want, got)
		}
	}
	fmt.Println("example output:\n" + got)
}
