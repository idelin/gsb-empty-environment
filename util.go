package netsec

import (
	"fmt"
	"sort"
)

func validateProblem(p Problem) error {
	if p.Graph == nil {
		return fmt.Errorf("%w: nil graph", ErrInvalidProblem)
	}
	if len(p.Allowed) == 0 {
		return fmt.Errorf("%w: no time steps", ErrInvalidProblem)
	}
	if p.TotalLength.Min < 0 || p.TotalLength.Max < p.TotalLength.Min {
		return fmt.Errorf("%w: bad total length interval", ErrInvalidProblem)
	}
	if p.MaxSpeedChange < 0 {
		return fmt.Errorf("%w: negative speed change bound", ErrInvalidProblem)
	}
	for t, cells := range p.Allowed {
		if len(cells) == 0 {
			return fmt.Errorf("%w: empty allowed set at time %d", ErrInvalidProblem, t)
		}
		seen := make(map[Cell]struct{}, len(cells))
		for _, cell := range cells {
			if _, dup := seen[cell]; dup {
				return fmt.Errorf("%w: duplicate cell at time %d", ErrInvalidProblem, t)
			}
			seen[cell] = struct{}{}
		}
	}
	return nil
}

func sortedCellSet(set map[Cell]struct{}) []Cell {
	cells := make([]Cell, 0, len(set))
	for cell := range set {
		cells = append(cells, cell)
	}
	sort.Slice(cells, func(i, j int) bool {
		if cells[i].Row != cells[j].Row {
			return cells[i].Row < cells[j].Row
		}
		return cells[i].Col < cells[j].Col
	})
	return cells
}

func sortedStateKeys(set map[stateKey]struct{}) []stateKey {
	keys := make([]stateKey, 0, len(set))
	for key := range set {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].cell.Row != keys[j].cell.Row {
			return keys[i].cell.Row < keys[j].cell.Row
		}
		if keys[i].cell.Col != keys[j].cell.Col {
			return keys[i].cell.Col < keys[j].cell.Col
		}
		if keys[i].speed != keys[j].speed {
			return keys[i].speed < keys[j].speed
		}
		return keys[i].dist < keys[j].dist
	})
	return keys
}

func sortedMoves(moves []move) []move {
	sort.Slice(moves, func(i, j int) bool {
		if moves[i].to.Row != moves[j].to.Row {
			return moves[i].to.Row < moves[j].to.Row
		}
		if moves[i].to.Col != moves[j].to.Col {
			return moves[i].to.Col < moves[j].to.Col
		}
		return moves[i].speed < moves[j].speed
	})
	return moves
}

func absInt(v int) int {
	if v < 0 {
		return -v
	}
	return v
}

// MergeUniqueRanges returns the maximal consecutive time ranges whose support
// set contains exactly one cell. Ranges are half-open [Start, End).
func MergeUniqueRanges(supports [][]Cell) []Range {
	var ranges []Range
	start := -1
	for t, cells := range supports {
		if len(cells) == 1 {
			if start < 0 {
				start = t
			}
			continue
		}
		if start >= 0 {
			ranges = append(ranges, Range{Start: start, End: t})
			start = -1
		}
	}
	if start >= 0 {
		ranges = append(ranges, Range{Start: start, End: len(supports)})
	}
	return ranges
}
