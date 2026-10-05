package distconv

import (
	"errors"
	"strconv"
)

// Convolver maintains the convolution of a fixed number of independent
// variables in a binary tree, caching the convolution of every subtree.
// After Set replaces one variable, Sum recomputes only the O(log n)
// cached nodes on the path from that leaf to the root; every unchanged
// subtree is reused as is. Correctness relies on convolution of
// independent variables being associative and commutative.
type Convolver struct {
	dists []*Dist
	nodes []*Dist // 1-based heap layout: leaves at n..2n-1, root at 1
	dirty []bool
	sums  int // pairwise convolutions performed (for instrumentation)
}

// NewConvolver builds a Convolver over the given distributions.
// It requires at least one distribution.
func NewConvolver(dists []*Dist) (*Convolver, error) {
	if len(dists) == 0 {
		return nil, errors.New("distconv: no distributions to convolve")
	}
	for i, d := range dists {
		if d == nil {
			return nil, errors.New("distconv: nil distribution at index " + strconv.Itoa(i))
		}
	}
	n := len(dists)
	c := &Convolver{
		dists: dists,
		nodes: make([]*Dist, 2*n),
		dirty: make([]bool, 2*n),
	}
	for i := 0; i < n; i++ {
		c.nodes[n+i] = dists[i]
	}
	for i := n - 1; i >= 1; i-- {
		c.dirty[i] = true
	}
	return c, nil
}

// Set replaces the distribution at index i, invalidating exactly the
// cached subtree convolutions that depend on it.
func (c *Convolver) Set(i int, d *Dist) {
	n := len(c.dists)
	c.dists[i] = d
	c.nodes[n+i] = d
	for k := (n + i) / 2; k >= 1; k /= 2 {
		c.dirty[k] = true
	}
}

// Sum returns the distribution of the sum of all current variables,
// recomputing only invalidated subtrees.
func (c *Convolver) Sum() *Dist {
	// Children always have larger indices than their parents, so a
	// descending pass computes every dirty node after its children.
	for i := len(c.dists) - 1; i >= 1; i-- {
		if c.dirty[i] {
			c.nodes[i] = Convolve(c.nodes[2*i], c.nodes[2*i+1])
			c.dirty[i] = false
			c.sums++
		}
	}
	return c.nodes[1]
}

// ComputedPairs reports how many pairwise convolutions have been
// performed so far; useful to confirm subtree reuse.
func (c *Convolver) ComputedPairs() int { return c.sums }
