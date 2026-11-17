package taskpkg

import (
	"errors"
	"reflect"
	"testing"
)

func block(id string, x, y, width, height float64) Block {
	return Block{ID: id, Rect: NewRect(x, y, width, height)}
}

func heading(id string, x, y, width, height float64) Block {
	result := block(id, x, y, width, height)
	result.Heading = true
	return result
}

func requireOrder(t *testing.T, got Result, want []string) {
	t.Helper()
	if !reflect.DeepEqual(got.Order, want) {
		t.Fatalf("order = %v, want %v (conflicts: %v)", got.Order, want, got.Conflicts)
	}
}

func hasReason(conflicts []Conflict, blockID string, reason ConflictReason) bool {
	for _, conflict := range conflicts {
		if conflict.BlockID == blockID && conflict.Reason == reason {
			return true
		}
	}
	return false
}

func TestCrossColumnHeading(t *testing.T) {
	result, err := Order(Input{
		Blocks: []Block{
			heading("title", 0, 0, 220, 24),
			block("left", 0, 40, 100, 40),
			block("right", 140, 40, 80, 40),
		},
		Dividers:  []float64{120},
		Direction: LTR,
	})
	if err != nil {
		t.Fatal(err)
	}

	requireOrder(t, result, []string{"title", "left", "right"})
	if result.Crossings != 0 || result.Dividers[0] != 120 {
		t.Fatalf("crossings/dividers = %d/%v, want 0/[120]", result.Crossings, result.Dividers)
	}
	if !hasReason(result.Conflicts, "title", ReasonHeadingSpansDivider) {
		t.Fatalf("missing heading-span conflict: %v", result.Conflicts)
	}
}

func TestCrossColumnHeadingSplitsVerticalFlow(t *testing.T) {
	result, err := Order(Input{
		Blocks: []Block{
			block("left-top", 0, 0, 90, 20),
			block("right-top", 130, 0, 90, 20),
			heading("mid-title", 0, 50, 220, 20),
			block("left-bottom", 0, 100, 90, 20),
			block("right-bottom", 130, 100, 90, 20),
		},
		Dividers:  []float64{110},
		Direction: LTR,
	})
	if err != nil {
		t.Fatal(err)
	}

	requireOrder(t, result, []string{
		"left-top", "right-top", "mid-title", "left-bottom", "right-bottom",
	})
}

func TestFloatingAnnotationUsesAncestorNotNearestColumn(t *testing.T) {
	parent := block("article", 0, 40, 90, 20)
	annotation := block("annotation", 90, 30, 80, 20)
	annotation.Ancestors = []string{"article"}

	result, err := Order(Input{
		Blocks: []Block{
			parent,
			annotation,
			block("aside", 130, 100, 70, 20),
		},
		Dividers:  []float64{110},
		Direction: LTR,
	})
	if err != nil {
		t.Fatal(err)
	}

	// The annotation's center is in the right column, but its DOM owner is the
	// left-column article. It must stay after that article, not be attached to
	// the visually nearest sidebar block.
	requireOrder(t, result, []string{"article", "annotation", "aside"})
	if !hasReason(result.Conflicts, "annotation", ReasonCrossesDivider) {
		t.Fatalf("missing divider crossing conflict: %v", result.Conflicts)
	}
	if !hasReason(result.Conflicts, "annotation", ReasonAncestorColumn) {
		t.Fatalf("missing ancestor-column conflict: %v", result.Conflicts)
	}
	if !hasReason(result.Conflicts, "annotation", ReasonAncestorOrder) {
		t.Fatalf("missing ancestor-order conflict: %v", result.Conflicts)
	}
}

func TestRTLColumnOrder(t *testing.T) {
	result, err := Order(Input{
		Blocks: []Block{
			heading("title", 0, 0, 220, 20),
			block("left", 0, 40, 90, 20),
			block("right", 130, 40, 90, 20),
		},
		Dividers:  []float64{110},
		Direction: RTL,
	})
	if err != nil {
		t.Fatal(err)
	}

	requireOrder(t, result, []string{"title", "right", "left"})
}

