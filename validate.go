package contour

import "fmt"

// Validate cross-checks the extracted loops against two independent models:
// direct unit-edge counting on the grid, and the flood-fill background
// model. It returns one human-readable entry per discrepancy; an empty
// result means all models agree.
//
// Checks performed:
//  1. Replay: the loops replay to exactly the directed boundary edge set.
//  2. Perimeter: per region, summed loop length equals unit-edge counting.
//  3. Area: per region, outer minus hole shoelace area equals pixel count.
//  4. Holes: every hole loop encloses an interior background component of
//     the flood-fill model adjacent to the loop's region, every interior
//     component is enclosed by at least one hole loop, and per-region hole
//     counts agree between the two models.
func (r *Result) Validate() []string {
	var problems []string
	im := r.Image

	// 1. Replay: multiset of directed edges from loops vs. freshly
	// generated boundary edges.
	want := make(map[Edge]int)
	edges, _ := boundaryEdges(im)
	for _, e := range edges {
		want[Edge{e.from, e.to}]++
	}
	got := make(map[Edge]int)
	for _, l := range r.Loops {
		for _, e := range l.Edges() {
			got[e]++
		}
	}
	for e, n := range want {
		if got[e] != n {
			problems = append(problems, fmt.Sprintf("replay: edge %v->%v counted %d times, loops yield %d", e.From, e.To, n, got[e]))
		}
	}
	for e, n := range got {
		if want[e] == 0 {
			problems = append(problems, fmt.Sprintf("replay: loops yield phantom edge %v->%v (%d)", e.From, e.To, n))
		}
	}

	// 2+3. Per-region perimeter and area from loops.
	loopLen := make([]int, len(r.Regions))
	loopArea2 := make([]int, len(r.Regions))
	for _, l := range r.Loops {
		a2 := l.SignedArea2()
		switch l.Kind {
		case Outer:
			if a2 >= 0 {
				problems = append(problems, fmt.Sprintf("area: outer loop of region %d has non-negative signed area", l.Region))
			}
			loopLen[l.Region] += l.Len()
			loopArea2[l.Region] += -a2
		case Hole:
			if a2 <= 0 {
				problems = append(problems, fmt.Sprintf("area: hole loop of region %d has non-positive signed area", l.Region))
			}
			loopLen[l.Region] += l.Len()
			loopArea2[l.Region] -= a2
		}
	}
	for _, reg := range r.Regions {
		if loopLen[reg.ID] != reg.Perimeter {
			problems = append(problems, fmt.Sprintf("perimeter: region %d loops total %d, unit-edge count %d", reg.ID, loopLen[reg.ID], reg.Perimeter))
		}
		if loopArea2[reg.ID] != 2*reg.Area {
			problems = append(problems, fmt.Sprintf("area: region %d loops enclose %d cells, pixel count %d", reg.ID, loopArea2[reg.ID]/2, reg.Area))
		}
	}

	// 4. Hole loops vs. flood-fill background model.
	enclosed := make(map[int]int) // background component ID -> enclosing hole loops
	for _, l := range r.Loops {
		if l.Kind != Hole {
			continue
		}
		e := l.Edges()[0]
		d := edgeDir(e)
		c := rightCell(e.From, d)
		if !inBounds(im, c) {
			problems = append(problems, fmt.Sprintf("holes: hole loop of region %d has no cell on its right", l.Region))
			continue
		}
		cid := r.bgLabels[c.Y*im.W+c.X]
		if cid < 0 {
			problems = append(problems, fmt.Sprintf("holes: hole loop of region %d encloses foreground at %v", l.Region, c))
			continue
		}
		comp := r.Background[cid]
		if comp.Exterior {
			problems = append(problems, fmt.Sprintf("holes: hole loop of region %d encloses exterior component %d", l.Region, cid))
		}
		if !containsInt(comp.Regions, l.Region) {
			problems = append(problems, fmt.Sprintf("holes: component %d is not adjacent to its enclosing region %d", cid, l.Region))
		}
		enclosed[cid]++
	}
	// Interior components adjacent to each region, per the flood-fill model.
	adjacent := make([]int, len(r.Regions))
	for _, comp := range r.Background {
		if comp.Exterior {
			continue
		}
		for _, rid := range comp.Regions {
			adjacent[rid]++
		}
	}
	for _, comp := range r.Background {
		if !comp.Exterior && enclosed[comp.ID] == 0 {
			problems = append(problems, fmt.Sprintf("holes: interior component %d (%d cells, regions %v) is enclosed by no loop", comp.ID, comp.Area, comp.Regions))
		}
	}
	for _, reg := range r.Regions {
		if adjacent[reg.ID] != reg.Holes {
			problems = append(problems, fmt.Sprintf("holes: region %d has %d hole loops but %d adjacent interior components", reg.ID, reg.Holes, adjacent[reg.ID]))
		}
	}
	return problems
}

func edgeDir(e Edge) int {
	dx, dy := e.To.X-e.From.X, e.To.Y-e.From.Y
	switch {
	case dx == 1:
		return dirE
	case dx == -1:
		return dirW
	case dy == 1:
		return dirS
	default:
		return dirN
	}
}

func inBounds(im *Image, p Point) bool {
	return p.X >= 0 && p.Y >= 0 && p.X < im.W && p.Y < im.H
}

func containsInt(s []int, v int) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}
