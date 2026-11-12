package taskpkg

import (
	"errors"
	"reflect"
	"testing"
)

func rect(x, y, width, height float64) Rect {
	return Rect{X: x, Y: y, Width: width, Height: height}
}

func block(id int, x, y, width, height float64) Block {
	return Block{NodeID: id, Rect: rect(x, y, width, height)}
}

func TestLTRColumnMajorOrder(t *testing.T) {
	blocks := []Block{
		block(4, 300, 100, 220, 40),
		block(1, 0, 10, 220, 40),
		block(3, 300, 10, 220, 40),
		block(2, 0, 100, 220, 40),
	}

	result, err := BuildReadingOrder(blocks, []float64{260}, LTR)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := result.Order, []string{"1", "2", "3", "4"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("order = %v, want %v", got, want)
	}
	if result.CrossingCount != 0 {
		t.Fatalf("crossing count = %d, want 0", result.CrossingCount)
	}
}

func TestRTLReversesColumnOrder(t *testing.T) {
	blocks := []Block{
		block(4, 300, 100, 220, 40),
		block(1, 0, 10, 220, 40),
		block(3, 300, 10, 220, 40),
		block(2, 0, 100, 220, 40),
	}

	result, err := BuildReadingOrder(blocks, []float64{260}, RTL)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := result.Order, []string{"3", "4", "1", "2"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("order = %v, want %v", got, want)
	}
}

func TestSpanningHeadingIsExemptAndReadFirst(t *testing.T) {
	blocks := []Block{
		block(2, 0, 70, 210, 40),
		block(4, 290, 70, 210, 40),
		{NodeID: "title", Heading: true, Rect: rect(0, 0, 500, 30)},
		block(1, 0, 30, 210, 40),
		block(3, 290, 30, 210, 40),
	}

	result, err := BuildReadingOrder(blocks, []float64{250}, LTR)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := result.Order, []string{"title", "1", "2", "3", "4"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("order = %v, want %v", got, want)
	}
	if result.CrossingCount != 0 {
		t.Fatalf("heading must not contribute to crossing cost: got %d", result.CrossingCount)
	}
	conflict := findConflict(t, result, "title")
	if !hasReason(conflict, ReasonHeadingSpansDivider) {
		t.Fatalf("title reasons = %v, want %v", conflict.Reasons, ReasonHeadingSpansDivider)
	}
}

func TestRTLSpanningHeadingStillReadsFirst(t *testing.T) {
	blocks := []Block{
		block(1, 0, 30, 210, 40),
		block(3, 290, 30, 210, 40),
		{NodeID: "title", Heading: true, Rect: rect(0, 0, 500, 30)},
		block(2, 0, 70, 210, 40),
		block(4, 290, 70, 210, 40),
	}

	result, err := BuildReadingOrder(blocks, []float64{250}, RTL)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := result.Order, []string{"title", "3", "4", "1", "2"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("order = %v, want %v", got, want)
	}
}

func TestExhaustivePartitionEnumerationChoosesCheapest(t *testing.T) {
	blocks := []Block{
		block(1, 0, 0, 100, 30),
		block(2, 50, 80, 200, 30),
		block(3, 400, 0, 100, 30),
	}
	// 150 crosses block 2; 250 lies in the gutter. Both subsets are valid,
	// but 150 has cost one non-heading block and 250 has cost zero.
	dividers := []float64{150, 250}

	result, err := BuildReadingOrder(blocks, dividers, LTR)
	if err != nil {
		t.Fatal(err)
	}
	if result.EvaluatedPartitions != 3 {
		t.Fatalf("evaluated partitions = %d, want all 3 non-empty subsets", result.EvaluatedPartitions)
	}
	if got, want := result.SelectedDividers, []float64{250}; !reflect.DeepEqual(got, want) {
		t.Fatalf("selected = %v, want %v", got, want)
	}
	if result.CrossingCount != 0 {
		t.Fatalf("crossing count = %d, want 0", result.CrossingCount)
	}
}

func TestAllEffectiveSubsetsAreEnumerated(t *testing.T) {
	blocks := []Block{
		block(1, 0, 0, 80, 30),
		block(2, 120, 0, 80, 30),
		block(3, 240, 0, 80, 30),
		block(4, 360, 0, 80, 30),
	}

	result, err := BuildReadingOrder(blocks, []float64{340, 100, 220}, LTR)
	if err != nil {
		t.Fatal(err)
	}
	if result.EvaluatedPartitions != 7 {
		t.Fatalf("evaluated partitions = %d, want 2^3-1 = 7", result.EvaluatedPartitions)
	}
	if got, want := result.SelectedDividers, []float64{100, 220, 340}; !reflect.DeepEqual(got, want) {
		t.Fatalf("selected = %v, want all effective dividers %v", got, want)
	}
	if got, want := result.DividerIndices, []int{1, 2, 0}; !reflect.DeepEqual(got, want) {
		t.Fatalf("original divider indices = %v, want %v", got, want)
	}
}

