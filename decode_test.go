package graphdec

import (
	"bytes"
	"encoding/binary"
	"strings"
	"testing"
)

func mustMarshal(t *testing.T, root *Value) []byte {
	t.Helper()
	data, err := Marshal(root)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	return data
}

func mustFail(t *testing.T, dec *Decoder, data []byte) {
	t.Helper()
	v, err := dec.Decode(data)
	if err == nil {
		t.Fatalf("expected error, got decoded value %+v", v)
	}
	if v != nil {
		t.Fatalf("error path leaked partial object: %+v", v)
	}
}

// chain builds a linked list of n list nodes ending in an int leaf and
// returns the root. Its condensation depth is n+1.
func chain(n int) *Value {
	root := &Value{Kind: KindInt, Int: 0}
	for i := 0; i < n; i++ {
		root = &Value{Kind: KindList, List: []*Value{root}}
	}
	return root
}

func TestRoundTrip(t *testing.T) {
	shared := &Value{Kind: KindString, Str: "shared"}
	back := &Value{Kind: KindList} // cyclic: points at root
	root := &Value{Kind: KindMap, Map: []MapEntry{
		{Key: &Value{Kind: KindString, Str: "name"}, Value: &Value{Kind: KindString, Str: "graph"}},
		{Key: &Value{Kind: KindString, Str: "n"}, Value: &Value{Kind: KindInt, Int: -42}},
		{Key: &Value{Kind: KindString, Str: "ok"}, Value: &Value{Kind: KindBool, Bool: true}},
		{Key: &Value{Kind: KindString, Str: "nil"}, Value: &Value{Kind: KindNull}},
		{Key: &Value{Kind: KindString, Str: "raw"}, Value: &Value{Kind: KindBytes, Bytes: []byte{1, 2, 3}}},
		{Key: &Value{Kind: KindString, Str: "a"}, Value: shared},
		{Key: &Value{Kind: KindString, Str: "b"}, Value: shared},
		{Key: &Value{Kind: KindString, Str: "cycle"}, Value: back},
	}}
	back.List = []*Value{root}

	v, err := Decode(mustMarshal(t, root))
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if v.Kind != KindMap || len(v.Map) != 8 {
		t.Fatalf("root: got kind=%v entries=%d", v.Kind, len(v.Map))
	}
	get := func(key string) *Value {
		for _, e := range v.Map {
			if e.Key.Str == key {
				return e.Value
			}
		}
		return nil
	}
	if got := get("n"); got.Kind != KindInt || got.Int != -42 {
		t.Fatalf("n: %+v", got)
	}
	if got := get("ok"); got.Kind != KindBool || !got.Bool {
		t.Fatalf("ok: %+v", got)
	}
	if got := get("raw"); !bytes.Equal(got.Bytes, []byte{1, 2, 3}) {
		t.Fatalf("raw: %+v", got)
	}
	if get("a") != get("b") {
		t.Fatal("shared node was duplicated")
	}
	if cyc := get("cycle"); cyc.Kind != KindList || len(cyc.List) != 1 || cyc.List[0] != v {
		t.Fatal("cycle back-reference not preserved")
	}
}

func TestDepthLimitOnCondensation(t *testing.T) {
	data := mustMarshal(t, chain(10)) // depth 11
	dec := &Decoder{Limits: Limits{MaxDepth: 11}}
	if _, err := dec.Decode(data); err != nil {
		t.Fatalf("depth 11 within budget 11: %v", err)
	}
	dec.Limits.MaxDepth = 10
	mustFail(t, dec, data)
}

func TestCycleDoesNotInflateDepth(t *testing.T) {
	// a <-> b cycle, then a tail: condensation is [a,b] -> tail, depth 2.
	a := &Value{Kind: KindList}
	b := &Value{Kind: KindList}
	tail := &Value{Kind: KindInt, Int: 7}
	a.List = []*Value{b}
	b.List = []*Value{a, tail}

	data := mustMarshal(t, a)
	dec := &Decoder{Limits: Limits{MaxDepth: 2}}
	v, err := dec.Decode(data)
	if err != nil {
		t.Fatalf("cycle should not inflate depth: %v", err)
	}
	if v.List[0].List[0] != v {
		t.Fatal("cycle not preserved")
	}
	dec.Limits.MaxDepth = 1
	mustFail(t, dec, data)
}

func TestCycleNodeQuota(t *testing.T) {
	a := &Value{Kind: KindList}
	b := &Value{Kind: KindList}
	a.List = []*Value{b}
	b.List = []*Value{a}
	data := mustMarshal(t, a)

	dec := &Decoder{Limits: Limits{MaxCycleNodes: 2}}
	if _, err := dec.Decode(data); err != nil {
		t.Fatalf("2 cycle nodes within budget 2: %v", err)
	}
	dec.Limits.MaxCycleNodes = 1
	mustFail(t, dec, data)
}

