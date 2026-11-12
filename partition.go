package taskpkg

import (
	"fmt"
	"math"
	"sort"
	"strconv"
)

const (
	lineSeparator = "\x1f"
	idSeparator   = "\x1e"
)

type dpEntry struct {
	cost     float64
	key      string
	lineMask uint32
}

type solver struct {
	words      []wordRecord
	baseline   []float64
	left       []float64
	right      []float64
	width      []float64
	allowed    [][]bool
	distance2  [][]float64
	penalty    float64
	memo       map[uint32]dpEntry
	candidates int
}

func partition(records []wordRecord, opts Options) ([]wordRecord, []uint32, float64, int) {
	ordered := append([]wordRecord(nil), records...)
	sort.SliceStable(ordered, func(i, j int) bool {
		ci := ordered[i].bounds.CenterX()
		cj := ordered[j].bounds.CenterX()
		if ci != cj {
			return ci < cj
		}
		if ordered[i].baseline != ordered[j].baseline {
			return ordered[i].baseline < ordered[j].baseline
		}
		if ordered[i].ID != ordered[j].ID {
			return ordered[i].ID < ordered[j].ID
		}
		return ordered[i].source < ordered[j].source
	})

	s := newSolver(ordered, opts)
	all := uint32(1<<len(ordered)) - 1
	best := s.solve(all)
	return ordered, s.reconstruct(all), best.cost, s.candidates
}

func newSolver(records []wordRecord, opts Options) *solver {
	n := len(records)
	s := &solver{
		words:     records,
		baseline:  make([]float64, n),
		left:      make([]float64, n),
		right:     make([]float64, n),
		width:     make([]float64, n),
		allowed:   make([][]bool, n),
		distance2: make([][]float64, n),
		penalty:   opts.LinePenalty,
		memo:      make(map[uint32]dpEntry),
	}
	for i, r := range records {
		s.baseline[i] = r.baseline
		s.left[i] = r.left
		s.right[i] = r.right
		s.width[i] = r.width
		s.allowed[i] = make([]bool, n)
		s.distance2[i] = make([]float64, n)
	}

	for i := 0; i < n; i++ {
		for j := i + 1; j < n; j++ {
			dy := s.baseline[j] - s.baseline[i]
			s.distance2[i][j] = dy * dy
			s.allowed[i][j] = s.canFollow(i, j, opts)
		}
	}
	return s
}

func (s *solver) canFollow(i, j int, opts Options) bool {
	if math.Abs(s.baseline[j]-s.baseline[i]) > opts.BaselineThreshold+geometryEpsilon {
		return false
	}

	overlap := math.Min(s.right[i], s.right[j]) - math.Max(s.left[i], s.left[j])
	if overlap < 0 {
		overlap = 0
	}
	gap := math.Max(s.left[i], s.left[j]) - math.Min(s.right[i], s.right[j])
	if gap < 0 {
		gap = 0
	}
	shortSide := math.Min(s.width[i], s.width[j])
	if opts.MaxOverlapRatio >= 0 &&
		overlap > opts.MaxOverlapRatio*shortSide+geometryEpsilon {
		return false
	}
	if opts.MaxGapRatio >= 0 &&
		gap > opts.MaxGapRatio*shortSide+geometryEpsilon {
		return false
	}
	return true
}

func (s *solver) solve(mask uint32) dpEntry {
	if mask == 0 {
		return dpEntry{cost: 0, key: "", lineMask: 0}
	}
	if entry, ok := s.memo[mask]; ok {
		return entry
	}

	anchor := trailingBit(mask)
	anchorBit := uint32(1) << uint(anchor)
	best := dpEntry{cost: math.Inf(1)}

	var enumerate func(next, last int, subset uint32, lineCost float64)
	enumerate = func(next, last int, subset uint32, lineCost float64) {
		// Extend the current line first. Following the lowest-index
		// compatible words immediately produces a full-line upper bound,
		// which makes the later branch-and-bound pruning effective.
		for j := next; j < len(s.words); j++ {
			bit := uint32(1) << uint(j)
			if mask&bit == 0 || !s.allowed[last][j] {
				continue
			}
			newSubset := subset | bit
			newCost := lineCost + s.distance2[last][j]
			newRemaining := mask &^ newSubset
			if newCost+s.lowerBound(newRemaining) > best.cost+costEpsilon(best.cost) {
				continue
			}
			enumerate(j+1, j, newSubset, newCost)
		}

		remaining := mask &^ subset
		if lineCost+s.lowerBound(remaining) > best.cost+costEpsilon(best.cost) {
			return
		}
		sub := s.solve(remaining)
		key := s.lineKey(subset)
		if remaining != 0 {
			key += lineSeparator + sub.key
		}
		total := lineCost + sub.cost
		s.candidates++
		if betterPartition(total, key, best) {
			best = dpEntry{cost: total, key: key, lineMask: subset}
		}
	}

	enumerate(anchor+1, anchor, anchorBit, s.penalty)
	s.memo[mask] = best
	return best
}

