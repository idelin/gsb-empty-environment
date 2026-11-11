package discretecomp

import (
	"container/heap"
	"math/big"
	"strings"
)

// TruncateOptions controls budgeted enumeration of the product space.
type TruncateOptions struct {
	// MassBudget is the largest fraction of the finite-ratio mass (measured in
	// the numerator distribution of the requested direction) that may be left
	// un-enumerated. It must be in [0, 1]. Zero enumerates everything and
	// reproduces the exact profile; one enumerates nothing.
	MassBudget *big.Rat

	// MaxNodes, when positive, caps the number of product buckets popped from
	// the priority queue. It is a hard safety limit that may stop enumeration
	// before MassBudget is reached. Zero means no node cap.
	MaxNodes int
}

// TruncationStats reports how much of the product space was inspected.
type TruncationStats struct {
	// NodesPopped is the number of product buckets taken from the queue.
	NodesPopped int
	// Buckets is the number of distinct likelihood-ratio buckets after merging.
	Buckets int
	// EnumeratedMass is the numerator-distribution mass that was enumerated.
	EnumeratedMass *big.Rat
	// RemainingMass is the numerator-distribution mass left un-enumerated.
	RemainingMass *big.Rat
	// Exact is true when nothing was left un-enumerated, in which case Lower
	// and Upper coincide with the exact delta.
	Exact bool
}

// Bounds is a provable interval [Lower, Upper] containing the exact delta.
type Bounds struct {
	Lower *big.Rat
	Upper *big.Rat
}

// TruncationResult bundles the delta interval with enumeration statistics.
type TruncationResult struct {
	Bounds Bounds
	Stats  TruncationStats
}

// node is one element of the best-first product enumeration. indices[i]
// selects a finite bucket of mechanism i. Each mechanism's buckets are sorted
// by decreasing ratio, so the product ratio is monotone in every index and a
// max-heap over product ratios enumerates buckets in decreasing ratio order.
type node struct {
	indices []int
	ratio   *big.Rat
	pMass   *big.Rat
	qMass   *big.Rat
}

type productHeap struct {
	items []*node
}

func (h *productHeap) Len() int { return len(h.items) }
func (h *productHeap) Less(i, j int) bool {
	return h.items[i].ratio.Cmp(h.items[j].ratio) > 0
}
func (h *productHeap) Swap(i, j int) { h.items[i], h.items[j] = h.items[j], h.items[i] }
func (h *productHeap) Push(x any)    { h.items = append(h.items, x.(*node)) }
func (h *productHeap) Pop() any {
	old := h.items
	n := len(old)
	it := old[n-1]
	h.items = old[:n-1]
	return it
}

