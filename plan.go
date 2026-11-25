package taskpkg

import (
	"fmt"
	"math"
	"sort"
	"strings"
)

type engine struct {
	req    Request
	blocks []Block
	lines  []CandidateLine
	dir    Direction
	paths  [][]string
	orders map[string]int
	public []string
	tieIDs []string
}

type evaluatedPartition struct {
	partition Partition
	lineIdx   []int
	crossings map[int][]int
}

// Plan selects the best candidate-line subset and computes the reading order.
func Plan(req Request) (*Result, error) {
	e, err := newEngine(req)
	if err != nil {
		return nil, err
	}

	evaluated := e.enumerate()
	best := chooseBestPartition(evaluated)

	partitions := make([]Partition, len(evaluated))
	minPenalty := math.MaxInt
	for _, ep := range evaluated {
		if ep.partition.CrossedNonHeadingCount < minPenalty {
			minPenalty = ep.partition.CrossedNonHeadingCount
		}
	}
	for i := range evaluated {
		evaluated[i].partition.Optimal = evaluated[i].partition.CrossedNonHeadingCount == minPenalty
		partitions[i] = evaluated[i].partition
	}

	return e.buildResult(best, partitions), nil
}

// Analyze is a verb-first alias for hosts that prefer that name.
func Analyze(req Request) (*Result, error) {
	return Plan(req)
}

// Extract is another descriptive entry point.
func Extract(req Request) (*Result, error) {
	return Plan(req)
}

// ReadingOrder is the small-call API: it returns only the IDs and conflicts.
func ReadingOrder(blocks []Block, lines []CandidateLine, dir Direction) ([]string, []Conflict, error) {
	result, err := Plan(Request{
		Blocks:         blocks,
		CandidateLines: lines,
		Direction:      dir,
	})
	if err != nil {
		return nil, nil, err
	}
	return result.Order, result.Conflicts, nil
}

// EnumeratePartitions performs the complete 2^n candidate-line enumeration.
// n is at most MaxCandidateLines, so this never exceeds 64 partitions.
func EnumeratePartitions(req Request) ([]Partition, error) {
	e, err := newEngine(req)
	if err != nil {
		return nil, err
	}
	evaluated := e.enumerate()
	out := make([]Partition, len(evaluated))
	minPenalty := math.MaxInt
	for _, ep := range evaluated {
		if ep.partition.CrossedNonHeadingCount < minPenalty {
			minPenalty = ep.partition.CrossedNonHeadingCount
		}
	}
	for i := range evaluated {
		evaluated[i].partition.Optimal = evaluated[i].partition.CrossedNonHeadingCount == minPenalty
		out[i] = evaluated[i].partition
	}
	return out, nil
}

func (req Request) candidateLines() []CandidateLine {
	if len(req.CandidateLines) > 0 {
		return req.CandidateLines
	}
	if len(req.Lines) > 0 {
		return req.Lines
	}
	return req.Dividers
}

func (req Request) directionValue() Direction {
	if req.Direction != "" {
		return req.Direction
	}
	if req.WritingDirection != "" {
		return req.WritingDirection
	}
	return req.WritingDir
}