func (s *solver) lowerBound(mask uint32) float64 {
	if mask == 0 {
		return 0
	}
	// Every non-empty remainder needs at least one line.
	return s.penalty
}

func trailingBit(mask uint32) int {
	i := 0
	for mask&1 == 0 {
		mask >>= 1
		i++
	}
	return i
}

func betterPartition(cost float64, key string, best dpEntry) bool {
	if math.IsInf(best.cost, 1) {
		return true
	}
	eps := costEpsilon(best.cost)
	if cost < best.cost-eps {
		return true
	}
	if math.Abs(cost-best.cost) <= eps && key < best.key {
		return true
	}
	return false
}

func costEpsilon(v float64) float64 {
	return geometryEpsilon * math.Max(1, math.Abs(v))
}

func (s *solver) lineKey(mask uint32) string {
	ids := make([]string, 0, trailingBitCount(mask))
	for i := 0; i < len(s.words); i++ {
		bit := uint32(1) << uint(i)
		if mask&bit == 0 {
			continue
		}
		id := s.words[i].ID
		if id == "" {
			id = "#" + strconv.Itoa(s.words[i].source)
		}
		ids = append(ids, id)
	}
	// A fixed-width group length first makes lexicographic comparison of
	// encoded groups agree with lexicographic comparison of the group lists
	// themselves, including the "shorter list first" rule.
	key := fmt.Sprintf("%02d", len(ids))
	for _, id := range ids {
		key += idSeparator + strconv.Quote(id)
	}
	return key
}

func (s *solver) reconstruct(mask uint32) []uint32 {
	if mask == 0 {
		return nil
	}
	entry := s.memo[mask]
	rest := mask &^ entry.lineMask
	lines := make([]uint32, 1, 1+trailingBitCount(rest))
	lines[0] = entry.lineMask
	for rest != 0 {
		sub := s.memo[rest]
		lines = append(lines, sub.lineMask)
		rest &^= sub.lineMask
	}
	return lines
}

func trailingBitCount(mask uint32) int {
	count := 0
	for mask != 0 {
		mask &= mask - 1
		count++
	}
	return count
}

func makeLine(records []wordRecord, mask uint32, penalty float64) Line {
	words := make([]Word, 0, trailingBitCount(mask))
	baselines := make([]float64, 0, trailingBitCount(mask))
	lineCost := penalty
	last := -1
	for i := 0; i < len(records); i++ {
		bit := uint32(1) << uint(i)
		if mask&bit == 0 {
			continue
		}
		words = append(words, records[i].Word)
		baselines = append(baselines, records[i].baseline)
		if last >= 0 {
			dy := records[i].baseline - records[last].baseline
			lineCost += dy * dy
		}
		last = i
	}
	meanBaseline := 0.0
	if len(baselines) > 0 {
		for _, b := range baselines {
			meanBaseline += b
		}
		meanBaseline /= float64(len(baselines))
	}
	return Line{
		Words:    words,
		Baseline: meanBaseline,
		Cost:     lineCost,
	}
}

func sortLines(lines []Line) {
	sort.SliceStable(lines, func(i, j int) bool {
		if lines[i].Baseline != lines[j].Baseline {
			return lines[i].Baseline < lines[j].Baseline
		}
		xi, xj := lineMinX(lines[i]), lineMinX(lines[j])
		if xi != xj {
			return xi < xj
		}
		return lines[i].Text() < lines[j].Text()
	})
}

func lineMinX(line Line) float64 {
	if len(line.Words) == 0 {
		return math.Inf(1)
	}
	minX := line.Words[0].Quad.Bounds().MinX
	for _, w := range line.Words[1:] {
		minX = math.Min(minX, w.Quad.Bounds().MinX)
	}
	return minX
}
