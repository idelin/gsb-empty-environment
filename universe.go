// Package netsec computes the effective semantic difference between two
// finite-domain cloud access policies.
//
// A policy is an ordered list of rules. Each rule has an effect (Allow or
// Deny) and a boolean Condition expressed over membership atoms of the
// finite domain axes: principal, action, resource and zero or more tag axes.
// Deny overrides allow; a point that no rule matches is denied.
//
// The central operation, [Diff], returns a non-overlapping rectangular cover
// of every point that is denied under the old policy but explicitly allowed
// under the new one, with the rules that fire at each rectangle attached.
// Diffing is semantic: rewording or reordering a policy without changing its
// decisions produces no findings.
package netsec

import (
	"fmt"
	"strings"
)

// Well-known axis indices. Any axis beyond TagAxis is a tag axis.
const (
	PrincipalAxis = 0
	ActionAxis    = 1
	ResourceAxis  = 2
	TagAxis       = 3
)

// TagDomain describes one finite tag axis. Key is the tag key; Values are its
// finite value domain.
type TagDomain struct {
	Key    string
	Values []string
}

// Universe is the finite domain every policy is interpreted over.
type Universe struct {
	axes []axis
}

type axis struct {
	name   string
	values []string
	index  map[string]int
}

// NewUniverse builds the finite domain. principals, actions and resources are
// the three mandatory axes; tags contributes zero or more tag axes.
func NewUniverse(principals, actions, resources []string, tags ...TagDomain) (*Universe, error) {
	specs := make([]axis, 0, 3+len(tags))
	specs = append(specs,
		axis{name: "principal", values: principals},
		axis{name: "action", values: actions},
		axis{name: "resource", values: resources},
	)
	for _, tag := range tags {
		specs = append(specs, axis{name: tag.Key, values: append([]string(nil), tag.Values...)})
	}

	u := &Universe{axes: make([]axis, len(specs))}
	seenNames := make(map[string]bool)
	for dim, spec := range specs {
		if strings.TrimSpace(spec.name) == "" {
			return nil, fmt.Errorf("netsec: axis %d has an empty name", dim)
		}
		if seenNames[spec.name] {
			return nil, fmt.Errorf("netsec: duplicate axis name %q", spec.name)
		}
		seenNames[spec.name] = true
		if len(spec.values) == 0 {
			return nil, fmt.Errorf("netsec: axis %q has an empty value domain", spec.name)
		}
		idx := make(map[string]int, len(spec.values))
		for i, value := range spec.values {
			if value == "" {
				return nil, fmt.Errorf("netsec: axis %q contains an empty value", spec.name)
			}
			if _, dup := idx[value]; dup {
				return nil, fmt.Errorf("netsec: axis %q contains duplicate value %q", spec.name, value)
			}
			idx[value] = i
		}
		u.axes[dim] = axis{
			name:   spec.name,
			values: append([]string(nil), spec.values...),
			index:  idx,
		}
	}
	return u, nil
}

// Dims is the number of axes, including tag axes.
func (u *Universe) Dims() int { return len(u.axes) }

// AxisName returns the name of axis dim (one of the well-known names or a tag key).
func (u *Universe) AxisName(dim int) string { return u.axes[dim].name }

// Values returns a copy of the finite value domain of axis dim.
func (u *Universe) Values(dim int) []string {
	return append([]string(nil), u.axes[dim].values...)
}

// Size returns the total number of points in the universe (the Cartesian
// product of all axes).
func (u *Universe) Size() int {
	size := 1
	for _, ax := range u.axes {
		size *= len(ax.values)
	}
	return size
}

// Point is one tuple of concrete axis values, in axis order.
type Point []string

// indexPoint translates named values to per-axis indices.
func (u *Universe) indexPoint(p Point) ([]int, error) {
	if len(p) != len(u.axes) {
		return nil, fmt.Errorf("netsec: point has %d values, universe has %d axes", len(p), len(u.axes))
	}
	out := make([]int, len(p))
	for dim, value := range p {
		idx, ok := u.axes[dim].index[value]
		if !ok {
			return nil, fmt.Errorf("netsec: value %q is not in domain of axis %q", value, u.axes[dim].name)
		}
		out[dim] = idx
	}
	return out, nil
}

// Shrunk returns a sub-universe keeping only the given values per axis.
// Order is preserved and values must be members of the original domains.
// It is used to "shrink the domain" for exhaustive verification on tractable
// sub-domains.
func (u *Universe) Shrunk(keep [][]string) (*Universe, error) {
	if len(keep) != len(u.axes) {
		return nil, fmt.Errorf("netsec: Shrunk got %d filters, universe has %d axes", len(keep), len(u.axes))
	}
	tags := make([]TagDomain, 0, max(0, len(u.axes)-TagAxis))
	for dim := range u.axes {
		seen := make(map[string]bool)
		values := make([]string, 0, len(keep[dim]))
		for _, value := range keep[dim] {
			if _, ok := u.axes[dim].index[value]; !ok {
				return nil, fmt.Errorf("netsec: value %q is not in domain of axis %q", value, u.axes[dim].name)
			}
			if seen[value] {
				continue
			}
			seen[value] = true
			values = append(values, value)
		}
		if dim < TagAxis {
			keep[dim] = values
			continue
		}
		tags = append(tags, TagDomain{Key: u.axes[dim].name, Values: values})
	}
	return NewUniverse(keep[PrincipalAxis], keep[ActionAxis], keep[ResourceAxis], tags...)
}

// enumerate walks every point of the universe, calling fn with named values.
func (u *Universe) enumerate(fn func(Point)) {
	point := make(Point, len(u.axes))
	var walk func(dim int)
	walk = func(dim int) {
		if dim == len(u.axes) {
			fn(append(Point(nil), point...))
			return
		}
		for _, value := range u.axes[dim].values {
			point[dim] = value
			walk(dim + 1)
		}
	}
	walk(0)
}
