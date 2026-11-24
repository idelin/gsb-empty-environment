package nestedslice

import (
	"encoding/binary"
	"testing"
)

func testBuffer(id string, kind BufferKind, length int64) *Buffer {
	return &Buffer{ID: id, Kind: kind, Length: length}
}

func int64Payload(values ...int64) []byte {
	data := make([]byte, 8*len(values))
	for i, v := range values {
		binary.LittleEndian.PutUint64(data[i*8:], uint64(v))
	}
	return data
}

func bitmapPayload(valid []bool) []byte {
	data := make([]byte, bitmapBytes(int64(len(valid))))
	for i, ok := range valid {
		if ok {
			data[i/8] |= 1 << uint(i%8)
		}
	}
	return data
}

func bytePayload(values ...byte) []byte { return append([]byte(nil), values...) }

func primitivePayload(n int64, width int64) []byte { return make([]byte, n*width) }

func assertNoForbiddenLeak(t *testing.T, plan *Plan) {
	t.Helper()
	for _, seg := range plan.Segments {
		for _, f := range plan.ForbiddenRanges[seg.BufferID] {
			if seg.Start < f.End && f.Start < seg.End {
				t.Fatalf("segment %v on %s leaks forbidden range %v", seg.ByteRange, seg.BufferID, f)
			}
		}
	}
}

func assertSegmentsCoverRows(t *testing.T, plan *Plan, bufferID string, ranges ...ByteRange) {
	t.Helper()
	got := plan.RequiredRanges[bufferID]
	if len(got) != len(ranges) {
		t.Fatalf("buffer %s required ranges = %v, want %v", bufferID, got, ranges)
	}
	for i := range got {
		if got[i] != ranges[i] {
			t.Fatalf("buffer %s range %d = %v, want %v", bufferID, i, got[i], ranges[i])
		}
	}
}

// buildSmallListSchema creates rows:
// [1,2], null (owns child interval [2,3)), [], [6], null (empty interval).
func buildSmallListSchema() (*Node, map[string][]byte) {
	validity := testBuffer("list.valid", ValidityBuffer, 5)
	offsets := testBuffer("list.off", OffsetsBuffer, 6)
	values := testBuffer("child.val", ValuesBuffer, 7)
	values.ValueWidth = 4
	child := &Node{
		ID: "child", Name: "child", Kind: PrimitiveNode, Length: 7,
		ValueWidth: 4, Value: values,
	}
	root := &Node{
		ID: "root", Name: "root", Kind: ListNode, Length: 5,
		Validity:   validity,
		NullRows:   SetOf(AP{First: 1, Step: 1, Count: 1}, AP{First: 4, Step: 1, Count: 1}),
		Offsets:    offsets,
		OffsetData: []int64{0, 2, 3, 3, 5, 7},
		Children:   []*Node{child},
	}
	payloads := map[string][]byte{
		"list.valid": bitmapPayload([]bool{true, false, true, true, false}),
		"list.off":   int64Payload(0, 2, 3, 3, 5, 7),
		"child.val":  primitivePayload(7, 4),
	}
	return root, payloads
}

func TestListNullShadowAndEmptyList(t *testing.T) {
	root, payloads := buildSmallListSchema()
	store := NewStore(payloads)
	rows := Interval(0, 5)

	oracle, err := DecodeOracle(root, store, rows)
	if err != nil {
		t.Fatalf("oracle decode: %v", err)
	}
	if !oracle[1].Null || oracle[2].Null || len(oracle[2].ListItems) != 0 {
		t.Fatalf("null/empty-list distinction lost: %+v", oracle)
	}
	if oracle[2].ListItems == nil {
		t.Fatalf("empty list must use a non-nil item slice")
	}

	plan, err := BuildPlan(root, Query{TopRows: rows}, Options{StartupCost: 10, PerByteCost: 1})
	if err != nil {
		t.Fatalf("BuildPlan: %v", err)
	}
	assertNoForbiddenLeak(t, plan)

	// The null list at row 1 physically owns child values [2,3); those child
	// bytes must be forbidden even though the read hull covers them.
	childForbidden := plan.ForbiddenRanges["child.val"]
	wantShadow := []ByteRange{{Start: 8, End: 12}, {Start: 20, End: 28}}
	if !rangesEqual(childForbidden, wantShadow) {
		t.Fatalf("child shadow = %v, want %v", childForbidden, wantShadow)
	}

	replayed, err := DecodePlan(plan, root, store)
	if err != nil {
		t.Fatalf("plan decode: %v", err)
	}
	if !AssertEquivalent(oracle, replayed) {
		t.Fatalf("replayed values differ\noracle=%v\nplan  =%v", oracle, replayed)
	}
}