func TestSelfLoopCountsAsCycle(t *testing.T) {
	v := &Value{Kind: KindList}
	v.List = []*Value{v}
	data := mustMarshal(t, v)

	dec := &Decoder{Limits: Limits{MaxCycleNodes: 1}}
	if _, err := dec.Decode(data); err != nil {
		t.Fatalf("self loop within budget 1: %v", err)
	}
	// White-box: with a literal zero cycle budget the self-loop must be
	// rejected by the analyzer (zero Limits fields mean "default" at
	// the Decoder level, so analyze is exercised directly).
	nodes := []*node{{kind: KindList, refs: []uint64{0}}}
	if _, err := analyze(nodes, []uint64{0}, 0, Limits{MaxDepth: 10}); err == nil {
		t.Fatal("self loop must count against the cycle-node budget")
	}
}

func TestNodeLimitCountsSharedOnce(t *testing.T) {
	shared := &Value{Kind: KindInt, Int: 1}
	root := &Value{Kind: KindList, List: []*Value{shared, shared, shared}}
	data := mustMarshal(t, root)

	// 2 distinct nodes, 3 references.
	dec := &Decoder{Limits: Limits{MaxNodes: 2, MaxRefs: 3}}
	v, err := dec.Decode(data)
	if err != nil {
		t.Fatalf("shared node must count once: %v", err)
	}
	if v.List[0] != v.List[1] || v.List[1] != v.List[2] {
		t.Fatal("shared identity lost")
	}
	dec.Limits.MaxNodes = 1
	mustFail(t, dec, data)
}

func TestRefLimitOnSmallInput(t *testing.T) {
	shared := &Value{Kind: KindNull}
	root := &Value{Kind: KindList, List: make([]*Value, 64)}
	for i := range root.List {
		root.List[i] = shared
	}
	data := mustMarshal(t, root)
	if len(data) > 200 {
		t.Fatalf("test input unexpectedly large: %d bytes", len(data))
	}
	dec := &Decoder{Limits: Limits{MaxRefs: 64}}
	if _, err := dec.Decode(data); err != nil {
		t.Fatalf("64 refs within budget 64: %v", err)
	}
	dec.Limits.MaxRefs = 63
	mustFail(t, dec, data)
}

func TestByteLimit(t *testing.T) {
	data := mustMarshal(t, &Value{Kind: KindString, Str: strings.Repeat("x", 100)})
	dec := &Decoder{Limits: Limits{MaxBytes: len(data) - 1}}
	mustFail(t, dec, data)
	dec.Limits.MaxBytes = len(data)
	if _, err := dec.Decode(data); err != nil {
		t.Fatalf("exact-size input within budget: %v", err)
	}
}

func TestHugeFanOutRejected(t *testing.T) {
	leaf := &Value{Kind: KindNull}
	root := &Value{Kind: KindList, List: make([]*Value, 5000)}
	for i := range root.List {
		root.List[i] = leaf
	}
	data := mustMarshal(t, root)
	dec := &Decoder{Limits: Limits{MaxRefs: 1000}}
	mustFail(t, dec, data)
}

func TestUnknownReferenceRejected(t *testing.T) {
	// Hand-crafted: root node 0 is a list referencing undeclared id 99.
	var buf bytes.Buffer
	buf.Write(magic[:])
	buf.Write(binary.AppendUvarint(nil, 0)) // root id
	buf.Write(binary.AppendUvarint(nil, 1)) // node count
	buf.Write(binary.AppendUvarint(nil, 0)) // node id
	buf.WriteByte(byte(KindList))
	buf.Write(binary.AppendUvarint(nil, 1))  // 1 child
	buf.Write(binary.AppendUvarint(nil, 99)) // dangling id
	mustFail(t, &Decoder{}, buf.Bytes())
}

func TestDuplicateIDRejected(t *testing.T) {
	var buf bytes.Buffer
	buf.Write(magic[:])
	buf.Write(binary.AppendUvarint(nil, 0))
	buf.Write(binary.AppendUvarint(nil, 2))
	for i := 0; i < 2; i++ {
		buf.Write(binary.AppendUvarint(nil, 0)) // same id twice
		buf.WriteByte(byte(KindNull))
	}
	mustFail(t, &Decoder{}, buf.Bytes())
}

func TestUnreachableNodeRejected(t *testing.T) {
	var buf bytes.Buffer
	buf.Write(magic[:])
	buf.Write(binary.AppendUvarint(nil, 0)) // root id
	buf.Write(binary.AppendUvarint(nil, 2)) // node count
	buf.Write(binary.AppendUvarint(nil, 0))
	buf.WriteByte(byte(KindNull))
	buf.Write(binary.AppendUvarint(nil, 1)) // unreachable from root
	buf.WriteByte(byte(KindNull))
	mustFail(t, &Decoder{}, buf.Bytes())
}

func TestTruncatedInputsRejected(t *testing.T) {
	data := mustMarshal(t, &Value{Kind: KindMap, Map: []MapEntry{
		{Key: &Value{Kind: KindString, Str: "k"}, Value: &Value{Kind: KindString, Str: "v"}},
	}})
	for i := 0; i < len(data); i++ {
		mustFail(t, &Decoder{}, data[:i])
	}
}