func newEngine(req Request) (*engine, error) {
	blocks := req.Blocks
	if len(blocks) > MaxBlocks {
		return nil, fmt.Errorf("%w: got %d", ErrTooManyBlocks, len(blocks))
	}

	lines := append([]CandidateLine(nil), req.candidateLines()...)
	if len(lines) > MaxCandidateLines {
		return nil, fmt.Errorf("%w: got %d", ErrTooManyLines, len(lines))
	}

	dir := req.directionValue()
	if dir == "" {
		dir = DirectionLTR
	}
	switch Direction(strings.ToLower(strings.TrimSpace(string(dir)))) {
	case DirectionLTR, "lr", "left-to-right":
		dir = DirectionLTR
	case DirectionRTL, "rl", "right-to-left":
		dir = DirectionRTL
	default:
		return nil, fmt.Errorf("%w: %q", ErrInvalidDirection, dir)
	}

	e := &engine{
		req:    req,
		blocks: append([]Block(nil), blocks...),
		dir:    dir,
		orders: make(map[string]int),
	}

	seenIDs := make(map[string]struct{}, len(blocks))
	e.public = make([]string, len(blocks))
	e.tieIDs = make([]string, len(blocks))
	e.paths = make([][]string, len(blocks))

	for i := range e.blocks {
		block := &e.blocks[i]
		block.Heading = block.Heading || block.IsHeading || block.Title || block.IsTitle
		if len(block.Ancestors) == 0 && len(block.AncestorIDs) > 0 {
			block.Ancestors = append([]string(nil), block.AncestorIDs...)
		}
	}

	for i, block := range e.blocks {
		publicID := block.ID
		if publicID == "" {
			publicID = block.NodeID
		}
		if publicID == "" {
			return nil, fmt.Errorf("%w: block index %d", ErrEmptyBlockID, i)
		}
		if _, ok := seenIDs[publicID]; ok {
			return nil, fmt.Errorf("%w: %q", ErrDuplicateBlockID, publicID)
		}
		seenIDs[publicID] = struct{}{}
		e.public[i] = publicID
		e.tieIDs[i] = block.nodeID()
		if e.tieIDs[i] == "" {
			e.tieIDs[i] = publicID
		}

		r := block.Rect
		if math.IsNaN(r.X) || math.IsNaN(r.Y) || math.IsNaN(r.Width) || math.IsNaN(r.Height) ||
			math.IsInf(r.X, 0) || math.IsInf(r.Y, 0) || math.IsInf(r.Width, 0) || math.IsInf(r.Height, 0) ||
			r.Width < 0 || r.Height < 0 {
			return nil, fmt.Errorf("%w: block %q", ErrInvalidRect, publicID)
		}

		if block.DOMOrder != 0 {
			if _, ok := e.orders[publicID]; !ok {
				e.orders[publicID] = block.DOMOrder
			}
			if _, ok := e.orders[e.tieIDs[i]]; !ok {
				e.orders[e.tieIDs[i]] = block.DOMOrder
			}
		}
	}

	for id, order := range req.NodeOrder {
		e.orders[id] = order
	}

	for i := range lines {
		if lines[i].ID == "" {
			lines[i].ID = fmt.Sprintf("candidate-line-%d", i+1)
		}
		if math.IsNaN(lines[i].X) || math.IsInf(lines[i].X, 0) {
			return nil, fmt.Errorf("taskpkg: candidate line %q has invalid x", lines[i].ID)
		}
	}
	lineIDs := make(map[string]struct{}, len(lines))
	for _, line := range lines {
		if _, ok := lineIDs[line.ID]; ok {
			return nil, fmt.Errorf("%w: %q", ErrDuplicateLineID, line.ID)
		}
		lineIDs[line.ID] = struct{}{}
	}
	e.lines = lines

	for i, block := range e.blocks {
		path, err := e.buildPath(i, block)
		if err != nil {
			return nil, err
		}
		e.paths[i] = path
	}

	return e, nil
}

func (e *engine) buildPath(blockIndex int, block Block) ([]string, error) {
	id := e.public[blockIndex]
	tieID := e.tieIDs[blockIndex]

	var ancestors []string
	switch {
	case len(block.Ancestors) > 0:
		ancestors = append([]string(nil), block.Ancestors...)
	case len(e.req.AncestorPaths[id]) > 0:
		ancestors = append([]string(nil), e.req.AncestorPaths[id]...)
	case len(e.req.AncestorPaths[tieID]) > 0:
		ancestors = append([]string(nil), e.req.AncestorPaths[tieID]...)
	case len(e.req.Ancestors[id]) > 0:
		ancestors = append([]string(nil), e.req.Ancestors[id]...)
	case len(e.req.Ancestors[tieID]) > 0:
		ancestors = append([]string(nil), e.req.Ancestors[tieID]...)
	default:
		start := id
		if _, ok := e.req.parentOf(id); !ok {
			if _, tieOK := e.req.parentOf(tieID); tieOK {
				start = tieID
			}
		}
		path := []string{start}
		seen := map[string]struct{}{start: {}}
		for {
			parent, ok := e.req.parentOf(path[len(path)-1])
			if !ok || parent == "" {
				break
			}
			if _, cycle := seen[parent]; cycle {
				return nil, fmt.Errorf("%w near %q", ErrInvalidParent, parent)
			}
			seen[parent] = struct{}{}
			path = append(path, parent)
		}
		for left, right := 0, len(path)-1; left < right; left, right = left+1, right-1 {
			path[left], path[right] = path[right], path[left]
		}
		ancestors = path
	}

	if len(ancestors) == 0 || ancestors[len(ancestors)-1] != id && ancestors[len(ancestors)-1] != tieID {
		ancestors = append(ancestors, id)
	}
	return ancestors, nil
}

