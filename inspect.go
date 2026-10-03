package hexgrid

import (
	"fmt"
	"sort"
)

// FindingKind classifies an integrity problem discovered by Inspect.
type FindingKind int

const (
	// FindingDanglingRef marks a cell whose required ancestor is
	// missing from the snapshot.
	FindingDanglingRef FindingKind = iota
	// FindingOrphanLoop marks a connected component of the neighbor
	// graph that is detached from the main component (an isolated
	// loop of cells with no path to the rest of the state).
	FindingOrphanLoop
	// FindingDataSkew marks an aggregation bucket (parent cell or
	// face) whose payload weight abnormally exceeds the mean.
	FindingDataSkew
)

func (k FindingKind) String() string {
	switch k {
	case FindingDanglingRef:
		return "dangling-ref"
	case FindingOrphanLoop:
		return "orphan-loop"
	case FindingDataSkew:
		return "data-skew"
	default:
		return "unknown"
	}
}

// Finding is a single integrity violation.
type Finding struct {
	Kind   FindingKind
	Cell   Cell
	Detail string
}

// Snapshot is an offline copy of the hierarchical state to inspect.
//
// Levels maps a resolution to the set of cells stored at that
// resolution. Weights optionally attaches a payload weight to cells
// at the finest level; cells without an entry weigh 1.
type Snapshot struct {
	Levels  map[int]map[Cell]struct{}
	Weights map[Cell]int64
}

// NewSnapshot builds a Snapshot from per-resolution cell lists.
func NewSnapshot(levels map[int][]Cell) Snapshot {
	s := Snapshot{Levels: make(map[int]map[Cell]struct{}, len(levels))}
	for res, cells := range levels {
		set := make(map[Cell]struct{}, len(cells))
		for _, c := range cells {
			set[c] = struct{}{}
		}
		s.Levels[res] = set
	}
	return s
}

// SkewStats summarizes payload distribution across aggregation buckets.
type SkewStats struct {
	Buckets   int
	Mean      float64
	Max       int64
	MaxBucket Cell
	Min       int64
	MinBucket Cell
}

// Report is the outcome of an Inspect run.
type Report struct {
	Findings         []Finding
	Components       int // connected components of the finest level
	LargestComponent int
	Skew             SkewStats
}

// OK reports whether the inspection found no violations.
func (r Report) OK() bool { return len(r.Findings) == 0 }

// FindingsOfKind filters the report's findings by kind.
func (r Report) FindingsOfKind(k FindingKind) []Finding {
	var out []Finding
	for _, f := range r.Findings {
		if f.Kind == k {
			out = append(out, f)
		}
	}
	return out
}

// InspectOptions tunes the inspector.
type InspectOptions struct {
	// SkewThreshold is the max/mean ratio above which a bucket is
	// flagged as skewed. Zero selects the default of 3.
	SkewThreshold float64
}

