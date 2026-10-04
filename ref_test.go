package set32

import "sort"

// refSet 是参考实现：标准 map + 排序，用于对照验证。
type refSet struct {
	m map[uint32]struct{}
}

func newRef() *refSet { return &refSet{m: make(map[uint32]struct{})} }

func (r *refSet) add(x uint32) { r.m[x] = struct{}{} }

func (r *refSet) remove(x uint32) { delete(r.m, x) }

func (r *refSet) contains(x uint32) bool {
	_, ok := r.m[x]
	return ok
}

func (r *refSet) sorted() []uint32 {
	out := make([]uint32, 0, len(r.m))
	for v := range r.m {
		out = append(out, v)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

func (r *refSet) rank(x uint32) int {
	n := 0
	for v := range r.m {
		if v <= x {
			n++
		}
	}
	return n
}

func (r *refSet) sel(k int) (uint32, bool) {
	s := r.sorted()
	if k < 0 || k >= len(s) {
		return 0, false
	}
	return s[k], true
}

func (r *refSet) and(o *refSet) *refSet {
	out := newRef()
	for v := range r.m {
		if o.contains(v) {
			out.add(v)
		}
	}
	return out
}

func (r *refSet) or(o *refSet) *refSet {
	out := newRef()
	for v := range r.m {
		out.add(v)
	}
	for v := range o.m {
		out.add(v)
	}
	return out
}

func (r *refSet) xor(o *refSet) *refSet {
	out := newRef()
	for v := range r.m {
		if !o.contains(v) {
			out.add(v)
		}
	}
	for v := range o.m {
		if !r.contains(v) {
			out.add(v)
		}
	}
	return out
}
