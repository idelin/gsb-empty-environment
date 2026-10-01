package graphser

import (
	"bytes"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

type Node struct {
	Name string
	Next *Node
}

type Bag struct {
	A    *Node
	B    *Node
	List []*Node
	M    map[string]*Node
}

type M map[string]M

func roundTrip(t *testing.T, in any, out any) []byte {
	t.Helper()
	data, err := Marshal(in)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if err := Unmarshal(data, out); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	return data
}

func TestSharedIdentityPreserved(t *testing.T) {
	shared := &Node{Name: "shared"}
	in := &Bag{
		A:    shared,
		B:    shared,
		List: []*Node{shared, shared},
		M:    map[string]*Node{"x": shared},
	}
	var out *Bag
	roundTrip(t, in, &out)
	if out.A == nil || out.A.Name != "shared" {
		t.Fatalf("bad decode: %+v", out.A)
	}
	if out.A != out.B || out.A != out.List[0] || out.A != out.List[1] || out.A != out.M["x"] {
		t.Fatal("shared object lost its identity: references decode to distinct objects")
	}
}

func TestSelfCyclePreserved(t *testing.T) {
	n := &Node{Name: "loop"}
	n.Next = n
	var out *Node
	roundTrip(t, n, &out)
	if out == nil || out.Name != "loop" {
		t.Fatalf("bad decode: %+v", out)
	}
	if out.Next != out {
		t.Fatal("self-cycle not preserved: out.Next != out")
	}
}

func TestTwoNodeCycle(t *testing.T) {
	a := &Node{Name: "a"}
	b := &Node{Name: "b"}
	a.Next = b
	b.Next = a
	var out *Node
	roundTrip(t, a, &out)
	if out.Next == nil || out.Next.Name != "b" || out.Next.Next != out {
		t.Fatal("two-node cycle not preserved")
	}
}

func TestMapIdentityAndSelfCycle(t *testing.T) {
	m := M{}
	m["self"] = m
	m["child"] = M{}
	var out M
	roundTrip(t, m, &out)
	if reflect.ValueOf(out["self"]).Pointer() != reflect.ValueOf(out).Pointer() {
		t.Fatal("map self-cycle not preserved")
	}
	if out["child"] == nil || len(out["child"]) != 0 {
		t.Fatal("child map corrupted")
	}
}

func TestSharedMapIdentity(t *testing.T) {
	type Holder struct {
		X, Y map[string]int
	}
	shared := map[string]int{"n": 1}
	in := &Holder{X: shared, Y: shared}
	var out *Holder
	roundTrip(t, in, &out)
	if reflect.ValueOf(out.X).Pointer() != reflect.ValueOf(out.Y).Pointer() {
		t.Fatal("shared map lost its identity")
	}
}

func TestValueEqualityVsIdentity(t *testing.T) {
	a := &Node{Name: "x"}
	b := &Node{Name: "x"} // equal by value, distinct objects
	in := &Bag{A: a, B: b}
	var out *Bag
	roundTrip(t, in, &out)
	if out.A == out.B {
		t.Fatal("distinct objects collapsed into one identity")
	}
	if !reflect.DeepEqual(*out.A, *out.B) {
		t.Fatal("equal values no longer equal after round trip")
	}
}

func TestStableIDsAndDeterministicBytes(t *testing.T) {
	build := func() *Bag {
		shared := &Node{Name: "s"}
		return &Bag{A: shared, B: shared, M: map[string]*Node{"k2": shared, "k1": shared}}
	}
	d1, err := Marshal(build())
	if err != nil {
		t.Fatal(err)
	}
	d2, err := Marshal(build())
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(d1, d2) {
		t.Fatal("same graph encoded to different bytes; IDs not stable")
	}
	var doc Document
	if err := json.Unmarshal(d1, &doc); err != nil {
		t.Fatal(err)
	}
	if len(doc.Objects) != 3 {
		t.Fatalf("expected 3 objects (Bag, Node, map), got %+v", doc.Objects)
	}
	for i, obj := range doc.Objects {
		if obj.ID != uint64(i+1) {
			t.Fatalf("object IDs not stable/sequential: %+v", doc.Objects)
		}
	}
}

func TestRoundTripValues(t *testing.T) {
	type All struct {
		B     bool
		I     int
		I8    int8
		I64   int64
		U     uint
		U16   uint16
		F32   float32
		F64   float64
		S     string
		Sl    []int
		NilSl []int
		Empty []int
		Arr   [3]string
		P     *int
		NilP  *int
		M     map[string]int
		NilM  map[string]int
	}
	seven := 7
	in := &All{
		B: true, I: -5, I8: -8, I64: 1 << 60, U: 9, U16: 16,
		F32: 1.5, F64: -2.25, S: "héllo",
		Sl: []int{1, 2, 3}, Empty: []int{}, Arr: [3]string{"a", "b", "c"},
		P: &seven, M: map[string]int{"one": 1},
	}
	var out *All
	roundTrip(t, in, &out)
	if !reflect.DeepEqual(in, out) {
		t.Fatalf("round trip mismatch:\nin:  %+v\nout: %+v", in, out)
	}
	if out.NilSl != nil || out.NilM != nil || out.NilP != nil {
		t.Fatal("nil values did not round-trip as nil")
	}
	if out.Empty == nil || len(out.Empty) != 0 {
		t.Fatal("empty slice did not round-trip as empty non-nil")
	}
}

func TestDanglingRefRejected(t *testing.T) {
	doc := `{"version":1,"root":{"kind":"ref","ref":9},"objects":[]}`
	var out Node
	if err := Unmarshal([]byte(doc), &out); err == nil || !strings.Contains(err.Error(), "dangling") {
		t.Fatalf("expected dangling reference error, got %v", err)
	}
}

func TestDuplicateIDRejected(t *testing.T) {
	doc := `{"version":1,"root":{"kind":"ref","ref":1},"objects":[
		{"id":1,"type":"graphser.Node","value":{"kind":"struct","type":"graphser.Node"}},
		{"id":1,"type":"graphser.Node","value":{"kind":"struct","type":"graphser.Node"}}]}`
	var out *Node
	if err := Unmarshal([]byte(doc), &out); err == nil || !strings.Contains(err.Error(), "duplicate") {
		t.Fatalf("expected duplicate definition error, got %v", err)
	}
}

func TestConflictingTypesRejected(t *testing.T) {
	// Object 1 is referenced both as *Node and as *Other.
	doc := `{"version":1,"root":{"kind":"struct","type":"graphser.duo","fields":[
		{"name":"N","value":{"kind":"ref","ref":1}},
		{"name":"O","value":{"kind":"ref","ref":1}}]},
		"objects":[{"id":1,"type":"graphser.Node","value":{"kind":"struct","type":"graphser.Node"}}]}`
	var out duo
	if err := Unmarshal([]byte(doc), &out); err == nil ||
		!(strings.Contains(err.Error(), "conflicting") || strings.Contains(err.Error(), "want")) {
		t.Fatalf("expected type conflict error, got %v", err)
	}
}

type duo struct {
	N *Node
	O *Other
}

type Other struct {
	X int
}

func TestUnreferencedObjectRejected(t *testing.T) {
	doc := `{"version":1,"root":{"kind":"ref","ref":1},"objects":[
		{"id":1,"type":"graphser.Node","value":{"kind":"struct","type":"graphser.Node"}},
		{"id":2,"type":"graphser.Node","value":{"kind":"struct","type":"graphser.Node"}}]}`
	var out *Node
	if err := Unmarshal([]byte(doc), &out); err == nil || !strings.Contains(err.Error(), "never referenced") {
		t.Fatalf("expected unreferenced object error, got %v", err)
	}
}

func TestDuplicateFieldRejected(t *testing.T) {
	doc := `{"version":1,"root":{"kind":"struct","type":"graphser.Node","fields":[
		{"name":"Name","value":{"kind":"string","str":"a"}},
		{"name":"Name","value":{"kind":"string","str":"b"}}]},"objects":[]}`
	var out Node
	if err := Unmarshal([]byte(doc), &out); err == nil || !strings.Contains(err.Error(), "duplicate field") {
		t.Fatalf("expected duplicate field error, got %v", err)
	}
}

func TestLengthValidatedBeforeAllocation(t *testing.T) {
	defer func(lo, ll, ld int) { maxObjects, maxLen, maxDepth = lo, ll, ld }(maxObjects, maxLen, maxDepth)
	maxLen = 2
	big := strings.Repeat(`{"kind":"int","int":1},`, 3)
	doc := fmt.Sprintf(`{"version":1,"root":{"kind":"slice","type":"[]int","elems":[%s{"kind":"int","int":1}]},"objects":[]}`, big)
	var out []int
	if err := Unmarshal([]byte(doc), &out); err == nil || !strings.Contains(err.Error(), "exceeds limit") {
		t.Fatalf("expected length limit error, got %v", err)
	}
	maxLen = 1 << 22
	maxObjects = 1
	doc = `{"version":1,"root":{"kind":"ref","ref":1},"objects":[
		{"id":1,"type":"graphser.Node","value":{"kind":"struct","type":"graphser.Node"}},
		{"id":2,"type":"graphser.Node","value":{"kind":"struct","type":"graphser.Node"}}]}`
	var n *Node
	if err := Unmarshal([]byte(doc), &n); err == nil || !strings.Contains(err.Error(), "too many objects") {
		t.Fatalf("expected object count limit error, got %v", err)
	}
}

func TestDepthLimit(t *testing.T) {
	defer func(d int) { maxDepth = d }(maxDepth)
	maxDepth = 5
	deep := &Node{}
	cur := deep
	for i := 0; i < 20; i++ {
		cur.Next = &Node{}
		cur = cur.Next
	}
	data, err := Marshal(deep)
	if err == nil || !strings.Contains(err.Error(), "max depth") {
		t.Fatalf("expected encode depth error, got %v", err)
	}
	_ = data
}

func TestOverflowRejected(t *testing.T) {
	doc := `{"version":1,"root":{"kind":"int","int":300},"objects":[]}`
	var out int8
	if err := Unmarshal([]byte(doc), &out); err == nil || !strings.Contains(err.Error(), "overflows") {
		t.Fatalf("expected overflow error, got %v", err)
	}
}

func TestTypeMismatchRejected(t *testing.T) {
	// Document claims the pointee is Other, output expects Node.
	doc := `{"version":1,"root":{"kind":"ref","ref":1},"objects":[
		{"id":1,"type":"graphser.Other","value":{"kind":"struct","type":"graphser.Other","fields":[
			{"name":"X","value":{"kind":"int","int":1}}]}}]}`
	var out *Node
	if err := Unmarshal([]byte(doc), &out); err == nil || !strings.Contains(err.Error(), "want") {
		t.Fatalf("expected type mismatch error, got %v", err)
	}
}

func TestUnsupportedTypesRejected(t *testing.T) {
	if _, err := Marshal(make(chan int)); err == nil {
		t.Fatal("expected error for chan")
	}
	if _, err := Marshal(func() {}); err == nil {
		t.Fatal("expected error for func")
	}
	type HasIface struct {
		V any
	}
	if _, err := Marshal(&HasIface{V: 1}); err == nil {
		t.Fatal("expected error for interface field")
	}
	if _, err := Marshal(map[*Node]int{}); err == nil {
		t.Fatal("expected error for non-scalar map key")
	}
}

func TestTrailingDataRejected(t *testing.T) {
	data, err := Marshal(&Node{Name: "x"})
	if err != nil {
		t.Fatal(err)
	}
	var out Node
	if err := Unmarshal(append(data, 0), &out); err == nil {
		t.Fatal("expected trailing data error")
	}
}

func TestWrongVersionRejected(t *testing.T) {
	doc := `{"version":99,"root":{"kind":"null"},"objects":[]}`
	var out *Node
	if err := Unmarshal([]byte(doc), &out); err == nil || !strings.Contains(err.Error(), "version") {
		t.Fatalf("expected version error, got %v", err)
	}
}

func TestPointerToMap(t *testing.T) {
	type Holder struct {
		PM *map[string]int
	}
	m := map[string]int{"a": 1}
	in := &Holder{PM: &m}
	var out *Holder
	roundTrip(t, in, &out)
	if out.PM == nil || (*out.PM)["a"] != 1 {
		t.Fatalf("pointer to map not preserved: %+v", out)
	}
}
