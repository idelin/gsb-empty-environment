package decomp

import "math/big"

// Factors reports whether the joint table equals, on every cell, the
// product of its block marginals for partition p:
//
//	P(x) == prod_b Marginal_b(x_b)   for all x
//
// The check is a pure multiply-and-compare over exact rationals. No
// conditional probabilities are ever formed, so zero-probability cells
// cannot cause divisions by zero and cannot be silently ignored.
//
// Note this tests mutual independence of the blocks, which is strictly
// stronger than pairwise (marginal) independence of variable pairs.
func (t *Table) Factors(p Partition) bool {
	margs := make([]*Table, p.NumBlocks())
	for i, b := range p.Blocks {
		margs[i] = t.Marginal(b)
	}
	return productEquals(t, p, margs)
}

// productEquals checks t(x) == prod_b margs[b](x_b) on every cell.
func productEquals(t *Table, p Partition, margs []*Table) bool {
	blockStrides := make([][]int, len(p.Blocks))
	for bi, b := range p.Blocks {
		st := make([]int, len(b))
		acc := 1
		for i := len(b) - 1; i >= 0; i-- {
			st[i] = acc
			acc *= t.Dims[b[i]]
		}
		blockStrides[bi] = st
	}
	prod := new(big.Rat)
	for flat, want := range t.Probs {
		tup := t.tuple(flat)
		prod.SetInt64(1)
		for bi, b := range p.Blocks {
			sub := 0
			for i, v := range b {
				sub += tup[v] * blockStrides[bi][i]
			}
			prod.Mul(prod, margs[bi].Probs[sub])
		}
		if prod.Cmp(want) != 0 {
			return false
		}
	}
	return true
}

// Result is the outcome of a finest-decomposition search.
type Result struct {
	Partition   Partition    // finest partition, canonical (lexicographically smallest RGS)
	Marginals   []*Table     // exact joint distribution of each block
	Certificate *Certificate // exact equality + finest-ness proof
}

// Decompose finds the finest partition of the variables into mutually
// independent blocks, i.e. a valid factorisation with the maximum
// possible number of blocks.
//
// Method: enumerate all Bell(n) set partitions (n <= 6, so at most 203),
// test each with the exact product-of-marginals identity, and keep the
// valid ones with the most blocks. Ties are broken by canonical
// block-numbering order (lexicographically smallest RGS), which the
// enumeration order provides for free. (In fact the set of valid
// partitions is closed under common refinement, so the finest valid
// partition is unique; the tie-break is a deterministic safeguard.)
//
// The same exhaustive enumeration doubles as the finest-ness proof and
// is recorded in the certificate.
func (t *Table) Decompose() (*Result, error) {
	if err := t.Validate(); err != nil {
		return nil, err
	}
	all := AllPartitions(t.NumVars())
	var valid []Partition
	best := 0
	var chosen Partition
	for _, p := range all {
		if t.Factors(p) {
			valid = append(valid, p)
			if p.NumBlocks() > best {
				best = p.NumBlocks()
				chosen = p
			}
		}
	}
	margs := make([]*Table, chosen.NumBlocks())
	for i, b := range chosen.Blocks {
		margs[i] = t.Marginal(b)
	}
	cert := buildCertificate(t, chosen, margs, all, valid)
	return &Result{Partition: chosen, Marginals: margs, Certificate: cert}, nil
}
