package conv

import (
	"errors"
	"fmt"
)

// Sum computes the distribution of a sum of independent variables and
// caches intermediate convolutions in a balanced binary tree, so that
// replacing one variable only recomputes O(log n) nodes along the path
// from that leaf to the root.
type Sum struct {
	root   *node
	leaves []*node
}

type node struct {
	dist        *Dist
	left, right *node
	parent      *node
}

// NewSum builds a Sum over the given variable distributions.
func NewSum(vars []*Dist) (*Sum, error) {
	if len(vars) == 0 {
		return nil, errors.New("conv: no variables")
	}
	for i, d := range vars {
		if d == nil {
			return nil, fmt.Errorf("conv: nil variable at index %d", i)
		}
	}
	leaves := make([]*node, len(vars))
	var build func(lo, hi int, parent *node) *node
	build = func(lo, hi int, parent *node) *node {
		n := &node{parent: parent}
		if hi-lo == 1 {
			n.dist = vars[lo]
			leaves[lo] = n
			return n
		}
		mid := lo + (hi-lo)/2
		n.left = build(lo, mid, n)
		n.right = build(mid, hi, n)
		n.dist = Convolve(n.left.dist, n.right.dist)
		return n
	}
	return &Sum{root: build(0, len(vars), nil), leaves: leaves}, nil
}

// Set replaces the i-th variable's distribution, reusing the cached
// convolutions of all subtrees that do not contain variable i.
func (s *Sum) Set(i int, d *Dist) error {
	if i < 0 || i >= len(s.leaves) {
		return fmt.Errorf("conv: variable index %d out of range", i)
	}
	if d == nil {
		return errors.New("conv: nil distribution")
	}
	leaf := s.leaves[i]
	leaf.dist = d
	for n := leaf.parent; n != nil; n = n.parent {
		n.dist = Convolve(n.left.dist, n.right.dist)
	}
	return nil
}

// Dist returns the distribution of the sum of all variables.
func (s *Sum) Dist() *Dist { return s.root.dist }
