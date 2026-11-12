package taskpkg

import (
	"math"
	"sort"
)

func deduplicate(records []wordRecord, opts Options) ([]wordRecord, []DuplicateGroup, error) {
	if len(records) < 2 || opts.DuplicateCoverage < 0 {
		return records, nil, nil
	}

	parent := make([]int, len(records))
	for i := range parent {
		parent[i] = i
	}
	var find func(int) int
	find = func(x int) int {
		if parent[x] != x {
			parent[x] = find(parent[x])
		}
		return parent[x]
	}
	union := func(a, b int) {
		ra, rb := find(a), find(b)
		if ra != rb {
			parent[rb] = ra
		}
	}

	threshold := opts.DuplicateCoverage
	for i := 0; i < len(records); i++ {
		for j := i + 1; j < len(records); j++ {
			if samePosition(records[i], records[j], threshold) {
				union(i, j)
			}
		}
	}

	groupsByRoot := make(map[int][]wordRecord)
	for i, r := range records {
		root := find(i)
		groupsByRoot[root] = append(groupsByRoot[root], r)
	}

	selected := make([]wordRecord, 0, len(groupsByRoot))
	var evidence []DuplicateGroup
	for _, group := range groupsByRoot {
		sort.SliceStable(group, func(i, j int) bool {
			if group[i].Confidence != group[j].Confidence {
				return group[i].Confidence > group[j].Confidence
			}
			if group[i].ID != group[j].ID {
				return group[i].ID < group[j].ID
			}
			return group[i].source < group[j].source
		})
		best := group[0]
		selected = append(selected, best)
		if len(group) > 1 {
			candidates := make([]Word, len(group))
			for i, r := range group {
				candidates[i] = r.Word
			}
			evidence = append(evidence, DuplicateGroup{
				Selected:   best.Word,
				Candidates: candidates,
				Bounds:     best.bounds,
			})
		}
	}

	sort.Slice(selected, func(i, j int) bool {
		return selected[i].source < selected[j].source
	})
	sort.Slice(evidence, func(i, j int) bool {
		if evidence[i].Selected.ID != evidence[j].Selected.ID {
			return evidence[i].Selected.ID < evidence[j].Selected.ID
		}
		return evidence[i].Bounds.MinX < evidence[j].Bounds.MinX
	})
	return selected, evidence, nil
}

func samePosition(a, b wordRecord, threshold float64) bool {
	inter := intersectionArea(a.bounds, b.bounds)
	areaA, areaB := boxArea(a.bounds), boxArea(b.bounds)
	minArea := math.Min(areaA, areaB)
	if minArea <= geometryEpsilon {
		dx := a.bounds.CenterX() - b.bounds.CenterX()
		if a.bounds.Width() == 0 && b.bounds.Width() == 0 {
			dx = a.bounds.MinX - b.bounds.MinX
		}
		dy := ((a.bounds.MinY+a.bounds.MaxY)/2 - (b.bounds.MinY+b.bounds.MaxY)/2)
		return math.Hypot(dx, dy) <= geometryEpsilon
	}
	return inter/minArea >= threshold
}