// Inspect runs the full offline integrity sweep over a snapshot:
// dangling-reference detection across adjacent stored levels,
// orphan-loop detection on the finest level's neighbor graph, and
// data-skew analysis of payload weights aggregated per parent (or
// per face when only one level is stored).
func Inspect(s Snapshot, opts InspectOptions) Report {
	threshold := opts.SkewThreshold
	if threshold <= 0 {
		threshold = 3
	}
	var rep Report

	resolutions := make([]int, 0, len(s.Levels))
	for res := range s.Levels {
		resolutions = append(resolutions, res)
	}
	sort.Ints(resolutions)
	if len(resolutions) == 0 {
		return rep
	}

	// 1. Dangling references: every cell must find its ancestor at
	// each coarser stored level.
	for idx := 1; idx < len(resolutions); idx++ {
		coarse, fine := resolutions[idx-1], resolutions[idx]
		coarseSet := s.Levels[coarse]
		for c := range s.Levels[fine] {
			anc, err := c.ParentTo(coarse)
			if err != nil {
				rep.Findings = append(rep.Findings, Finding{
					Kind: FindingDanglingRef, Cell: c,
					Detail: fmt.Sprintf("cannot derive ancestor at res %d: %v", coarse, err),
				})
				continue
			}
			if _, ok := coarseSet[anc]; !ok {
				rep.Findings = append(rep.Findings, Finding{
					Kind: FindingDanglingRef, Cell: c,
					Detail: fmt.Sprintf("ancestor %s at res %d is missing", anc, coarse),
				})
			}
		}
	}

	// 2. Orphan loops: connected components of the neighbor graph on
	// the finest stored level.
	finest := resolutions[len(resolutions)-1]
	fineSet := s.Levels[finest]
	parent := make(map[Cell]Cell, len(fineSet))
	for c := range fineSet {
		parent[c] = c
	}
	var find func(Cell) Cell
	find = func(c Cell) Cell {
		for parent[c] != c {
			parent[c] = parent[parent[c]]
			c = parent[c]
		}
		return c
	}
	union := func(a, b Cell) {
		ra, rb := find(a), find(b)
		if ra != rb {
			parent[ra] = rb
		}
	}
	for c := range fineSet {
		for _, nc := range Neighbors(c) {
			if _, ok := fineSet[nc]; ok {
				union(c, nc)
			}
		}
	}
	compSize := make(map[Cell]int)
	for c := range fineSet {
		compSize[find(c)]++
	}
	rep.Components = len(compSize)
	if len(compSize) > 1 {
		// The largest component is the main body; every other
		// component is an isolated loop.
		var mainRoot Cell
		mainSize := -1
		for root, size := range compSize {
			if size > mainSize {
				mainRoot, mainSize = root, size
			}
		}
		rep.LargestComponent = mainSize
		roots := make([]Cell, 0, len(compSize)-1)
		for root, size := range compSize {
			if root != mainRoot {
				roots = append(roots, root)
				_ = size
			}
		}
		sort.Slice(roots, func(a, b int) bool { return roots[a] < roots[b] })
		for _, root := range roots {
			rep.Findings = append(rep.Findings, Finding{
				Kind: FindingOrphanLoop, Cell: root,
				Detail: fmt.Sprintf("isolated component of %d cells at res %d detached from main component of %d cells",
					compSize[root], finest, mainSize),
			})
		}
	} else if len(compSize) == 1 {
		for _, size := range compSize {
			rep.LargestComponent = size
		}
	}

	// 3. Data skew: aggregate finest-level weights into buckets.
	// With multiple levels, bucket by parent at the next coarser
	// level; with a single level, bucket by face (reported as the
	// res-0 root cell of that face).
	bucketOf := func(c Cell) Cell {
		if len(resolutions) > 1 {
			anc, err := c.ParentTo(resolutions[len(resolutions)-2])
			if err == nil {
				return anc
			}
		}
		return newCell(c.Face(), 0, 0, 0)
	}
	weightOf := func(c Cell) int64 {
		if s.Weights == nil {
			return 1
		}
		if w, ok := s.Weights[c]; ok {
			return w
		}
		return 1
	}
	buckets := make(map[Cell]int64)
	for c := range fineSet {
		buckets[bucketOf(c)] += weightOf(c)
	}
	if len(buckets) > 0 {
		var total, maxW, minW int64
		var maxB, minB Cell
		first := true
		for b, w := range buckets {
			total += w
			if first || w > maxW {
				maxW, maxB = w, b
			}
			if first || w < minW {
				minW, minB = w, b
			}
			first = false
		}
		mean := float64(total) / float64(len(buckets))
		rep.Skew = SkewStats{
			Buckets: len(buckets), Mean: mean,
			Max: maxW, MaxBucket: maxB, Min: minW, MinBucket: minB,
		}
		if float64(maxW) > threshold*mean {
			hot := make([]Cell, 0)
			for b, w := range buckets {
				if float64(w) > threshold*mean {
					hot = append(hot, b)
				}
			}
			sort.Slice(hot, func(a, b int) bool { return hot[a] < hot[b] })
			for _, b := range hot {
				rep.Findings = append(rep.Findings, Finding{
					Kind: FindingDataSkew, Cell: b,
					Detail: fmt.Sprintf("bucket weight %d exceeds %.1fx the mean %.2f", buckets[b], threshold, mean),
				})
			}
		}
	}
	return rep
}

// UniverseReport summarizes VerifyUniverse.
type UniverseReport struct {
	Resolution    int
	Cells         int
	Reachable     int
	Unreachable   int
	Duplicates    int
	Invalid       int
	BadParentRefs int
	Findings      []Finding
}

// OK reports whether the universe passed every structural invariant.
func (r UniverseReport) OK() bool {
	return r.Duplicates == 0 && r.Invalid == 0 && r.BadParentRefs == 0
}

// VerifyUniverse exhaustively checks the structural invariants of the
// full cell universe at the given resolution: uniqueness, validity,
// exact aperture-4 parent coverage, and reachability (every cell's
// center re-encodes to itself). Intended for offline audits.
func VerifyUniverse(res int) (UniverseReport, error) {
	cells, err := Universe(res)
	if err != nil {
		return UniverseReport{}, err
	}
	rep := UniverseReport{Resolution: res, Cells: len(cells)}
	seen := make(map[Cell]struct{}, len(cells))
	var parentCount map[Cell]int
	if res > 0 {
		parentCount = make(map[Cell]int)
	}
	for _, c := range cells {
		if _, dup := seen[c]; dup {
			rep.Duplicates++
			rep.Findings = append(rep.Findings, Finding{Kind: FindingDanglingRef, Cell: c, Detail: "duplicate cell in universe"})
		}
		seen[c] = struct{}{}
		if !c.IsValid() {
			rep.Invalid++
			continue
		}
		if c.IsReachable() {
			rep.Reachable++
		} else {
			rep.Unreachable++
		}
		if res > 0 {
			p, _ := c.Parent()
			parentCount[p]++
		}
	}
	for p, n := range parentCount {
		if n != 4 {
			rep.BadParentRefs++
			rep.Findings = append(rep.Findings, Finding{
				Kind: FindingDanglingRef, Cell: p,
				Detail: fmt.Sprintf("parent has %d children in universe, want 4", n),
			})
		}
	}
	return rep, nil
}
