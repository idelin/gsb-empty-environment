package noderouter

import (
	"errors"
	"sort"
)

// ErrNoRoute is returned when no healthy node matches the selector.
var ErrNoRoute = errors.New("noderouter: no healthy node matches selector")

// Router answers topology queries against a local MemberList replica.
type Router struct {
	ml *MemberList
}

// NewRouter builds a Router over the given membership view.
func NewRouter(ml *MemberList) *Router { return &Router{ml: ml} }

// Subset returns all healthy members matching the selector expression,
// ordered by descending weight (ties broken by node ID for determinism).
func (r *Router) Subset(selectorExpr string) ([]NodeState, error) {
	sel, err := ParseSelector(selectorExpr)
	if err != nil {
		return nil, err
	}
	var out []NodeState
	for _, s := range r.ml.Snapshot() {
		if !s.Meta.Healthy {
			continue
		}
		if s.Matches(sel) {
			out = append(out, s)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Meta.Weight != out[j].Meta.Weight {
			return out[i].Meta.Weight > out[j].Meta.Weight
		}
		return out[i].ID < out[j].ID
	})
	return out, nil
}

// Route returns the single best node for the selector: healthy, matching,
// highest weight. It returns ErrNoRoute when the subset is empty.
func (r *Router) Route(selectorExpr string) (NodeState, error) {
	sub, err := r.Subset(selectorExpr)
	if err != nil {
		return NodeState{}, err
	}
	if len(sub) == 0 {
		return NodeState{}, ErrNoRoute
	}
	return sub[0], nil
}