func TestTrailingGarbageRejected(t *testing.T) {
	data := mustMarshal(t, &Value{Kind: KindNull})
	mustFail(t, &Decoder{}, append(data, 0x00))
}

func TestBadMagicRejected(t *testing.T) {
	data := mustMarshal(t, &Value{Kind: KindNull})
	data[0] = 'X'
	mustFail(t, &Decoder{}, data)
}

func TestUnknownKindRejected(t *testing.T) {
	var buf bytes.Buffer
	buf.Write(magic[:])
	buf.Write(binary.AppendUvarint(nil, 0))
	buf.Write(binary.AppendUvarint(nil, 1))
	buf.Write(binary.AppendUvarint(nil, 0))
	buf.WriteByte(200) // not a valid kind
	mustFail(t, &Decoder{}, buf.Bytes())
}

func TestDeclaredCountsValidatedBeforeAllocation(t *testing.T) {
	// Tiny input claiming a huge node count: rejected from the header,
	// before any proportional allocation.
	var buf bytes.Buffer
	buf.Write(magic[:])
	buf.Write(binary.AppendUvarint(nil, 0))
	buf.Write(binary.AppendUvarint(nil, 1<<40))
	mustFail(t, &Decoder{}, buf.Bytes())

	// A list node claiming more children than bytes remain.
	buf.Reset()
	buf.Write(magic[:])
	buf.Write(binary.AppendUvarint(nil, 0))
	buf.Write(binary.AppendUvarint(nil, 1))
	buf.Write(binary.AppendUvarint(nil, 0))
	buf.WriteByte(byte(KindList))
	buf.Write(binary.AppendUvarint(nil, 1<<32))
	mustFail(t, &Decoder{}, buf.Bytes())

	// A string node claiming more bytes than remain.
	buf.Reset()
	buf.Write(magic[:])
	buf.Write(binary.AppendUvarint(nil, 0))
	buf.Write(binary.AppendUvarint(nil, 1))
	buf.Write(binary.AppendUvarint(nil, 0))
	buf.WriteByte(byte(KindString))
	buf.Write(binary.AppendUvarint(nil, 1<<32))
	mustFail(t, &Decoder{}, buf.Bytes())
}

func TestPendingForwardRefsBounded(t *testing.T) {
	// Root references two not-yet-declared ids; with a pending budget of
	// 1 the staged forward references exceed the quota.
	var buf bytes.Buffer
	buf.Write(magic[:])
	buf.Write(binary.AppendUvarint(nil, 0)) // root id
	buf.Write(binary.AppendUvarint(nil, 3)) // node count
	buf.Write(binary.AppendUvarint(nil, 0))
	buf.WriteByte(byte(KindList))
	buf.Write(binary.AppendUvarint(nil, 2)) // 2 children
	buf.Write(binary.AppendUvarint(nil, 1)) // forward ref
	buf.Write(binary.AppendUvarint(nil, 2)) // forward ref
	buf.Write(binary.AppendUvarint(nil, 1))
	buf.WriteByte(byte(KindNull))
	buf.Write(binary.AppendUvarint(nil, 2))
	buf.WriteByte(byte(KindNull))
	data := buf.Bytes()

	if _, err := (&Decoder{}).Decode(data); err != nil {
		t.Fatalf("forward refs should resolve: %v", err)
	}
	dec := &Decoder{Limits: Limits{MaxPendingRefs: 1}}
	mustFail(t, dec, data)
}

func TestUnresolvedForwardRefsRejected(t *testing.T) {
	var buf bytes.Buffer
	buf.Write(magic[:])
	buf.Write(binary.AppendUvarint(nil, 0))
	buf.Write(binary.AppendUvarint(nil, 1))
	buf.Write(binary.AppendUvarint(nil, 0))
	buf.WriteByte(byte(KindList))
	buf.Write(binary.AppendUvarint(nil, 1))
	buf.Write(binary.AppendUvarint(nil, 5)) // never declared
	mustFail(t, &Decoder{}, buf.Bytes())
}

func TestLimitsRecovery(t *testing.T) {
	big := mustMarshal(t, chain(50))
	small := mustMarshal(t, &Value{Kind: KindInt, Int: 1})
	dec := &Decoder{Limits: Limits{MaxDepth: 4}}

	mustFail(t, dec, big) // exceeds depth budget
	mustFail(t, dec, big) // deterministic, no state leaked
	if _, err := dec.Decode(small); err != nil {
		t.Fatalf("decoder must stay usable after limit failure: %v", err)
	}
}

func TestDeepChainDoesNotOverflowStack(t *testing.T) {
	// 200k-deep chain: analysis must be iterative.
	data := mustMarshal(t, chain(200000))
	dec := &Decoder{Limits: Limits{MaxDepth: 200001, MaxNodes: 1 << 21, MaxRefs: 1 << 21, MaxBytes: 1 << 22}}
	if _, err := dec.Decode(data); err != nil {
		t.Fatalf("deep chain: %v", err)
	}
}

func TestMalformedVarintRejected(t *testing.T) {
	data := []byte{'O', 'G', 'D', '1', 0x80} // unterminated varint
	mustFail(t, &Decoder{}, data)
}