func TestListSelectionChildSegments(t *testing.T) {
	root, payloads := buildSmallListSchema()
	store := NewStore(payloads)
	rows := SetOf(AP{First: 0, Step: 3, Count: 2}) // rows 0 and 3
	plan, err := BuildPlan(root, Query{TopRows: rows}, Options{StartupCost: 100, PerByteCost: 1})
	if err != nil {
		t.Fatalf("BuildPlan: %v", err)
	}
	assertNoForbiddenLeak(t, plan)

	oracle, err := DecodeOracle(root, store, rows)
	if err != nil {
		t.Fatalf("oracle: %v", err)
	}
	replayed, err := DecodePlan(plan, root, store)
	if err != nil {
		t.Fatalf("plan decode: %v", err)
	}
	if !AssertEquivalent(oracle, replayed) {
		t.Fatalf("mismatch %v vs %v", oracle, replayed)
	}

	// Rows 0 and 3 use child intervals [0,2) and [3,5); the middle gap
	// contains the shadowed null-owned interval.
	childRows := plan.Root.Children[0].Layer.Rows
	if len(childRows.APs) != 2 || childRows.APs[0] != (AP{First: 0, Step: 1, Count: 2}) ||
		childRows.APs[1] != (AP{First: 3, Step: 1, Count: 2}) {
		t.Fatalf("child row APs = %+v, want [0,2) and [3,5)", childRows.APs)
	}
	// Byte-level required ranges may merge adjacent hulls; the read optimizer
	// still splits at forbidden barriers.
	assertSegmentsCoverRows(t, plan, "child.val", ByteRange{Start: 0, End: 20})
}

func buildUnionSchema() (*Node, map[string][]byte) {
	validity := testBuffer("union.valid", ValidityBuffer, 4)
	typeIDs := testBuffer("union.tid", TypeIDBuffer, 4)
	unionOff := testBuffer("union.off", UnionOffsetBuffer, 4)
	textVal := testBuffer("text.val", ValuesBuffer, 2)
	textVal.ValueWidth = 1
	numVal := testBuffer("num.val", ValuesBuffer, 2)
	numVal.ValueWidth = 8
	text := &Node{ID: "text", Name: "text", Kind: PrimitiveNode, Length: 2, ValueWidth: 1, Value: textVal}
	num := &Node{ID: "num", Name: "num", Kind: PrimitiveNode, Length: 2, ValueWidth: 8, Value: numVal}
	root := &Node{
		ID: "root", Name: "root", Kind: DenseUnionNode, Length: 4,
		Validity: validity,
		NullRows: SetOf(AP{First: 2, Step: 1, Count: 1}),
		TypeIDs:  typeIDs, UnionOffsets: unionOff,
		TypeIDData:      []uint8{0, 1, 255, 0}, // 255 is illegal but row 2 is null
		UnionOffsetData: []int64{0, 0, 99, 1},
		Children:        []*Node{text, num},
	}
	payloads := map[string][]byte{
		"union.valid": bitmapPayload([]bool{true, true, false, true}),
		"union.tid":   bytePayload(0, 1, 255, 0),
		"union.off":   int64Payload(0, 0, 99, 1),
		"text.val":    primitivePayload(2, 1),
		"num.val":     primitivePayload(2, 8),
	}
	return root, payloads
}

func TestDenseUnionIllegalLabelShieldedByNull(t *testing.T) {
	root, payloads := buildUnionSchema()
	store := NewStore(payloads)
	rows := Interval(0, 4)

	oracle, err := DecodeOracle(root, store, rows)
	if err != nil {
		t.Fatalf("oracle must not inspect null union label: %v", err)
	}
	if !oracle[2].Null || oracle[0].UnionName != "text" || oracle[1].UnionName != "num" {
		t.Fatalf("unexpected oracle rows: %+v", oracle)
	}

	plan, err := BuildPlan(root, Query{TopRows: rows}, Options{StartupCost: 5, PerByteCost: 2})
	if err != nil {
		t.Fatalf("BuildPlan: %v", err)
	}
	assertNoForbiddenLeak(t, plan)
	tidForbidden := plan.ForbiddenRanges["union.tid"]
	if len(tidForbidden) != 1 || tidForbidden[0] != (ByteRange{Start: 2, End: 3}) {
		t.Fatalf("union type id shadow = %v, want [2,3)", tidForbidden)
	}

	replayed, err := DecodePlan(plan, root, store)
	if err != nil {
		t.Fatalf("plan decode read illegal label: %v", err)
	}
	if !AssertEquivalent(oracle, replayed) {
		t.Fatalf("union mismatch %v vs %v", oracle, replayed)
	}
}

