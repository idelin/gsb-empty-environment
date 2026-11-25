package taskpkg

import (
	"errors"
	"fmt"
	"math"
	"math/rand"
	"testing"
)

func TestCrossColumnHeadingIsExemptAndOrderedOnce(t *testing.T) {
	req := Request{
		Blocks: []Block{
			{ID: "heading", Heading: true, Rect: RectFromLTRB(20, 10, 580, 50)},
			{ID: "left", Rect: RectFromLTRB(20, 80, 260, 180)},
			{ID: "right", Rect: RectFromLTRB(340, 80, 580, 180)},
		},
		CandidateLines: []CandidateLine{
			{ID: "true-gutter", X: 300},
			{ID: "bad-through-left", X: 140},
		},
		Direction: DirectionLTR,
	}
	assertExhaustiveMinimum(t, req)

	result, err := Plan(req)
	if err != nil {
		t.Fatal(err)
	}
	assertOrder(t, result.Order, "heading", "left", "right")
	assertContains(t, result.SelectedLineIDs, "true-gutter")
	if containsString(result.SelectedLineIDs, "bad-through-left") {
		t.Fatalf("bad line selected: %#v", result.SelectedLineIDs)
	}
	if !hasConflictCode(result.Conflicts, "cross_column_heading") {
		t.Fatalf("missing cross-column heading reason: %#v", result.Conflicts)
	}
	if hasConflictCode(result.Conflicts, "divider_crosses_non_heading") {
		t.Fatalf("selected gutter should not cross body blocks: %#v", result.Conflicts)
	}
	assertIDsOnce(t, result.Order, len(req.Blocks))

	reqRTL := req
	reqRTL.Direction = DirectionRTL
	rtl, err := Plan(reqRTL)
	if err != nil {
		t.Fatal(err)
	}
	assertOrder(t, rtl.Order, "heading", "right", "left")
	assertIDsOnce(t, rtl.Order, len(req.Blocks))
}

func TestRTLColumnProgression(t *testing.T) {
	req := Request{
		Blocks: []Block{
			{ID: "left-1", Rect: RectFromLTRB(20, 20, 260, 60)},
			{ID: "left-2", Rect: RectFromLTRB(20, 80, 260, 120)},
			{ID: "right-1", Rect: RectFromLTRB(340, 20, 580, 60)},
			{ID: "right-2", Rect: RectFromLTRB(340, 80, 580, 120)},
		},
		CandidateLines: []CandidateLine{{ID: "gutter", X: 300}},
	}
	assertExhaustiveMinimum(t, req)

	ltr, err := Plan(req)
	if err != nil {
		t.Fatal(err)
	}
	assertOrder(t, ltr.Order, "left-1", "left-2", "right-1", "right-2")

	req.Direction = RTL
	rtl, err := Plan(req)
	if err != nil {
		t.Fatal(err)
	}
	assertOrder(t, rtl.Order, "right-1", "right-2", "left-1", "left-2")
	assertIDsOnce(t, rtl.Order, len(req.Blocks))
}

func TestMidPageSpanningHeadingBreaksColumns(t *testing.T) {
	req := Request{
		Blocks: []Block{
			{ID: "left-above", Rect: RectFromLTRB(20, 20, 260, 60)},
			{ID: "right-above", Rect: RectFromLTRB(340, 20, 580, 60)},
			{ID: "heading", Heading: true, Rect: RectFromLTRB(20, 80, 580, 110)},
			{ID: "left-below", Rect: RectFromLTRB(20, 130, 260, 170)},
			{ID: "right-below", Rect: RectFromLTRB(340, 130, 580, 170)},
		},
		CandidateLines: []CandidateLine{{ID: "gutter", X: 300}},
	}

	result, err := Plan(req)
	if err != nil {
		t.Fatal(err)
	}
	assertOrder(t, result.Order,
		"left-above", "right-above",
		"heading",
		"left-below", "right-below",
	)

	req.Direction = DirectionRTL
	result, err = Plan(req)
	if err != nil {
		t.Fatal(err)
	}
	assertOrder(t, result.Order,
		"right-above", "left-above",
		"heading",
		"right-below", "left-below",
	)
}

