package nestedslice

import "testing"

func TestLeafPathPrunesUnreferencedFields(t *testing.T) {
	aVal := testBuffer("a.val", ValuesBuffer, 4)
	aVal.ValueWidth = 1
	bVal := testBuffer("b.val", ValuesBuffer, 4)
	bVal.ValueWidth = 2
	a := &Node{ID: "a", Name: "a", Kind: PrimitiveNode, Length: 4, ValueWidth: 1, Value: aVal}
	b := &Node{ID: "b", Name: "b", Kind: PrimitiveNode, Length: 4, ValueWidth: 2, Value: bVal}
	root := &Node{
		ID: "root", Name: "root", Kind: StructNode, Length: 4,
		NullRows: EmptySet(0, 4),
		Children: []*Node{a, b},
	}
	plan, err := BuildPlan(root, Query{
		TopRows: Interval(0, 4),
		Paths:   [][]string{{"b"}},
	}, Options{StartupCost: 1, PerByteCost: 1})
	if err != nil {
		t.Fatalf("BuildPlan: %v", err)
	}
	if len(plan.Root.Children) != 1 || plan.Root.Children[0].Name != "b" {
		t.Fatalf("path pruning failed: %+v", plan.Root.Children)
	}
	if _, exists := plan.RequiredRanges["a.val"]; exists {
		t.Fatalf("unreferenced leaf a.val was read")
	}
	if _, exists := plan.RequiredRanges["b.val"]; !exists {
		t.Fatalf("requested leaf b.val missing")
	}
}

func TestNestedListBoundaryRestoration(t *testing.T) {
	innerOff := testBuffer("inner.off", OffsetsBuffer, 5)
	leafVal := testBuffer("leaf.val", ValuesBuffer, 4)
	leafVal.ValueWidth = 1
	leaf := &Node{ID: "leaf", Name: "leaf", Kind: PrimitiveNode, Length: 4, ValueWidth: 1, Value: leafVal}
	inner := &Node{
		ID: "inner", Name: "inner", Kind: ListNode, Length: 4,
		NullRows:   EmptySet(0, 4),
		Offsets:    innerOff,
		OffsetData: []int64{0, 1, 1, 3, 4},
		Children:   []*Node{leaf},
	}
	outerOff := testBuffer("outer.off", OffsetsBuffer, 4)
	outer := &Node{
		ID: "outer", Name: "outer", Kind: ListNode, Length: 3,
		NullRows:   EmptySet(0, 3),
		Offsets:    outerOff,
		OffsetData: []int64{0, 2, 2, 4},
		Children:   []*Node{inner},
	}
	payloads := map[string][]byte{
		"inner.off": int64Payload(0, 1, 1, 3, 4),
		"outer.off": int64Payload(0, 2, 2, 4),
		"leaf.val":  primitivePayload(4, 1),
	}
	store := NewStore(payloads)
	rows := Interval(0, 3)
	plan, err := BuildPlan(outer, Query{TopRows: rows}, Options{StartupCost: 2, PerByteCost: 1})
	if err != nil {
		t.Fatalf("BuildPlan: %v", err)
	}
	oracle, err := DecodeOracle(outer, store, rows)
	if err != nil {
		t.Fatalf("oracle: %v", err)
	}
	replayed, err := DecodePlan(plan, outer, store)
	if err != nil {
		t.Fatalf("plan decode: %v", err)
	}
	if !AssertEquivalent(oracle, replayed) {
		t.Fatalf("nested list mismatch %v vs %v", oracle, replayed)
	}
	if len(replayed[1].ListItems) != 0 || replayed[1].ListItems == nil {
		t.Fatalf("empty outer list must decode to non-nil zero-length slice")
	}
}

func TestAffineReversedDictionaryStaysSymbolic(t *testing.T) {
	const count = (int64(1) << 30) + 1
	dictVal := testBuffer("dict.val", ValuesBuffer, count)
	dictVal.ValueWidth = 1
	dictionary := &Node{
		ID: "dict", Name: "dict", Kind: PrimitiveNode, Length: count,
		ValueWidth: 1, Value: dictVal,
	}
	index := testBuffer("dict.idx", DictIndexBuffer, count)
	root := &Node{
		ID: "root", Name: "root", Kind: DictionaryNode, Length: count,
		NullRows:    EmptySet(0, count),
		Index:       index,
		Dictionary:  dictionary,
		IndexAffine: true,
		IndexFirst:  count - 1,
		IndexStep:   -1,
	}
	rows := SetOf(AP{First: 0, Step: 2, Count: (count + 1) / 2})
	plan, err := BuildPlan(root, Query{TopRows: rows}, Options{})
	if err != nil {
		t.Fatalf("BuildPlan: %v", err)
	}
	dictRows := plan.Root.Dict.OrderedIndices
	if len(dictRows.APs) != 1 || dictRows.APs[0].Step != -2 {
		t.Fatalf("affine reversed dict rows = %+v, want single AP step -2", dictRows.APs)
	}
	if dictRows.APs[0].First != count-1 || dictRows.APs[0].Count != (count+1)/2 {
		t.Fatalf("affine reversed dict AP = %+v", dictRows.APs[0])
	}
}

func TestIllegalUnionLabelOnValidRowRejected(t *testing.T) {
	root, _ := buildUnionSchema()
	root.NullRows = EmptySet(0, root.Length)
	root.Validity = nil
	defer func() {
		if r := recover(); r == nil {
			t.Fatalf("illegal type id on a valid row must be rejected")
		}
	}()
	_, _ = BuildPlan(root, Query{TopRows: Interval(0, root.Length)}, Options{})
}

func TestValidateRejectsBrokenDescriptors(t *testing.T) {
	root, _ := buildSmallListSchema()
	if err := Validate(root); err != nil {
		t.Fatalf("valid schema rejected: %v", err)
	}
	root.Offsets = nil
	if err := Validate(root); err == nil {
		t.Fatalf("list without offsets must fail validation")
	}
}

func TestReadCostWeightSwitch(t *testing.T) {
	required := []ByteRange{{Start: 0, End: 2}, {Start: 10, End: 12}}
	mergedHigh := optimizeSegments(required, nil, costWeights{startup: 100, perByte: 1})
	if len(mergedHigh) != 1 || mergedHigh[0].ByteRange != (ByteRange{Start: 0, End: 12}) {
		t.Fatalf("high startup should merge, got %+v", mergedHigh)
	}
	splitLow := optimizeSegments(required, nil, costWeights{startup: 1, perByte: 10})
	want := []ByteRange{{Start: 0, End: 2}, {Start: 10, End: 12}}
	got := make([]ByteRange, len(splitLow))
	for i, seg := range splitLow {
		got[i] = seg.ByteRange
	}
	if !rangesEqual(got, want) {
		t.Fatalf("low startup should split, got %v", got)
	}
}
