package netsec

import (
	"fmt"
	"sort"
)

// rect is an axis-aligned hyper-rectangle over index domains. Each entry is a
// set of indices for one axis; a point is inside when every axis index is in
// the corresponding set.
type rect struct {
	sets [][]int
}

func newRect(dims int) rect {
	r := rect{sets: make([][]int, dims)}
	for dim := range r.sets {
		r.sets[dim] = []int{}
	}
	return r
}

func fullRect(u *Universe) rect {
	r := newRect(u.Dims())
	for dim, ax := range u.axes {
		r.sets[dim] = make([]int, len(ax.values))
		for i := range ax.values {
			r.sets[dim][i] = i
		}
	}
	return r
}

func (r rect) clone() rect {
	out := newRect(len(r.sets))
	for dim, set := range r.sets {
		out.sets[dim] = append(out.sets[dim], set...)
	}
	return out
}

func (r rect) empty() bool {
	for _, set := range r.sets {
		if len(set) == 0 {
			return true
		}
	}
	return false
}

func (r rect) volume() int {
	v := 1
	for _, set := range r.sets {
		v *= len(set)
	}
	return v
}

// containsPoint reports whether the index tuple lies in the rectangle.
func (r rect) containsPoint(p []int) bool {
	for dim, idx := range p {
		if !containsInt(r.sets[dim], idx) {
			return false
		}
	}
	return true
}

func containsInt(set []int, v int) bool {
	for _, item := range set {
		if item == v {
			return true
		}
	}
	return false
}

// intersect returns the overlap of a and b, or an empty rectangle.
func intersect(a, b rect) rect {
	out := newRect(len(a.sets))
	for dim := range a.sets {
		for _, v := range a.sets[dim] {
			if containsInt(b.sets[dim], v) {
				out.sets[dim] = append(out.sets[dim], v)
			}
		}
		if len(out.sets[dim]) == 0 {
			return newRect(len(a.sets))
		}
	}
	return out
}

// subtract returns the disjoint pieces of base that lie outside cut.
func subtract(base, cut rect) []rect {
	if len(base.sets) != len(cut.sets) {
		panic(fmt.Sprintf("netsec: rectangle dimension mismatch %d vs %d", len(base.sets), len(cut.sets)))
	}
	overlap := intersect(base, cut)
	if overlap.empty() {
		return []rect{base.clone()}
	}

	var pieces []rect
	cur := base.clone()
	for dim := range base.sets {
		var outside []int
		for _, v := range cur.sets[dim] {
			if !containsInt(overlap.sets[dim], v) {
				outside = append(outside, v)
			}
		}
		if len(outside) > 0 {
			piece := cur.clone()
			piece.sets[dim] = outside
			pieces = append(pieces, piece)
			cur.sets[dim] = append([]int(nil), overlap.sets[dim]...)
		}
	}
	return pieces
}

// subtractMany returns base with every cut removed, as disjoint rectangles.
func subtractMany(base rect, cuts []rect) []rect {
	pieces := []rect{base.clone()}
	for _, cut := range cuts {
		var next []rect
		for _, piece := range pieces {
			next = append(next, subtract(piece, cut)...)
		}
		pieces = next
		if len(pieces) == 0 {
			return nil
		}
	}
	return pieces
}

// equalSet reports whether two index sets hold the same elements.
func equalSet(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for _, v := range a {
		if !containsInt(b, v) {
			return false
		}
	}
	return true
}

// mergeRects greedily combines rectangles whose signatures match and which
// align on every axis but one, iterating until no further merge is possible.
// keyFunc groups rectangles that may only be merged with each other; every
// output rectangle stays inside its group, so attached annotations remain
// valid. At most one absorption happens per pass, which keeps the working
// indices consistent and guarantees the output stays pairwise disjoint.
func mergeRects(rs []rect, keyFunc func(rect) string) []rect {
	work := make([]rect, len(rs))
	copy(work, rs)

	for {
		merged := false
		groups := make(map[string]map[string][]int)
		for i, r := range work {
			key := keyFunc(r)
			axisGroups, ok := groups[key]
			if !ok {
				axisGroups = make(map[string][]int)
				groups[key] = axisGroups
			}
			for dim := range r.sets {
				axisGroups[mergeKey(r, dim)] = append(axisGroups[mergeKey(r, dim)], i)
			}
		}

		for _, axisGroups := range groups {
			for _, indices := range axisGroups {
				if len(indices) < 2 {
					continue
				}
				dst, src := indices[0], indices[1]
				for dim := range work[dst].sets {
					for _, v := range work[src].sets[dim] {
						if !containsInt(work[dst].sets[dim], v) {
							work[dst].sets[dim] = append(work[dst].sets[dim], v)
						}
					}
				}
				work = append(work[:src], work[src+1:]...)
				merged = true
				break
			}
			if merged {
				break
			}
		}
		if !merged {
			return work
		}
	}
}

// mergeKey identifies rectangles that share the same signature and are aligned
// on every axis except skip: those can be merged along skip.
func mergeKey(r rect, skip int) string {
	key := ""
	for dim, set := range r.sets {
		if dim == skip {
			continue
		}
		ordered := append([]int(nil), set...)
		sort.Ints(ordered)
		key += fmt.Sprintf("%d:%v|", dim, ordered)
	}
	return key
}

// Rect is the exported, value-based view of an axis-aligned rectangle.
type Rect struct {
	// Values holds the value set of each axis, in universe axis order.
	Values [][]string
}

func (u *Universe) toNamed(r rect) Rect {
	out := Rect{Values: make([][]string, len(r.sets))}
	for dim, set := range r.sets {
		names := make([]string, 0, len(set))
		for _, idx := range set {
			names = append(names, u.axes[dim].values[idx])
		}
		out.Values[dim] = names
	}
	return out
}

func (u *Universe) toIndex(r Rect) (rect, error) {
	if len(r.Values) != u.Dims() {
		return rect{}, fmt.Errorf("netsec: rectangle has %d axes, universe has %d", len(r.Values), u.Dims())
	}
	out := newRect(u.Dims())
	for dim, values := range r.Values {
		seen := make(map[int]bool)
		for _, value := range values {
			idx, ok := u.axes[dim].index[value]
			if !ok {
				return rect{}, fmt.Errorf("netsec: value %q is not in domain of axis %q", value, u.axes[dim].name)
			}
			if !seen[idx] {
				seen[idx] = true
				out.sets[dim] = append(out.sets[dim], idx)
			}
		}
		if len(out.sets[dim]) == 0 {
			return rect{}, fmt.Errorf("netsec: rectangle has an empty set on axis %q", u.axes[dim].name)
		}
	}
	return out, nil
}

// enumeratePoints calls fn with every named point contained in the rectangle.
func (u *Universe) enumeratePoints(r rect, fn func(Point)) {
	point := make(Point, len(r.sets))
	var walk func(dim int)
	walk = func(dim int) {
		if dim == len(r.sets) {
			fn(append(Point(nil), point...))
			return
		}
		for _, idx := range r.sets[dim] {
			point[dim] = u.axes[dim].values[idx]
			walk(dim + 1)
		}
	}
	walk(0)
}