func buildSharedDictionarySchema() (*Node, map[string][]byte) {
	dictVal := testBuffer("dict.val", ValuesBuffer, 5)
	dictVal.ValueWidth = 4
	dictionary := &Node{
		ID: "dict", Name: "dict", Kind: PrimitiveNode, Length: 5,
		ValueWidth: 4, Value: dictVal,
	}
	idxA := testBuffer("a.idx", DictIndexBuffer, 4)
	idxB := testBuffer("b.idx", DictIndexBuffer, 3)
	parentA := &Node{
		ID: "a", Name: "a", Kind: DictionaryNode, Length: 4,
		Index: idxA, Dictionary: dictionary,
		DictIndexData: []int64{4, 3, 2, 1}, // reversed order
	}
	parentB := &Node{
		ID: "b", Name: "b", Kind: DictionaryNode, Length: 3,
		Index: idxB, Dictionary: dictionary,
		DictIndexData: []int64{0, 2, 4}, // overlaps A's shared segment
	}
	structValid := testBuffer("s.valid", ValidityBuffer, 2)
	root := &Node{
		ID: "root", Name: "root", Kind: StructNode, Length: 2,
		Validity: structValid,
		Children: []*Node{parentA, parentB},
	}
	payloads := map[string][]byte{
		"dict.val": primitivePayload(5, 4),
		"a.idx":    int64Payload(4, 3, 2, 1),
		"b.idx":    int64Payload(0, 2, 4),
		"s.valid":  bitmapPayload([]bool{true, true}),
	}
	return root, payloads
}

func TestMultipleParentsShareReversedDictionary(t *testing.T) {
	root, payloads := buildSharedDictionarySchema()
	store := NewStore(payloads)
	rows := Interval(0, 2)
	plan, err := BuildPlan(root, Query{TopRows: rows}, Options{StartupCost: 7, PerByteCost: 3})
	if err != nil {
		t.Fatalf("BuildPlan: %v", err)
	}
	assertNoForbiddenLeak(t, plan)

	aLayer := plan.Root.Children[0].Layer
	bLayer := plan.Root.Children[1].Layer
	if aLayer.Dict.DictionaryLayer != bLayer.Dict.DictionaryLayer {
		t.Fatalf("dictionary subtree was copied instead of shared")
	}
	if aLayer.Dict.OrderedIndices.APs[0].Step != -1 {
		t.Fatalf("reversed selection lost its negative step: %+v", aLayer.Dict.OrderedIndices.APs)
	}

	oracle, err := DecodeOracle(root, store, rows)
	if err != nil {
		t.Fatalf("oracle: %v", err)
	}
	replayed, err := DecodePlan(plan, root, store)
	if err != nil {
		t.Fatalf("plan decode: %v", err)
	}
	if !AssertEquivalent(oracle, replayed) {
		t.Fatalf("dictionary mismatch %v vs %v", oracle, replayed)
	}

	// Both parents together touch dictionary indices [0,5), yielding one
	// shared read instead of duplicated dictionary buffers.
	assertSegmentsCoverRows(t, plan, "dict.val", ByteRange{Start: 0, End: 20})
}