func TestNonHeadingConflictIsReported(t *testing.T) {
	blocks := []Block{
		block(1, 0, 0, 100, 30),
		block(2, 100, 80, 200, 30),
		block(3, 400, 0, 90, 30),
	}

	result, err := BuildReadingOrder(blocks, []float64{200}, LTR)
	if err != nil {
		t.Fatal(err)
	}
	if result.CrossingCount != 1 {
		t.Fatalf("crossing count = %d, want 1", result.CrossingCount)
	}
	conflict := findConflict(t, result, "2")
	if !hasReason(conflict, ReasonCrossesDivider) {
		t.Fatalf("block 2 reasons = %v", conflict.Reasons)
	}
	if got, want := conflict.Dividers, []float64{200}; !reflect.DeepEqual(got, want) {
		t.Fatalf("conflict dividers = %v, want %v", got, want)
	}
}

func TestFloatingAnnotationUsesExplicitAncestorNotNearbyPosition(t *testing.T) {
	// The annotation is physically above its parent and overlaps no column
	// line. Only the explicit ancestor relation may move it after the parent.
	blocks := []Block{
		{NodeID: 2, Rect: rect(220, 10, 180, 30), Ancestors: []any{1}},
		block(1, 0, 60, 200, 30),
	}

	result, err := BuildReadingOrder(blocks, nil, LTR)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := result.Order, []string{"1", "2"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("order = %v, want parent before floating annotation %v", got, want)
	}
	conflict := findConflict(t, result, "2")
	if !hasReason(conflict, ReasonAncestorReordered) {
		t.Fatalf("block 2 reasons = %v, want ancestor-reordered", conflict.Reasons)
	}
}

func TestNodeIDBreaksVerticalTie(t *testing.T) {
	blocks := []Block{
		block(10, 0, 20, 100, 30),
		block(2, 0, 20, 100, 30),
	}

	result, err := BuildReadingOrder(blocks, nil, LTR)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := result.Order, []string{"2", "10"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("order = %v, want numeric node-ID tie break %v", got, want)
	}
}

func TestCyclicAncestorsAreReported(t *testing.T) {
	blocks := []Block{
		{NodeID: 1, Rect: rect(0, 0, 100, 30), Ancestors: []int{2}},
		{NodeID: 2, Rect: rect(0, 40, 100, 30), Ancestors: []int{1}},
	}

	result, err := BuildReadingOrder(blocks, nil, LTR)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := result.Order, []string{"1", "2"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("order = %v, want deterministic cycle break %v", got, want)
	}
	for _, id := range []string{"1", "2"} {
		if conflict := findConflict(t, result, id); !hasReason(conflict, ReasonAncestorCycle) {
			t.Fatalf("block %s reasons = %v, want cycle", id, conflict.Reasons)
		}
	}
}

func TestWrapperAncestorIsNotAConflict(t *testing.T) {
	blocks := []Block{
		{NodeID: 1, Rect: rect(0, 0, 100, 30), Ancestors: []string{"article-wrapper"}},
		{NodeID: 2, Rect: rect(0, 40, 100, 30), Ancestors: []string{"article-wrapper"}},
	}

	result, err := BuildReadingOrder(blocks, nil, LTR)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Conflicts) != 0 {
		t.Fatalf("container ancestors must be ignored, got conflicts %#v", result.Conflicts)
	}
}

func TestNoEffectiveDividerLeavesOneColumn(t *testing.T) {
	blocks := []Block{
		block(1, 0, 20, 100, 30),
		block(2, 0, 60, 100, 30),
	}

	result, err := BuildReadingOrder(blocks, []float64{500}, LTR)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.SelectedDividers) != 0 {
		t.Fatalf("selected dividers = %v, want none", result.SelectedDividers)
	}
	if got, want := result.UnusedDividers, []float64{500}; !reflect.DeepEqual(got, want) {
		t.Fatalf("unused dividers = %v, want %v", got, want)
	}
}

func TestLimitsAndValidation(t *testing.T) {
	tooManyBlocks := make([]Block, MaxBlocks+1)
	for index := range tooManyBlocks {
		tooManyBlocks[index] = block(index, float64(index*20), 0, 10, 10)
	}
	if _, err := BuildReadingOrder(tooManyBlocks, nil, LTR); !errors.Is(err, ErrTooManyBlocks) {
		t.Fatalf("error = %v, want ErrTooManyBlocks", err)
	}

	if _, err := BuildReadingOrder(nil, make([]float64, MaxDividers+1), LTR); !errors.Is(err, ErrTooManyDividers) {
		t.Fatalf("error = %v, want ErrTooManyDividers", err)
	}
	if _, err := BuildReadingOrder(nil, nil, Direction("sideways")); !errors.Is(err, ErrInvalidDirection) {
		t.Fatalf("error = %v, want ErrInvalidDirection", err)
	}
	if _, err := BuildReadingOrder([]Block{{NodeID: 1, Rect: Rect{Left: 10, Right: 1}}}, nil, LTR); !errors.Is(err, ErrInvalidRect) {
		t.Fatalf("error = %v, want ErrInvalidRect", err)
	}
}

func findConflict(t *testing.T, result Result, id string) Conflict {
	t.Helper()
	for _, conflict := range result.Conflicts {
		if conflict.NodeID == id {
			return conflict
		}
	}
	t.Fatalf("no conflict for block %q; conflicts = %#v", id, result.Conflicts)
	return Conflict{}
}

func hasReason(conflict Conflict, reason Reason) bool {
	for _, current := range conflict.Reasons {
		if current == reason {
			return true
		}
	}
	return false
}
