package netsec

import "sort"

// Obj is an abstract heap object: all concrete objects allocated at one
// allocation site (one node in one procedure) are merged into it.
type Obj struct {
	Proc string
	Site NodeID
}

// pts is a may-points-to set. Uncertain aliases are always kept: a set with
// more than one element means "any of these", never "none of these".
type pts map[Obj]struct{}

func (p pts) clone() pts {
	if p == nil {
		return nil
	}
	q := make(pts, len(p))
	for o := range p {
		q[o] = struct{}{}
	}
	return q
}

// unionInto merges src into dst, reporting whether dst grew.
func unionInto(dst, src pts) bool {
	changed := false
	for o := range src {
		if _, ok := dst[o]; !ok {
			dst[o] = struct{}{}
			changed = true
		}
	}
	return changed
}

func (p pts) equal(q pts) bool {
	if len(p) != len(q) {
		return false
	}
	for o := range p {
		if _, ok := q[o]; !ok {
			return false
		}
	}
	return true
}

// sorted returns the elements in a deterministic order so that analysis
// output (report order, recorded paths) is reproducible.
func (p pts) sorted() []Obj {
	out := make([]Obj, 0, len(p))
	for o := range p {
		out = append(out, o)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Proc != out[j].Proc {
			return out[i].Proc < out[j].Proc
		}
		return out[i].Site < out[j].Site
	})
	return out
}

// heapState is the abstract heap: field-sensitive contents of abstract
// objects plus the set of objects that may have been freed.
type heapState struct {
	Fields map[Obj]map[string]pts
	Freed  map[Obj]struct{}
}

func newHeap() *heapState {
	return &heapState{Fields: map[Obj]map[string]pts{}, Freed: map[Obj]struct{}{}}
}

func (h *heapState) clone() *heapState {
	nh := newHeap()
	for o, fm := range h.Fields {
		nf := make(map[string]pts, len(fm))
		for f, s := range fm {
			nf[f] = s.clone()
		}
		nh.Fields[o] = nf
	}
	for o := range h.Freed {
		nh.Freed[o] = struct{}{}
	}
	return nh
}

// join merges o into h (may/weak semantics: unions everywhere), reporting
// whether h changed.
func (h *heapState) join(o *heapState) bool {
	changed := false
	for obj, fm := range o.Fields {
		dst := h.Fields[obj]
		if dst == nil {
			dst = map[string]pts{}
			h.Fields[obj] = dst
		}
		for f, s := range fm {
			cur := dst[f]
			if cur == nil {
				cur = pts{}
				dst[f] = cur
			}
			if unionInto(cur, s) {
				changed = true
			}
		}
	}
	for obj := range o.Freed {
		if _, ok := h.Freed[obj]; !ok {
			h.Freed[obj] = struct{}{}
			changed = true
		}
	}
	return changed
}

func (h *heapState) equal(o *heapState) bool {
	if len(h.Freed) != len(o.Freed) || len(h.Fields) != len(o.Fields) {
		return false
	}
	for obj := range h.Freed {
		if _, ok := o.Freed[obj]; !ok {
			return false
		}
	}
	for obj, fm := range h.Fields {
		ofm, ok := o.Fields[obj]
		if !ok || len(fm) != len(ofm) {
			return false
		}
		for f, s := range fm {
			os, ok := ofm[f]
			if !ok || !s.equal(os) {
				return false
			}
		}
	}
	return true
}

// state is an abstract dataflow state. The path is carried along so that
// reports can ship a replayable witness; it is deliberately excluded from
// equality and joins so the fixpoint stays finite.
type state struct {
	vars map[string]pts
	heap *heapState
	path []PathElem
}

func (s *state) clone() *state {
	ns := &state{vars: make(map[string]pts, len(s.vars)), heap: s.heap.clone()}
	for v, p := range s.vars {
		ns.vars[v] = p.clone()
	}
	ns.path = append(ns.path, s.path...)
	return ns
}

// join merges o into s (keeping s's path, the first witness), reporting
// whether s changed.
func (s *state) join(o *state) bool {
	changed := false
	for v, p := range o.vars {
		cur := s.vars[v]
		if cur == nil {
			cur = pts{}
			s.vars[v] = cur
		}
		if unionInto(cur, p) {
			changed = true
		}
	}
	if s.heap.join(o.heap) {
		changed = true
	}
	return changed
}