func TestGiantArithmeticProgressionStaysSymbolic(t *testing.T) {
	const count = int64(1) << 44
	const step = int64(1) << 10
	lastRow := addChecked(1, mulChecked(step, count-1))
	validity := testBuffer("big.valid", ValidityBuffer, count)
	offsets := testBuffer("big.off", OffsetsBuffer, count+1)
	childLength := mulChecked(addChecked(lastRow, 1), 2)
	childVal := testBuffer("big.child", ValuesBuffer, childLength)
	childVal.ValueWidth = 1
	child := &Node{
		ID: "child", Name: "child", Kind: PrimitiveNode, Length: childLength,
		ValueWidth: 1, Value: childVal,
	}
	root := &Node{
		ID: "root", Name: "root", Kind: ListNode, Length: count,
		Validity:  validity,
		NullRows:  EmptySet(0, count),
		Offsets:   offsets,
		FixedSize: true, OffsetFirst: 0, Stride: 2,
		Children: []*Node{child},
	}
	rows := SetOf(AP{First: 1, Step: step, Count: count})
	plan, err := BuildPlan(root, Query{TopRows: rows}, Options{StartupCost: 1, PerByteCost: 1})
	if err != nil {
		t.Fatalf("giant plan: %v", err)
	}
	if got := rows.Count(); got != count {
		t.Fatalf("AP count changed: got %d want %d", got, count)
	}
	// The periodic form must survive: no normalized interval materializes 2^44.
	if got := plan.Root.Rows.Count(); got != count {
		t.Fatalf("root row count = %d, want %d", got, count)
	}
	childLayer := plan.Root.Children[0].Layer
	if got := childLayer.Rows.Count(); got != count {
		t.Fatalf("child row count = %d, want %d", got, count)
	}
	if childLayer.Rows.APs[0].Step != 2*step {
		t.Fatalf("child AP step = %d, want %d", childLayer.Rows.APs[0].Step, 2*step)
	}

	// Reads are two hulls (bitmap and child); offsets remain affine and do not
	// require physical offset bytes to restore boundaries.
	if plan.Root.Offsets.FixedSize != true {
		t.Fatalf("fixed-size offset rule missing")
	}
	if _, exists := plan.RequiredRanges["big.off"]; exists {
		t.Fatalf("affine offsets must not require offset buffer reads")
	}
}

func TestGiantIrregularVariableListRejected(t *testing.T) {
	const count = int64(1) << 40
	offsets := testBuffer("var.off", OffsetsBuffer, count+1)
	values := testBuffer("var.val", ValuesBuffer, count)
	values.ValueWidth = 1
	child := &Node{ID: "child", Name: "child", Kind: PrimitiveNode, Length: count, ValueWidth: 1, Value: values}
	root := &Node{
		ID: "root", Name: "root", Kind: ListNode, Length: count,
		NullRows: EmptySet(0, count),
		Offsets:  offsets,
		OffsetFn: func(row int64) int64 { return (row * row) % count },
		Children: []*Node{child},
	}
	rows := SetOf(AP{First: 0, Step: 7, Count: count})
	_, err := BuildPlan(root, Query{TopRows: rows}, Options{})
	if err != ErrIrregularTooLarge {
		t.Fatalf("irregular giant list error = %v, want ErrIrregularTooLarge", err)
	}
}

func TestForbiddenBarriersAndOptimalCost(t *testing.T) {
	value := testBuffer("v", ValuesBuffer, 16)
	value.ValueWidth = 1
	root := &Node{
		ID: "root", Name: "root", Kind: PrimitiveNode, Length: 16,
		NullRows: EmptySet(0, 16), ValueWidth: 1, Value: value,
	}
	rows := SetOf(
		AP{First: 0, Step: 1, Count: 3},  // [0,3)
		AP{First: 6, Step: 1, Count: 2},  // [6,8)
		AP{First: 12, Step: 1, Count: 2}, // [12,14)
	)
	forbidden := map[string][]ByteRange{
		"v": {{Start: 3, End: 6}, {Start: 8, End: 12}},
	}

	// High startup cost: one spanning read per barrier-free component.
	planHigh, err := BuildPlan(root, Query{TopRows: rows}, Options{
		StartupCost: 100, PerByteCost: 1, Forbidden: forbidden,
	})
	if err != nil {
		t.Fatalf("BuildPlan: %v", err)
	}
	assertNoForbiddenLeak(t, planHigh)
	wantHigh := []ByteRange{{Start: 0, End: 3}, {Start: 6, End: 8}, {Start: 12, End: 14}}
	gotHigh := segmentsOn(planHigh, "v")
	if !rangesEqual(gotHigh, wantHigh) {
		t.Fatalf("high startup segments = %v, want %v", gotHigh, wantHigh)
	}

	// Low startup cost: split at the internal gap [3,6) where over-reading
	// would cost more than an extra start.
	planLow, err := BuildPlan(root, Query{TopRows: rows}, Options{
		StartupCost: 1, PerByteCost: 10, Forbidden: forbidden,
	})
	if err != nil {
		t.Fatalf("BuildPlan low: %v", err)
	}
	gotLow := segmentsOn(planLow, "v")
	wantLow := []ByteRange{{Start: 0, End: 3}, {Start: 6, End: 8}, {Start: 12, End: 14}}
	if !rangesEqual(gotLow, wantLow) {
		t.Fatalf("low startup segments = %v, want %v", gotLow, wantLow)
	}

	// Verify optimality against exhaustive segment combinations. Forbidden
	// ranges make the three components independent.
	brute := bruteBestCost([]ByteRange{{Start: 0, End: 3}}, 1, 10) +
		bruteBestCost([]ByteRange{{Start: 6, End: 8}}, 1, 10) +
		bruteBestCost([]ByteRange{{Start: 12, End: 14}}, 1, 10)
	if planLow.Cost < brute {
		t.Fatalf("planner cost %d below brute-force optimum %d", planLow.Cost, brute)
	}
	if planLow.Cost != brute {
		t.Fatalf("planner cost %d != brute-force optimum %d", planLow.Cost, brute)
	}
}

