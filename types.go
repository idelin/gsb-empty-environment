package netsec

// Dimension is one finite axis of the policy space, e.g. "subject",
// "action", "resource" or a tag key such as "tag:env".
type Dimension struct {
	Name   string   `json:"name"`
	Values []string `json:"values"`
}

// Domain is the full finite space over which policies are evaluated.
// Every dimension is independent; the space is the cartesian product
// of all dimension values.
type Domain []Dimension

// Point is a single fully-assigned cell of the domain: exactly one
// value per dimension.
type Point map[string]string

// Rect is an axis-aligned hyper-rectangle: a conjunction of
// per-dimension value sets. A dimension absent from Values is a
// wildcard (all values of that dimension match). It serves both as a
// rule condition and as a fragment of a cover.
type Rect struct {
	Values map[string]map[string]bool `json:"values,omitempty"`
}

// FullRect returns the rectangle covering the entire domain.
func FullRect() Rect { return Rect{} }

// Contains reports whether pt falls inside r. pt must assign a value
// to every dimension constrained by r.
func (r Rect) Contains(pt Point) bool {
	for dim, vals := range r.Values {
		if !vals[pt[dim]] {
			return false
		}
	}
	return true
}

// Intersects reports whether r and o share at least one point.
func (r Rect) Intersects(o Rect) bool {
	for dim, vals := range r.Values {
		other, ok := o.Values[dim]
		if !ok {
			continue
		}
		shared := false
		for v := range vals {
			if other[v] {
				shared = true
				break
			}
		}
		if !shared {
			return false
		}
	}
	return true
}

// Intersect returns the conjunction of r and o, or false if they are
// disjoint.
func (r Rect) Intersect(o Rect) (Rect, bool) {
	if !r.Intersects(o) {
		return Rect{}, false
	}
	out := r.Clone()
	if out.Values == nil {
		out.Values = map[string]map[string]bool{}
	}
	for dim, vals := range o.Values {
		existing, ok := out.Values[dim]
		if !ok {
			out.Values[dim] = cloneSet(vals)
			continue
		}
		merged := map[string]bool{}
		for v := range existing {
			if vals[v] {
				merged[v] = true
			}
		}
		out.Values[dim] = merged
	}
	return out, true
}

// Clone returns a deep copy of r.
func (r Rect) Clone() Rect {
	out := Rect{}
	if r.Values != nil {
		out.Values = make(map[string]map[string]bool, len(r.Values))
		for dim, vals := range r.Values {
			out.Values[dim] = cloneSet(vals)
		}
	}
	return out
}

func cloneSet(s map[string]bool) map[string]bool {
	out := make(map[string]bool, len(s))
	for v := range s {
		out[v] = true
	}
	return out
}

// valuesOf returns the value set of dim constrained by r, or the full
// dimension when r leaves it unconstrained.
func (d Domain) valuesOf(r Rect, dim Dimension) map[string]bool {
	if vals, ok := r.Values[dim.Name]; ok {
		return vals
	}
	full := make(map[string]bool, len(dim.Values))
	for _, v := range dim.Values {
		full[v] = true
	}
	return full
}

// Points enumerates every point of the domain. The count is the
// product of all dimension sizes, so only call this on shrunk
// domains (verification, tests).
func (d Domain) Points() []Point {
	pts := []Point{{}}
	for _, dim := range d {
		var next []Point
		for _, pt := range pts {
			for _, v := range dim.Values {
				p := make(Point, len(d))
				for k, val := range pt {
					p[k] = val
				}
				p[dim.Name] = v
				next = append(next, p)
			}
		}
		pts = next
	}
	return pts
}