func (req Request) parentOf(id string) (string, bool) {
	if parent, ok := req.Parents[id]; ok {
		return parent, true
	}
	parent, ok := req.Parent[id]
	return parent, ok
}

func (e *engine) enumerate() []evaluatedPartition {
	n := len(e.lines)
	total := 1 << n
	out := make([]evaluatedPartition, 0, total)

	for mask := 0; mask < total; mask++ {
		ep := evaluatedPartition{
			partition: Partition{Mask: mask},
			crossings: make(map[int][]int),
		}

		lineIDs := make([]string, 0, n)
		for li, line := range e.lines {
			if mask&(1<<li) == 0 {
				continue
			}
			ep.lineIdx = append(ep.lineIdx, li)
			lineIDs = append(lineIDs, line.ID)

			for bi, block := range e.blocks {
				if lineCrossesRect(block.Rect, line.X) {
					ep.crossings[bi] = append(ep.crossings[bi], li)
					ep.partition.CrossingCount++
				}
			}
		}
		ep.partition.SelectedLineIDs = lineIDs

		crossed := make([]int, 0, len(ep.crossings))
		for bi := range ep.crossings {
			crossed = append(crossed, bi)
		}
		sort.Ints(crossed)
		for _, bi := range crossed {
			ep.partition.CrossedBlocks = append(ep.partition.CrossedBlocks, e.public[bi])
			if e.blocks[bi].Heading {
				ep.partition.CrossedHeadingCount++
			} else {
				ep.partition.CrossedNonHeadingCount++
			}
		}

		out = append(out, ep)
	}
	return out
}

func lineCrossesRect(r Rect, x float64) bool {
	return x > r.Left() && x < r.Right()
}

func chooseBestPartition(all []evaluatedPartition) evaluatedPartition {
	best := all[0]
	for _, candidate := range all[1:] {
		if betterPartition(candidate.partition, best.partition) {
			best = candidate
		}
	}
	return best
}

func betterPartition(candidate, best Partition) bool {
	if candidate.CrossedNonHeadingCount != best.CrossedNonHeadingCount {
		return candidate.CrossedNonHeadingCount < best.CrossedNonHeadingCount
	}
	// When the explicit objective is tied, prefer a maximal set of clean
	// dividers so a genuinely multi-column page does not collapse to one
	// column merely because the empty subset also has zero crossings.
	if len(candidate.SelectedLineIDs) != len(best.SelectedLineIDs) {
		return len(candidate.SelectedLineIDs) > len(best.SelectedLineIDs)
	}
	if candidate.CrossingCount != best.CrossingCount {
		return candidate.CrossingCount < best.CrossingCount
	}
	candidateKeys := append([]string(nil), candidate.SelectedLineIDs...)
	bestKeys := append([]string(nil), best.SelectedLineIDs...)
	sort.Strings(candidateKeys)
	sort.Strings(bestKeys)
	return compareStringSlices(candidateKeys, bestKeys) < 0
}

func compareStringSlices(a, b []string) int {
	n := len(a)
	if len(b) < n {
		n = len(b)
	}
	for i := 0; i < n; i++ {
		if c := strings.Compare(a[i], b[i]); c != 0 {
			return c
		}
	}
	switch {
	case len(a) < len(b):
		return -1
	case len(a) > len(b):
		return 1
	default:
		return 0
	}
}

func cmpFloat(a, b float64) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	default:
		return 0
	}
}
