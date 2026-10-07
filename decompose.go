package factor

import (
	"errors"
	"fmt"
	"math/big"
)

// blockMarginals computes the exact marginal table of every block.
func blockMarginals(t *Table, p Partition) []*Table {
	margs := make([]*Table, len(p.Blocks))
	for i, b := range p.Blocks {
		margs[i] = t.Marginal(b)
	}
	return margs
}

// IsProductForm reports whether the joint table equals the product of the
// block marginals of p, checked cell by cell with exact rational
// multiplication. No division is ever used, so zero-probability cells are
// verified directly: a zero joint cell must still equal the product of the
// corresponding block-marginal values.
func IsProductForm(t *Table, p Partition) bool {
	if p.NumVars() != t.NumVars() {
		return false
	}
	margs := blockMarginals(t, p)
	for flat := 0; flat < t.Size(); flat++ {
		assign := t.Assignment(flat)
		prod := big.NewRat(1, 1)
		for i, b := range p.Blocks {
			prod.Mul(prod, margs[i].At(project(assign, b)))
		}
		if prod.Cmp(t.Cells[flat]) != 0 {
			return false
		}
	}
	return true
}

// FinestPartition returns the unique finest partition of the variables
// into mutually independent blocks: the joint table equals the product of
// the block marginals, and no strictly finer partition has that property.
//
// The valid factorizations of a joint distribution are closed under meet
// (blockwise intersection), even in the presence of zero-probability
// cells, so the finest valid partition is the meet of all valid two-block
// splits {S | V\S}. The result is returned in canonical block order.
// VerifyFinest independently confirms finestness by enumerating every set
// partition.
func FinestPartition(t *Table) Partition {
	n := t.NumVars()
	best := OneBlockPartition(n)
	// Every two-block split {S | V\S} with 0 in S, S proper and non-empty.
	for mask := 0; mask < 1<<(n-1); mask++ {
		s := []int{0}
		for v := 1; v < n; v++ {
			if mask&(1<<(v-1)) != 0 {
				s = append(s, v)
			}
		}
		if len(s) == n {
			continue
		}
		inS := make([]bool, n)
		for _, v := range s {
			inS[v] = true
		}
		var rest []int
		for v := 0; v < n; v++ {
			if !inS[v] {
				rest = append(rest, v)
			}
		}
		split := NewPartition([][]int{s, rest})
		if IsProductForm(t, split) {
			best = Meet(best, split)
		}
	}
	return best
}

// VerifyFinest independently verifies that p is the finest valid
// factorization of t by enumerating every set partition of the variables
// (Bell(6) = 203 at most) and checking that
//  1. p itself is a valid product-form factorization, and
//  2. every valid partition q is coarsened by p (p refines q), i.e. no
//     valid partition strictly finer than p exists.
//
// This deliberately does not reuse the meet-of-splits argument behind
// FinestPartition, so it serves as an independent check. Note that
// pairwise (marginal) independence of variables does not imply mutual
// independence; only the full joint product test above is decisive.
func VerifyFinest(t *Table, p Partition) error {
	if !IsProductForm(t, p) {
		return fmt.Errorf("factor: %s is not a valid product-form factorization", p)
	}
	for _, q := range AllPartitions(t.NumVars()) {
		if IsProductForm(t, q) && !p.Refines(q) {
			return fmt.Errorf("factor: %s is not finest; valid partition %s is incomparable or finer", p, q)
		}
	}
	return nil
}

// Decompose computes the finest independent-block partition of t and
// verifies it by full enumeration of all set partitions. It returns the
// canonical partition together with its exact equality certificate.
func Decompose(t *Table) (Partition, *Certificate, error) {
	if t == nil {
		return Partition{}, nil, errors.New("factor: nil table")
	}
	p := FinestPartition(t)
	if err := VerifyFinest(t, p); err != nil {
		return Partition{}, nil, err
	}
	cert, err := BuildCertificate(t, p)
	if err != nil {
		return Partition{}, nil, err
	}
	return p, cert, nil
}