func TestFloatingNoteUsesExplicitAncestorOrder(t *testing.T) {
	req := Request{
		Blocks: []Block{
			{
				ID:        "p1",
				Rect:      RectFromLTRB(20, 80, 240, 180),
				Ancestors: []string{"root", "article"},
			},
			{
				// ID is lexicographically before p1 and its top is higher.
				// Coordinate proximity/top ordering alone would put it first.
				ID:        "note-a",
				Rect:      RectFromLTRB(260, 70, 420, 140),
				Ancestors: []string{"root", "article"},
			},
			{
				ID:        "p2",
				Rect:      RectFromLTRB(20, 190, 240, 240),
				Ancestors: []string{"root", "article"},
			},
		},
		CandidateLines: []CandidateLine{
			{ID: "through-p1", X: 200},
			{ID: "through-note", X: 340},
		},
		NodeOrder: map[string]int{
			"p1":     1,
			"note-a": 2,
			"p2":     3,
		},
		Direction: DirectionLTR,
	}
	assertExhaustiveMinimum(t, req)

	result, err := Plan(req)
	if err != nil {
		t.Fatal(err)
	}
	// Both candidate dividers cross non-heading blocks, so the zero-conflict
	// empty subset is selected and the float remains in the article column.
	if result.SelectedMask != 0 {
		t.Fatalf("expected empty partition, got %#v", result.SelectedLineIDs)
	}
	assertOrder(t, result.Order, "p1", "note-a", "p2")
	if !hasConflictCode(result.Conflicts, "structure_resolves_overlap") {
		t.Fatalf("expected structural overlap reason: %#v", result.Conflicts)
	}
	if hasConflictCode(result.Conflicts, "vertical_ancestor_order_conflict") {
		t.Fatalf("test fixture should have consistent structure: %#v", result.Conflicts)
	}
	assertIDsOnce(t, result.Order, len(req.Blocks))

	// Horizontal distance must not act as a nearest-neighbor ordering rule.
	// Remove the dividers so moving the float cannot change columns; within
	// the same vertical structure, x proximity does not create ordering edges.
	noPartition := req
	noPartition.CandidateLines = nil
	noPartition.Blocks = append([]Block(nil), req.Blocks...)
	noPartition.Blocks[1].Rect.X = 500
	noPartition.Blocks[1].Rect.Width = 160
	result2, err := Plan(noPartition)
	if err != nil {
		t.Fatal(err)
	}
	assertOrder(t, result2.Order, "p1", "note-a", "p2")
}

func TestEqualKeysTieByOriginalNodeID(t *testing.T) {
	req := Request{
		Blocks: []Block{
			{ID: "b", Rect: RectFromLTRB(20, 20, 200, 60)},
			{ID: "a", Rect: RectFromLTRB(220, 20, 400, 60)},
		},
	}
	result, err := Plan(req)
	if err != nil {
		t.Fatal(err)
	}
	assertOrder(t, result.Order, "a", "b")
	if !hasConflictCode(result.Conflicts, "original_node_id_tie_break") {
		t.Fatalf("missing ID tie reason: %#v", result.Conflicts)
	}

	req.Blocks[0] = Block{ID: "public-1", NodeID: "z", Rect: req.Blocks[0].Rect}
	req.Blocks[1] = Block{ID: "public-2", NodeID: "a", Rect: req.Blocks[1].Rect}
	result, err = Plan(req)
	if err != nil {
		t.Fatal(err)
	}
	assertOrder(t, result.Order, "public-2", "public-1")
}

func TestParentMapEstablishesAncestorPrecedence(t *testing.T) {
	ancestorRect := RectFromLTRB(20, 20, 300, 100)
	req := Request{
		Blocks: []Block{
			{ID: "a-child", Rect: ancestorRect},
			{ID: "z-ancestor", Rect: ancestorRect},
		},
		Parents: map[string]string{
			"a-child": "z-ancestor",
		},
	}

	result, err := Plan(req)
	if err != nil {
		t.Fatal(err)
	}
	assertOrder(t, result.Order, "z-ancestor", "a-child")
}

func TestLimitsAndValidation(t *testing.T) {
	blocks := make([]Block, MaxBlocks+1)
	for i := range blocks {
		blocks[i] = Block{ID: string(rune('a'+i%26)) + string(rune('a'+(i/26)%26)) + string(rune('0'+i%10))}
	}
	if _, err := Plan(Request{Blocks: blocks}); !errors.Is(err, ErrTooManyBlocks) {
		t.Fatalf("expected ErrTooManyBlocks, got %v", err)
	}

	lines := make([]CandidateLine, MaxCandidateLines+1)
	for i := range lines {
		lines[i] = CandidateLine{ID: "l", X: float64(i)}
	}
	if _, err := Plan(Request{Blocks: []Block{{ID: "a"}}, CandidateLines: lines}); !errors.Is(err, ErrTooManyLines) {
		t.Fatalf("expected ErrTooManyLines, got %v", err)
	}

	if _, err := Plan(Request{Blocks: []Block{{Rect: Rect{Width: -1}}}}); err == nil {
		t.Fatal("expected empty ID error")
	}
	if _, err := Plan(Request{Blocks: []Block{{ID: "a"}, {ID: "a"}}}); !errors.Is(err, ErrDuplicateBlockID) {
		t.Fatalf("expected duplicate id, got %v", err)
	}
	if _, err := Plan(Request{Blocks: []Block{{ID: "a"}}, Direction: "sideways"}); !errors.Is(err, ErrInvalidDirection) {
		t.Fatalf("expected direction error, got %v", err)
	}
}

