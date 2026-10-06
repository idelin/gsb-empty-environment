package layout

import (
	"math"
	"reflect"
	"strings"
	"testing"
)

var (
	i8  = &Type{Kind: Int, Name: "i8", Size: 1, Align: 1}
	i16 = &Type{Kind: Int, Name: "i16", Size: 2, Align: 2}
	i32 = &Type{Kind: Int, Name: "i32", Size: 4, Align: 4}
	i64 = &Type{Kind: Int, Name: "i64", Size: 8, Align: 8}
)

func TestStructOffsetsAndTailPadding(t *testing.T) {
	s := &Type{Kind: Struct, Name: "S", Fields: []Field{
		{"a", i8},
		{"b", i32},
		{"c", i8},
	}}
	l, err := LayoutOf(s)
	if err != nil {
		t.Fatal(err)
	}
	if l.Size != 12 || l.Align != 4 {
		t.Fatalf("size=%d align=%d, want 12/4", l.Size, l.Align)
	}
	want := []FieldLayout{{"a", 0, 1}, {"b", 4, 4}, {"c", 8, 1}}
	if !reflect.DeepEqual(l.Fields, want) {
		t.Fatalf("fields=%+v, want %+v", l.Fields, want)
	}
}

func TestStructAlignmentIsMaxOfFields(t *testing.T) {
	s := &Type{Kind: Struct, Name: "S", Fields: []Field{
		{"a", i8},
		{"b", i64},
	}}
	l, err := LayoutOf(s)
	if err != nil {
		t.Fatal(err)
	}
	if l.Align != 8 || l.Size != 16 {
		t.Fatalf("size=%d align=%d, want 16/8", l.Size, l.Align)
	}
	if l.Fields[1].Offset != 8 {
		t.Fatalf("b offset=%d, want 8", l.Fields[1].Offset)
	}
}

func TestUnionMembersShareOrigin(t *testing.T) {
	u := &Type{Kind: Union, Name: "U", Members: []Field{
		{"c", i8},
		{"w", i32},
		{"d", i64},
	}}
	l, err := LayoutOf(u)
	if err != nil {
		t.Fatal(err)
	}
	if l.Size != 8 || l.Align != 8 {
		t.Fatalf("size=%d align=%d, want 8/8", l.Size, l.Align)
	}
	for _, f := range l.Fields {
		if f.Offset != 0 {
			t.Fatalf("member %s offset=%d, want 0", f.Name, f.Offset)
		}
	}
}

func TestArrayStrideIncludesTailPadding(t *testing.T) {
	elem := &Type{Kind: Struct, Name: "E", Fields: []Field{
		{"x", i32},
		{"y", i8},
	}} // size 8 (4+1 padded to align 4)
	arr := &Type{Kind: Array, Name: "A", Elem: elem, Count: 3}
	l, err := LayoutOf(arr)
	if err != nil {
		t.Fatal(err)
	}
	if l.Size != 24 || l.Align != 4 {
		t.Fatalf("size=%d align=%d, want 24/4", l.Size, l.Align)
	}
}

func TestZeroLengthArrayOccupiesNothing(t *testing.T) {
	z := &Type{Kind: Array, Name: "Z", Elem: i32, Count: 0}
	l, err := LayoutOf(z)
	if err != nil {
		t.Fatal(err)
	}
	if l.Size != 0 || l.Align != 4 {
		t.Fatalf("size=%d align=%d, want 0/4", l.Size, l.Align)
	}
	m, err := Model(z)
	if err != nil {
		t.Fatal(err)
	}
	if len(m) != 0 {
		t.Fatalf("model has %d bytes, want 0", len(m))
	}
}

func TestZeroLengthArrayInsideStruct(t *testing.T) {
	// Zero-length array still forces alignment of the following field.
	s := &Type{Kind: Struct, Name: "S", Fields: []Field{
		{"a", i8},
		{"z", &Type{Kind: Array, Name: "Z", Elem: i32, Count: 0}},
		{"b", i8},
	}}
	l, err := LayoutOf(s)
	if err != nil {
		t.Fatal(err)
	}
	want := []FieldLayout{{"a", 0, 1}, {"z", 4, 0}, {"b", 4, 1}}
	if !reflect.DeepEqual(l.Fields, want) {
		t.Fatalf("fields=%+v, want %+v", l.Fields, want)
	}
	if l.Size != 8 || l.Align != 4 {
		t.Fatalf("size=%d align=%d, want 8/4", l.Size, l.Align)
	}
}

