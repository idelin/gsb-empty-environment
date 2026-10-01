package graphser

import (
	"bytes"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

func roundTrip(t *testing.T, root any) any {
	t.Helper()
	data, err := Marshal(root)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	back, err := Unmarshal(data)
	if err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	return back
}

func TestMarshalGolden(t *testing.T) {
	obj := NewObject()
	obj.Fields["name"] = "x"
	obj.Fields["self"] = obj
	data, err := Marshal([]any{obj, obj})
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	want := `{"version":1,"count":1,"objects":[{"id":1,"fields":{"name":"x","self":{"ref":1}}}],"root":{"list":[{"ref":1},{"ref":1}]}}`
	if string(data) != want {
		t.Fatalf("golden mismatch:\n got: %s\nwant: %s", data, want)
	}
}

func TestRoundTripScalars(t *testing.T) {
	root := []any{nil, true, false, "hello", 3.5, -2.0, []any{1.0, []any{"nested"}}}
	back := roundTrip(t, root)
	if !reflect.DeepEqual(root, back) {
		t.Fatalf("round trip mismatch:\n got: %#v\nwant: %#v", back, root)
	}
}

func TestSharedIdentityPreserved(t *testing.T) {
	shared := NewObject()
	shared.Fields["n"] = 1.0
	back := roundTrip(t, []any{shared, shared}).([]any)
	a, b := back[0].(*Object), back[1].(*Object)
	if a != b {
		t.Fatal("two references to the same object decoded as distinct objects")
	}
	a.Fields["n"] = 2.0
	if b.Fields["n"] != 2.0 {
		t.Fatal("mutation through one reference not visible through the other")
	}
}

func TestDistinctObjectsStayDistinct(t *testing.T) {
	mk := func() *Object {
		o := NewObject()
		o.Fields["k"] = "v"
		return o
	}
	a, b := mk(), mk() // value-equal but NOT the same object
	back := roundTrip(t, []any{a, b}).([]any)
	da, db := back[0].(*Object), back[1].(*Object)
	if da == db {
		t.Fatal("distinct objects collapsed into one identity")
	}
	if !reflect.DeepEqual(da.Fields, db.Fields) {
		t.Fatal("value-equal objects no longer equal after round trip")
	}
}

func TestSelfLoopPreserved(t *testing.T) {
	obj := NewObject()
	obj.Fields["self"] = obj
	back := roundTrip(t, obj).(*Object)
	if back.Fields["self"].(*Object) != back {
		t.Fatal("self-loop not preserved")
	}
}

func TestMutualCyclePreserved(t *testing.T) {
	a, b := NewObject(), NewObject()
	a.Fields["other"] = b
	b.Fields["other"] = a
	back := roundTrip(t, a).(*Object)
	other := back.Fields["other"].(*Object)
	if other.Fields["other"].(*Object) != back {
		t.Fatal("two-node cycle not preserved")
	}
}

func TestDeterministicOutput(t *testing.T) {
	mk := func() any {
		shared := NewObject()
		shared.Fields["z"] = 1.0
		shared.Fields["a"] = []any{shared, "s"}
		return []any{shared, shared}
	}
	d1, err := Marshal(mk())
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	d2, err := Marshal(mk())
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if !bytes.Equal(d1, d2) {
		t.Fatalf("non-deterministic output:\n%s\n%s", d1, d2)
	}
}

func TestDanglingReferenceRejected(t *testing.T) {
	doc := `{"version":1,"count":0,"objects":[],"root":{"ref":7}}`
	if _, err := Unmarshal([]byte(doc)); err == nil ||
		!strings.Contains(err.Error(), "dangling") {
		t.Fatalf("expected dangling reference error, got %v", err)
	}
}

func TestDuplicateIDRejected(t *testing.T) {
	doc := `{"version":1,"count":2,"objects":[{"id":1,"fields":{}},{"id":1,"fields":{}}],"root":{"ref":1}}`
	if _, err := Unmarshal([]byte(doc)); err == nil ||
		!strings.Contains(err.Error(), "duplicate") {
		t.Fatalf("expected duplicate id error, got %v", err)
	}
}

func TestCountMismatchRejected(t *testing.T) {
	doc := `{"version":1,"count":5,"objects":[],"root":null}`
	if _, err := Unmarshal([]byte(doc)); err == nil ||
		!strings.Contains(err.Error(), "count") {
		t.Fatalf("expected count mismatch error, got %v", err)
	}
}

func TestUnknownFieldRejected(t *testing.T) {
	doc := `{"version":1,"count":0,"objects":[],"root":null,"evil":"constructor"}`
	if _, err := Unmarshal([]byte(doc)); err == nil {
		t.Fatal("expected unknown field to be rejected")
	}
}

func TestTrailingDataRejected(t *testing.T) {
	doc := `{"version":1,"count":0,"objects":[],"root":null} {"version":1}`
	if _, err := Unmarshal([]byte(doc)); err == nil {
		t.Fatal("expected trailing data to be rejected")
	}
}

func TestUnsupportedTypeRejected(t *testing.T) {
	for _, v := range []any{
		struct{}{},
		make(chan int),
		map[string]any{"k": 1.0}, // raw maps are outside the domain
	} {
		if _, err := Marshal(v); err == nil {
			t.Fatalf("expected unsupported type error for %T", v)
		}
	}
}

func TestNonFiniteNumberRejected(t *testing.T) {
	for _, v := range []float64{nan(), inf()} {
		if _, err := Marshal(v); err == nil {
			t.Fatalf("expected non-finite number %v to be rejected", v)
		}
	}
}

func nan() float64 { return 0.0 / zero() }
func inf() float64 { return 1.0 / zero() }
func zero() float64 {
	return 0.0
}

func TestDepthLimitEnforced(t *testing.T) {
	nest := MaxDepth + 1
	doc := `{"version":1,"count":0,"objects":[],"root":` +
		strings.Repeat(`{"list":[`, nest) + `null` + strings.Repeat(`]}`, nest) + `}`
	if _, err := Unmarshal([]byte(doc)); err == nil ||
		!strings.Contains(err.Error(), "deeper") {
		t.Fatalf("expected depth limit error, got %v", err)
	}

	deep := any(nil)
	for i := 0; i < nest; i++ {
		deep = []any{deep}
	}
	if _, err := Marshal(deep); err == nil {
		t.Fatal("expected marshal depth limit error")
	}
}

func TestListLengthLimitEnforced(t *testing.T) {
	var sb strings.Builder
	sb.WriteString(`{"version":1,"count":0,"objects":[],"root":{"list":[`)
	for i := 0; i <= MaxListLen; i++ {
		if i > 0 {
			sb.WriteByte(',')
		}
		sb.WriteString("null")
	}
	sb.WriteString(`]}}`)
	if _, err := Unmarshal([]byte(sb.String())); err == nil ||
		!strings.Contains(err.Error(), "exceeds limit") {
		t.Fatalf("expected list length limit error, got %v", err)
	}
}

func TestValueEqual(t *testing.T) {
	mk := func() *Object {
		o := NewObject()
		o.Fields["x"] = []any{1.0, "s"}
		return o
	}
	a, b := mk(), mk()
	if !ValueEqual(a, b) {
		t.Fatal("structurally equal objects reported unequal")
	}
	b.Fields["x"] = []any{1.0, "t"}
	if ValueEqual(a, b) {
		t.Fatal("different objects reported equal")
	}
	if ValueEqual(a, "not an object") {
		t.Fatal("different types reported equal")
	}

	// Value equality ignores identity and handles cycles.
	loop := NewObject()
	loop.Fields["self"] = loop
	loop.Fields["n"] = 1.0
	back := roundTrip(t, loop)
	if !ValueEqual(loop, back) {
		t.Fatal("cyclic graph not value-equal to its round trip")
	}
	if back.(*Object) == loop {
		t.Fatal("decoded object shares identity with the original")
	}
}

func TestRoundTripIsValueEqual(t *testing.T) {
	shared := NewObject()
	shared.Fields["tags"] = []any{"a", "b"}
	loop := NewObject()
	loop.Fields["self"] = loop
	loop.Fields["peer"] = shared
	root := []any{shared, shared, loop, 3.25, "text", nil, true}
	back := roundTrip(t, root)
	if !ValueEqual(root, back) {
		t.Fatal("round trip not value-equal")
	}
}

func ExampleMarshal() {
	shared := NewObject()
	shared.Fields["name"] = "shared"
	loop := NewObject()
	loop.Fields["self"] = loop
	data, _ := Marshal([]any{shared, shared, loop})
	fmt.Println(string(data))
	// Output: {"version":1,"count":2,"objects":[{"id":1,"fields":{"name":"shared"}},{"id":2,"fields":{"self":{"ref":2}}}],"root":{"list":[{"ref":1},{"ref":1},{"ref":2}]}}
}