func TestAdjacentReadsAreShared(t *testing.T) {
	value := testBuffer("adj", ValuesBuffer, 8)
	value.ValueWidth = 1
	root := &Node{
		ID: "root", Name: "root", Kind: PrimitiveNode, Length: 8,
		NullRows: EmptySet(0, 8), ValueWidth: 1, Value: value,
	}
	rows := SetOf(AP{First: 0, Step: 1, Count: 4}, AP{First: 4, Step: 1, Count: 4})
	plan, err := BuildPlan(root, Query{TopRows: rows}, Options{StartupCost: 9, PerByteCost: 1})
	if err != nil {
		t.Fatalf("BuildPlan: %v", err)
	}
	got := segmentsOn(plan, "adj")
	want := []ByteRange{{Start: 0, End: 8}}
	if !rangesEqual(got, want) {
		t.Fatalf("adjacent segments = %v, want merged %v", got, want)
	}
}

func TestPeriodicSetOperations(t *testing.T) {
	a := SetOf(AP{First: 0, Step: 2, Count: 4}) // 0,2,4,6
	b := SetOf(AP{First: 1, Step: 2, Count: 3}) // 1,3,5
	u := Union(a, b)
	if u.Count() != 7 {
		t.Fatalf("union count = %d, want 7", u.Count())
	}
	if !u.Contains(6) || u.Contains(7) {
		t.Fatalf("union membership wrong: %+v", u)
	}
	reversed := SetOf(AP{First: 9, Step: -2, Count: 5})
	lo, hi, ok := reversed.Hull()
	if !ok || lo != 1 || hi != 10 {
		t.Fatalf("reversed hull = [%d,%d) ok=%v", lo, hi, ok)
	}
	scaled := reversed.Affine(1, 0)
	if scaled.APs[0].First != 9 || scaled.APs[0].Step != -2 || scaled.APs[0].Count != 5 {
		t.Fatalf("reversed progression altered: %+v", scaled.APs[0])
	}
	byTwo := reversed.APs[0].Scale(2)
	if byTwo.First != 18 || byTwo.Step != -4 || byTwo.Count != 5 {
		t.Fatalf("scaled reversed progression = %+v", byTwo)
	}
	shifted := a.Shift(10)
	if shifted.APs[0].First != 10 || shifted.Lo != 10 {
		t.Fatalf("shift wrong: %+v", shifted)
	}
}

func segmentsOn(plan *Plan, id string) []ByteRange {
	var out []ByteRange
	for _, seg := range plan.Segments {
		if seg.BufferID == id {
			out = append(out, seg.ByteRange)
		}
	}
	return out
}

func rangesEqual(a, b []ByteRange) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func bruteBestCost(group []ByteRange, startup, perByte int64) int64 {
	n := len(group)
	dp := make([]int64, n+1)
	for j := 1; j <= n; j++ {
		best := maxInt64
		for i := 0; i < j; i++ {
			span := group[j-1].End - group[i].Start
			cost := dp[i] + startup + span*perByte
			if cost < best {
				best = cost
			}
		}
		dp[j] = best
	}
	return dp[n]
}