// ComposeTruncated enumerates the largest likelihood-ratio product buckets
// until at most opts.MassBudget fraction of the finite mass remains
// un-inspected (or opts.MaxNodes is reached) and returns bounds on the exact
// delta for budget in direction dir.
//
// The omitted tail can contribute at most its own numerator mass, hence
// Upper = Lower + RemainingMass is a rigorous bound. Zero-probability events
// are always accounted for exactly and never truncated.
func ComposeTruncated(mechanisms []*Mechanism, budget *big.Rat, dir Direction, opts TruncateOptions) (*TruncationResult, error) {
	if len(mechanisms) == 0 {
		return nil, errEmptyMechanism
	}
	for _, m := range mechanisms {
		if m == nil {
			return nil, errEmptyMechanism
		}
	}
	if budget == nil || budget.Cmp(big.NewRat(1, 1)) < 0 {
		return nil, errBadBudget
	}
	massBudget := opts.MassBudget
	if massBudget == nil {
		massBudget = big.NewRat(0, 1)
	}
	if massBudget.Sign() < 0 || massBudget.Cmp(big.NewRat(1, 1)) > 0 {
		return nil, errBadMassBudget
	}
	if opts.MaxNodes < 0 {
		return nil, errBadNodeCap
	}

	// Product mass of the finite-ratio classes. A mechanism with no finite
	// buckets makes the whole finite product empty.
	finiteP := big.NewRat(1, 1)
	finiteQ := big.NewRat(1, 1)
	for _, m := range mechanisms {
		finiteP.Mul(finiteP, m.finitePMass())
		finiteQ.Mul(finiteQ, m.finiteQMass())
	}

	zeroMass := big.NewRat(0, 1)
	switch dir {
	case DeltaPOverQ:
		zeroMass = productPOnly(mechanisms)
	case DeltaQOverP:
		zeroMass = productQOnly(mechanisms)
	default:
		return nil, errBadBudget
	}

	totalNumerator := new(big.Rat).Set(finiteP)
	if dir == DeltaQOverP {
		totalNumerator.Set(finiteQ)
	}
	allowedRemaining := new(big.Rat).Mul(massBudget, totalNumerator)

	merged := map[string]Bucket{}
	enumP := new(big.Rat)
	enumQ := new(big.Rat)
	popped := 0

	if allHaveFinite(mechanisms) {
		h := &productHeap{}
		heap.Init(h)
		heap.Push(h, seedNode(mechanisms))
		visited := map[string]bool{indexKey(seedIndices(len(mechanisms))): true}

		for h.Len() > 0 {
			enumerated := new(big.Rat).Set(enumP)
			if dir == DeltaQOverP {
				enumerated.Set(enumQ)
			}
			remaining := new(big.Rat).Sub(totalNumerator, enumerated)
			if remaining.Cmp(allowedRemaining) <= 0 {
				break
			}
			if opts.MaxNodes > 0 && popped >= opts.MaxNodes {
				break
			}

			cur := heap.Pop(h).(*node)
			popped++

			enumP.Add(enumP, cur.pMass)
			enumQ.Add(enumQ, cur.qMass)
			key := cur.ratio.RatString()
			if exist, ok := merged[key]; ok {
				exist.PMass.Add(exist.PMass, cur.pMass)
				exist.QMass.Add(exist.QMass, cur.qMass)
				merged[key] = exist
			} else {
				merged[key] = Bucket{
					PMass: new(big.Rat).Set(cur.pMass),
					QMass: new(big.Rat).Set(cur.qMass),
					Ratio: new(big.Rat).Set(cur.ratio),
				}
			}

			for i := range mechanisms {
				nextIndices := append([]int(nil), cur.indices...)
				nextIndices[i]++
				if nextIndices[i] >= len(mechanisms[i].Finite) {
					continue
				}
				visKey := indexKey(nextIndices)
				if visited[visKey] {
					continue
				}
				visited[visKey] = true
				heap.Push(h, buildNode(mechanisms, nextIndices))
			}
		}
	}

	enumeratedMass := new(big.Rat).Set(enumP)
	remainingMass := new(big.Rat).Sub(finiteP, enumP)
	if dir == DeltaQOverP {
		enumeratedMass.Set(enumQ)
		remainingMass.Sub(finiteQ, enumQ)
	}

	lower := new(big.Rat).Set(zeroMass)
	switch dir {
	case DeltaPOverQ:
		for _, b := range merged {
			if b.Ratio.Cmp(budget) > 0 {
				term := new(big.Rat).Mul(budget, b.QMass)
				term.Sub(b.PMass, term)
				lower.Add(lower, term)
			}
		}
	case DeltaQOverP:
		inv := new(big.Rat).Inv(budget)
		for _, b := range merged {
			if b.Ratio.Cmp(inv) < 0 {
				term := new(big.Rat).Mul(inv, b.PMass)
				term.Sub(b.QMass, term)
				lower.Add(lower, term)
			}
		}
	}

	upper := new(big.Rat).Add(lower, remainingMass)
	exact := remainingMass.Sign() == 0

	return &TruncationResult{
		Bounds: Bounds{Lower: lower, Upper: upper},
		Stats: TruncationStats{
			NodesPopped:    popped,
			Buckets:        len(merged),
			EnumeratedMass: enumeratedMass,
			RemainingMass:  remainingMass,
			Exact:          exact,
		},
	}, nil
}

func allHaveFinite(mechanisms []*Mechanism) bool {
	for _, m := range mechanisms {
		if len(m.Finite) == 0 {
			return false
		}
	}
	return true
}

func seedIndices(k int) []int {
	idx := make([]int, k)
	return idx
}

func seedNode(mechanisms []*Mechanism) *node {
	return buildNode(mechanisms, seedIndices(len(mechanisms)))
}

func buildNode(mechanisms []*Mechanism, indices []int) *node {
	ratio := big.NewRat(1, 1)
	pMass := big.NewRat(1, 1)
	qMass := big.NewRat(1, 1)
	for i, idx := range indices {
		b := mechanisms[i].Finite[idx]
		ratio.Mul(ratio, b.Ratio)
		pMass.Mul(pMass, b.PMass)
		qMass.Mul(qMass, b.QMass)
	}
	return &node{indices: indices, ratio: ratio, pMass: pMass, qMass: qMass}
}

func indexKey(indices []int) string {
	parts := make([]string, len(indices))
	for i, v := range indices {
		parts[i] = big.NewInt(int64(v)).String()
	}
	return strings.Join(parts, ",")
}

func productPOnly(mechanisms []*Mechanism) *big.Rat {
	noPOnly := big.NewRat(1, 1)
	for _, m := range mechanisms {
		noPOnly.Mul(noPOnly, new(big.Rat).Sub(big.NewRat(1, 1), m.Zero.POnly))
	}
	return new(big.Rat).Sub(big.NewRat(1, 1), noPOnly)
}

func productQOnly(mechanisms []*Mechanism) *big.Rat {
	noQOnly := big.NewRat(1, 1)
	for _, m := range mechanisms {
		noQOnly.Mul(noQOnly, new(big.Rat).Sub(big.NewRat(1, 1), m.Zero.QOnly))
	}
	return new(big.Rat).Sub(big.NewRat(1, 1), noQOnly)
}
