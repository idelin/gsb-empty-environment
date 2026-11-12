package taskpkg

import "math"

// stratifiedDistribution is a hierarchical discrete distribution stored as a
// binary sum tree. The leaves hold the non-negative item weights and every
// internal node holds the sum of its subtree. Sampling walks one root-to-leaf
// path, so both selection and CDF queries take O(log n) time while preserving
// the exact discrete probability mass of every item.
type stratifiedDistribution struct {
	stride int
	tree   []float64
}

func newStratifiedDistribution(weights []float64) *stratifiedDistribution {
	count := len(weights)
	stride := 1
	for stride < count {
		stride *= 2
	}
	tree := make([]float64, stride*2)
	for index, weight := range weights {
		tree[stride+index] = weight
	}
	for node := stride - 1; node >= 1; node-- {
		tree[node] = tree[node*2] + tree[node*2+1]
	}
	return &stratifiedDistribution{stride: stride, tree: tree}
}

// total returns the sum of all leaf weights.
func (d *stratifiedDistribution) total() float64 { return d.tree[1] }

// weightAt returns the weight stored at leaf index.
func (d *stratifiedDistribution) weightAt(index int) float64 {
	return d.tree[d.stride+index]
}

// sample selects a leaf proportional to its weight. xi must be in [0, 1).
// The returned leaf always carries positive weight whenever the total is
// positive; the remainder is its normalized discrete probability mass.
func (d *stratifiedDistribution) sample(xi float64) (index int, probability float64) {
	total := d.total()
	if total <= 0 {
		return 0, 0
	}
	target := math.Min(xi, 1-math.SmallestNonzeroFloat64) * total
	node := 1
	for node < d.stride {
		left := node * 2
		leftSum := d.tree[left]
		if target < leftSum {
			node = left
		} else {
			target -= leftSum
			node = left + 1
		}
	}
	index = node - d.stride
	return index, d.weightAt(index) / total
}