func TestAncestorPrecedesVerticalPositionAndIDBreaksTies(t *testing.T) {
	child := block("child", 0, 10, 80, 20)
	child.Ancestors = []string{"parent"}

	result, err := Order(Input{
		Blocks: []Block{
			child,
			block("parent", 0, 100, 80, 20),
			block("b", 120, 0, 80, 20),
			block("a", 120, 0, 80, 20),
		},
		Dividers:  []float64{100},
		Direction: LTR,
	})
	if err != nil {
		t.Fatal(err)
	}

	requireOrder(t, result, []string{"parent", "child", "a", "b"})
	if !hasReason(result.Conflicts, "child", ReasonAncestorOrder) {
		t.Fatalf("missing ancestor-order conflict: %v", result.Conflicts)
	}
}

func TestExhaustivePartitionChoosesCleanestSubset(t *testing.T) {
	blocks := []Block{
		block("a", 0, 0, 40, 20),
		block("b", 50, 0, 20, 20),
		block("c", 80, 0, 40, 20),
	}
	result, err := Order(Input{
		Blocks:    blocks,
		Dividers:  []float64{40, 60, 80},
		Direction: LTR,
	})
	if err != nil {
		t.Fatal(err)
	}

	wantDividers, wantCrossings := bruteBestPartition(blocks, []float64{40, 60, 80})
	if !reflect.DeepEqual(result.Dividers, wantDividers) || result.Crossings != wantCrossings {
		t.Fatalf("partition = %v/%d, want %v/%d",
			result.Dividers, result.Crossings, wantDividers, wantCrossings)
	}
	if !reflect.DeepEqual(result.Dividers, []float64{40, 80}) || result.Crossings != 0 {
		t.Fatalf("unexpected exhaustive choice: %v/%d", result.Dividers, result.Crossings)
	}
	requireOrder(t, result, []string{"a", "b", "c"})
}

func TestLimitsAndValidation(t *testing.T) {
	tooMany := make([]Block, MaxBlocks+1)
	for i := range tooMany {
		tooMany[i] = block(string(rune('a'+i)), float64(i), 0, 1, 1)
	}
	if _, err := Order(Input{Blocks: tooMany}); !errors.Is(err, ErrTooManyBlocks) {
		t.Fatalf("error = %v, want ErrTooManyBlocks", err)
	}

	if _, err := Order(Input{Dividers: []float64{1, 2, 3, 4, 5, 6, 7}}); !errors.Is(err, ErrTooManyDividers) {
		t.Fatalf("error = %v, want ErrTooManyDividers", err)
	}

	cycle := []Block{block("a", 0, 0, 1, 1), block("b", 0, 0, 1, 1)}
	cycle[0].Ancestors = []string{"b"}
	cycle[1].Ancestors = []string{"a"}
	if _, err := Order(Input{Blocks: cycle}); !errors.Is(err, ErrAncestorCycle) {
		t.Fatalf("error = %v, want ErrAncestorCycle", err)
	}
}

func bruteBestPartition(blocks []Block, candidates []float64) ([]float64, int) {
	bestMask := 0
	bestScore := 1 << 30
	bestCount := -1
	for mask := 1; mask < 1<<len(candidates); mask++ {
		var lines []float64
		for i, candidate := range candidates {
			if mask&(1<<i) != 0 {
				lines = append(lines, candidate)
			}
		}
		seen := make([]bool, len(lines)+1)
		for _, item := range blocks {
			center := item.Rect.X + item.Rect.Width/2
			column := 0
			for column < len(lines) && center >= lines[column] {
				column++
			}
			seen[column] = true
		}
		valid := true
		for _, nonEmpty := range seen {
			valid = valid && nonEmpty
		}
		if !valid {
			continue
		}

		crossed := make(map[string]bool)
		for _, line := range lines {
			for _, item := range blocks {
				if !item.Heading && item.Rect.X < line && line < item.Rect.X+item.Rect.Width {
					crossed[item.ID] = true
				}
			}
		}
		score := len(crossed)
		count := len(lines)
		if score < bestScore || (score == bestScore && count > bestCount) ||
			(score == bestScore && count == bestCount && mask < bestMask) {
			bestMask = mask
			bestScore = score
			bestCount = count
		}
	}

	var lines []float64
	for i, candidate := range candidates {
		if bestMask&(1<<i) != 0 {
			lines = append(lines, candidate)
		}
	}
	return lines, bestScore
}