func TestRandomPagesMaintainPermutationAndMinimum(t *testing.T) {
	rng := rand.New(rand.NewSource(20261125))
	for iteration := 0; iteration < 200; iteration++ {
		blockCount := rng.Intn(MaxBlocks + 1)
		lineCount := rng.Intn(MaxCandidateLines + 1)
		req := Request{
			Blocks:         make([]Block, blockCount),
			CandidateLines: make([]CandidateLine, lineCount),
		}
		if rng.Intn(2) == 0 {
			req.Direction = DirectionLTR
		} else {
			req.Direction = DirectionRTL
		}

		for i := range req.Blocks {
			x := rng.Float64() * 700
			y := rng.Float64() * 900
			req.Blocks[i] = Block{
				ID:      fmt.Sprintf("n%02d", i),
				Heading: rng.Intn(7) == 0,
				Rect: Rect{
					X:      x,
					Y:      y,
					Width:  20 + rng.Float64()*220,
					Height: 10 + rng.Float64()*120,
				},
			}
		}
		for i := range req.CandidateLines {
			req.CandidateLines[i] = CandidateLine{
				ID: fmt.Sprintf("line-%d", i),
				X:  rng.Float64() * 900,
			}
		}

		assertExhaustiveMinimum(t, req)
		result, err := Plan(req)
		if err != nil {
			t.Fatalf("iteration %d: %v", iteration, err)
		}
		assertIDsOnce(t, result.Order, blockCount)
	}
}

func assertExhaustiveMinimum(t *testing.T, req Request) {
	t.Helper()
	partitions, err := EnumeratePartitions(req)
	if err != nil {
		t.Fatal(err)
	}
	if len(partitions) != 1<<len(req.candidateLines()) {
		t.Fatalf("expected full enumeration, got %d partitions for %d lines", len(partitions), len(req.candidateLines()))
	}

	minimum := bruteForceMinimumCrossedNonHeadings(t, req)
	foundOptimal := false
	for _, partition := range partitions {
		if partition.CrossedNonHeadingCount < minimum {
			t.Fatalf("enumerated count below brute force minimum: %#v", partition)
		}
		if partition.Optimal {
			if partition.CrossedNonHeadingCount != minimum {
				t.Fatalf("wrong optimal mark: %#v minimum=%d", partition, minimum)
			}
			foundOptimal = true
		}
	}
	if !foundOptimal {
		t.Fatal("no optimal partition marked")
	}

	result, err := Plan(req)
	if err != nil {
		t.Fatal(err)
	}
	selected := result.Partitions[result.SelectedMask]
	if selected.CrossedNonHeadingCount != minimum {
		t.Fatalf("selected penalty %d is not exhaustive minimum %d", selected.CrossedNonHeadingCount, minimum)
	}
}

func bruteForceMinimumCrossedNonHeadings(t *testing.T, req Request) int {
	t.Helper()
	lines := req.candidateLines()
	minimum := math.MaxInt
	for mask := 0; mask < 1<<len(lines); mask++ {
		count := 0
		for _, block := range req.Blocks {
			crossed := false
			for li, line := range lines {
				if mask&(1<<li) == 0 {
					continue
				}
				if line.X > block.Rect.Left() && line.X < block.Rect.Right() {
					crossed = true
					break
				}
			}
			if crossed && !block.Heading {
				count++
			}
		}
		if count < minimum {
			minimum = count
		}
	}
	return minimum
}

func assertOrder(t *testing.T, got []string, want ...string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("order length mismatch: got %v want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("order mismatch at %d: got %v want %v", i, got, want)
		}
	}
}

func assertIDsOnce(t *testing.T, ids []string, wantCount int) {
	t.Helper()
	if len(ids) != wantCount {
		t.Fatalf("expected %d ids, got %v", wantCount, ids)
	}
	seen := make(map[string]int, wantCount)
	for _, id := range ids {
		seen[id]++
		if seen[id] > 1 {
			t.Fatalf("id %q repeated in %v", id, ids)
		}
	}
}

func assertContains(t *testing.T, items []string, want string) {
	t.Helper()
	if !containsString(items, want) {
		t.Fatalf("%v does not contain %q", items, want)
	}
}

func containsString(items []string, want string) bool {
	for _, item := range items {
		if item == want {
			return true
		}
	}
	return false
}

func hasConflictCode(conflicts []Conflict, code string) bool {
	for _, conflict := range conflicts {
		if conflict.Code == code {
			return true
		}
	}
	return false
}
