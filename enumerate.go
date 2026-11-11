package netsec

import "fmt"

// EnumerateTrajectories exhaustively lists every complete feasible trajectory
// over the problem. It is intended for small grids used to cross-check the
// dynamic-programming solver.
//
// maxPaths caps the number of returned trajectories; when the cap is reached
// enumeration stops and the function returns ErrEnumerationLimit along with the
// partial results. A non-positive maxPath removes the cap.
func EnumerateTrajectories(p Problem, maxPaths int) ([][]Cell, error) {
	if _, err := NewSolver(p); err != nil {
		return nil, err
	}
	maxSpeed := p.MaxSpeed
	timeCount := len(p.Allowed)
	allowedSet := make([]map[Cell]struct{}, timeCount)
	for t, cells := range p.Allowed {
		set := make(map[Cell]struct{}, len(cells))
		for _, cell := range cells {
			set[cell] = struct{}{}
		}
		allowedSet[t] = set
	}

	var paths [][]Cell
	cursor := make([]Cell, timeCount)
	var dfs func(t int, cell Cell, prevSpeed, dist int) bool
	dfs = func(t int, cell Cell, prevSpeed, dist int) bool {
		cursor[t] = cell
		if t == timeCount-1 {
			if dist < p.TotalLength.Min || dist > p.TotalLength.Max {
				return true
			}
			path := make([]Cell, timeCount)
			copy(path, cursor)
			paths = append(paths, path)
			return maxPaths <= 0 || len(paths) < maxPaths
		}
		moves, err := enumerateMoves(p.Graph, cell)
		if err != nil {
			return true
		}
		for _, mv := range moves {
			if maxSpeed > 0 && mv.speed > maxSpeed {
				continue
			}
			if t > 0 && absInt(mv.speed-prevSpeed) > p.MaxSpeedChange {
				continue
			}
			nextDist := dist + mv.speed
			if nextDist > p.TotalLength.Max {
				continue
			}
			if _, ok := allowedSet[t+1][mv.to]; !ok {
				continue
			}
			if !dfs(t+1, mv.to, mv.speed, nextDist) {
				return false
			}
		}
		return true
	}

	for _, start := range sortedCellSet(allowedSet[0]) {
		if !dfs(0, start, 0, 0) {
			break
		}
	}
	if maxPaths > 0 && len(paths) >= maxPaths {
		return paths, ErrEnumerationLimit
	}
	return paths, nil
}

// ErrEnumerationLimit reports that exhaustive enumeration hit maxPaths.
var ErrEnumerationLimit = fmt.Errorf("netsec: trajectory enumeration limit reached")

func enumerateMoves(graph Graph, cell Cell) ([]move, error) {
	moves := []move{{to: cell, speed: 0}}
	for _, edge := range graph.Edges(cell) {
		if edge.Length < 0 {
			return nil, fmt.Errorf("%w: edge length must be non-negative", ErrInvalidProblem)
		}
		moves = append(moves, move{to: edge.To, speed: edge.Length})
	}
	return dedupeMoves(sortedMoves(moves)), nil
}

// SupportFromTrajectories derives the per-time support cells and unique ranges
// from an exhaustive list of feasible trajectories.
func SupportFromTrajectories(paths [][]Cell) ([][]Cell, []Range) {
	if len(paths) == 0 {
		return nil, nil
	}
	timeCount := len(paths[0])
	supportSet := make([]map[Cell]struct{}, timeCount)
	for t := 0; t < timeCount; t++ {
		supportSet[t] = make(map[Cell]struct{})
	}
	for _, path := range paths {
		for t, cell := range path {
			supportSet[t][cell] = struct{}{}
		}
	}
	supports := make([][]Cell, timeCount)
	for t := range supportSet {
		supports[t] = sortedCellSet(supportSet[t])
	}
	return supports, MergeUniqueRanges(supports)
}
