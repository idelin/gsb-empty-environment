package crossmesh

import "sort"

// vKey identifies one (scope cell, atomic time interval) column.
type vKey struct {
	cell int
	time int
}

// actionMask is a bitset over the global action universe. The universe is
// bounded to 64 entries; graphs needing more are rejected at build time.
type actionMask uint64

// vSet is a sparse set of three-dimensional voxels. Each entry pairs a
// (cell, time) column with the exact set of actions authorized in that column.
// The full marker represents the complete universe, used only for trusted-root
// states before their first edge intersection materializes them.
type vSet struct {
	full bool
	m    map[vKey]actionMask
}

func newVSet() *vSet {
	return &vSet{m: map[vKey]actionMask{}}
}

func fullVSet() *vSet {
	return &vSet{full: true}
}

func (v *vSet) isEmpty() bool {
	return !v.full && len(v.m) == 0
}

func (v *vSet) clone() *vSet {
	if v == nil {
		return newVSet()
	}
	c := &vSet{full: v.full, m: make(map[vKey]actionMask, len(v.m))}
	for k, val := range v.m {
		c.m[k] = val
	}
	return c
}

// unionInto ORs other into v in place.
func (v *vSet) unionInto(other *vSet) {
	if other == nil || other.isEmpty() {
		return
	}
	if v.full || other.full {
		v.full = true
		v.m = nil
		return
	}
	if v.m == nil {
		v.m = map[vKey]actionMask{}
	}
	for k, mask := range other.m {
		v.m[k] |= mask
	}
}

// equals compares two voxel sets exactly.
func (v *vSet) equals(other *vSet) bool {
	if v == nil || other == nil {
		return (v == nil || v.isEmpty()) && (other == nil || other.isEmpty())
	}
	if v.full != other.full {
		return false
	}
	if v.full {
		return true
	}
	if len(v.m) != len(other.m) {
		return false
	}
	for k, mask := range v.m {
		if other.m[k] != mask {
			return false
		}
	}
	return true
}

// timeUniverse is the ordered partition of the time line derived from every
// edge's [NotBefore, NotAfter). Interval i is [bounds[i], bounds[i+1]).
type timeUniverse struct {
	bounds []int64
}

func buildTimeUniverse(edges []DelegationEdge) *timeUniverse {
	pts := map[int64]struct{}{}
	for _, e := range edges {
		pts[e.NotBefore] = struct{}{}
		pts[e.NotAfter] = struct{}{}
	}
	out := make([]int64, 0, len(pts))
	for p := range pts {
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return &timeUniverse{bounds: out}
}

// intervalRange returns the half-open index range [lo, hi) of atomic intervals
// covered by [from, to).
func (tu *timeUniverse) intervalRange(from, to int64) (lo, hi int) {
	lo = sort.Search(len(tu.bounds), func(i int) bool { return tu.bounds[i] >= from })
	hi = sort.Search(len(tu.bounds), func(i int) bool { return tu.bounds[i] >= to })
	return lo, hi
}

func buildActionUniverse(edges []DelegationEdge) ([]string, map[string]uint8) {
	uniq := map[string]struct{}{}
	for _, e := range edges {
		for _, a := range e.Actions {
			uniq[a] = struct{}{}
		}
	}
	out := make([]string, 0, len(uniq))
	for a := range uniq {
		out = append(out, a)
	}
	sort.Strings(out)
	idx := map[string]uint8{}
	for i, a := range out {
		idx[a] = uint8(i)
	}
	return out, idx
}

// edgeCover is the precomputed three-dimensional footprint of one edge.
type edgeCover struct {
	cells   bitSet
	timeLo  int
	timeHi  int
	actions actionMask
}

func buildEdgeCover(e DelegationEdge, su *scopeUniverse, tu *timeUniverse,
	actionIdx map[string]uint8) edgeCover {
	var mask actionMask
	for _, a := range e.Actions {
		if bit, ok := actionIdx[a]; ok {
			mask |= 1 << bit
		}
	}
	lo, hi := tu.intervalRange(e.NotBefore, e.NotAfter)
	return edgeCover{
		cells:   scopeCover(su, e.AllowedPrefixes, e.ExcludedSubtrees),
		timeLo:  lo,
		timeHi:  hi,
		actions: mask,
	}
}

// intersectByCover returns the voxel set that survives intersection with one
// edge's exact three-dimensional product. The full set is materialized over
// the engine's finite universe on first contact.
func intersectByCover(in *vSet, cov edgeCover, cellCount, intervalCount int) *vSet {
	if in == nil || in.isEmpty() {
		return newVSet()
	}
	out := newVSet()
	if in.full {
		for cell := range cov.cells {
			for t := cov.timeLo; t < cov.timeHi; t++ {
				out.m[vKey{cell: cell, time: t}] = cov.actions
			}
		}
		return out
	}
	for k, mask := range in.m {
		if !cov.cells.contains(k.cell) {
			continue
		}
		if k.time < cov.timeLo || k.time >= cov.timeHi {
			continue
		}
		got := mask & cov.actions
		if got != 0 {
			out.m[k] = got
		}
	}
	return out
}