func TestModelNestedUnionByteByByte(t *testing.T) {
	u := &Type{Kind: Union, Name: "U", Members: []Field{
		{"x", i32},
		{"y", i8},
	}}
	s := &Type{Kind: Struct, Name: "S", Fields: []Field{
		{"a", i8},
		{"u", u},
		{"z", &Type{Kind: Array, Name: "Z", Elem: i8, Count: 0}},
		{"b", i8},
	}}
	l, err := LayoutOf(s)
	if err != nil {
		t.Fatal(err)
	}
	want := []FieldLayout{{"a", 0, 1}, {"u", 4, 4}, {"z", 8, 0}, {"b", 8, 1}}
	if !reflect.DeepEqual(l.Fields, want) {
		t.Fatalf("fields=%+v, want %+v", l.Fields, want)
	}
	if l.Size != 12 {
		t.Fatalf("size=%d, want 12", l.Size)
	}
	got, err := Model(s)
	if err != nil {
		t.Fatal(err)
	}
	wantModel := []string{
		"S.a", "", "", "",
		"S.u.x+S.u.y", "S.u.x", "S.u.x", "S.u.x",
		"S.b", "", "", "",
	}
	if !reflect.DeepEqual(got, wantModel) {
		t.Fatalf("model=%q, want %q", got, wantModel)
	}
}

func TestModelArrayOfStructs(t *testing.T) {
	elem := &Type{Kind: Struct, Name: "E", Fields: []Field{
		{"x", i16},
		{"y", i8},
	}} // size 4, align 2
	arr := &Type{Kind: Array, Name: "A", Elem: elem, Count: 2}
	got, err := Model(arr)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"A[0].x", "A[0].x", "A[0].y", "",
		"A[1].x", "A[1].x", "A[1].y", "",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("model=%q, want %q", got, want)
	}
}

func TestOverflowArrayCount(t *testing.T) {
	big := &Type{Kind: Int, Name: "big", Size: 1 << 32, Align: 1}
	arr := &Type{Kind: Array, Name: "A", Elem: big, Count: 1 << 32}
	if _, err := LayoutOf(arr); err != errOverflow {
		t.Fatalf("err=%v, want overflow", err)
	}
}

func TestOverflowStructOffset(t *testing.T) {
	big := &Type{Kind: Int, Name: "big", Size: math.MaxInt64 - 2, Align: 1}
	s := &Type{Kind: Struct, Name: "S", Fields: []Field{
		{"a", big},
		{"b", big},
	}}
	if _, err := LayoutOf(s); err != errOverflow {
		t.Fatalf("err=%v, want overflow", err)
	}
}

func TestOverflowTailPadding(t *testing.T) {
	big := &Type{Kind: Int, Name: "big", Size: math.MaxInt64 - 1, Align: 4}
	s := &Type{Kind: Struct, Name: "S", Fields: []Field{{"a", big}}}
	if _, err := LayoutOf(s); err != errOverflow {
		t.Fatalf("err=%v, want overflow", err)
	}
}

func TestRecursiveValueType(t *testing.T) {
	n := &Type{Kind: Struct, Name: "Node"}
	n.Fields = []Field{{"self", n}}
	_, err := LayoutOf(n)
	rec, ok := err.(*RecursiveError)
	if !ok {
		t.Fatalf("err=%v, want RecursiveError", err)
	}
	if rec.Name != "Node" {
		t.Fatalf("recursive type=%q, want Node", rec.Name)
	}
}

func TestIndirectRecursionThroughArrayAndUnion(t *testing.T) {
	u := &Type{Kind: Union, Name: "U"}
	arr := &Type{Kind: Array, Name: "A", Elem: u, Count: 2}
	u.Members = []Field{{"arr", arr}}
	if _, err := LayoutOf(u); err == nil {
		t.Fatal("want recursion error, got nil")
	}
	if _, err := Model(u); err == nil {
		t.Fatal("want recursion error from Model, got nil")
	}
}

func TestInvalidSpecs(t *testing.T) {
	cases := []*Type{
		{Kind: Int, Name: "bad", Size: 0, Align: 1},
		{Kind: Int, Name: "bad", Size: 4, Align: 3},
		{Kind: Int, Name: "bad", Size: 2, Align: 4},
		{Kind: Array, Name: "bad", Elem: i8, Count: -1},
	}
	for _, c := range cases {
		if _, err := LayoutOf(c); err == nil {
			t.Fatalf("%+v: want error, got nil", c)
		}
	}
}

func TestDescribe(t *testing.T) {
	u := &Type{Kind: Union, Name: "U", Members: []Field{
		{"x", i32},
		{"y", i8},
	}}
	s := &Type{Kind: Struct, Name: "S", Fields: []Field{
		{"a", i8},
		{"u", u},
	}}
	out, err := Describe(s)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"S: offset=0 size=8 align=4",
		"S.a: offset=0 size=1 align=1",
		"S.u: offset=4 size=4 align=4",
		"S.u.x: offset=4 size=4 align=4",
		"S.u.y: offset=4 size=1 align=1",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("Describe missing %q:\n%s", want, out)
		}
	}
}
