package factor

import (
	"errors"
	"fmt"
	"math/big"
	"strings"
)

// CellEquation is one exact rational identity
//
//	joint(assignment) = factor_0 * factor_1 * ... * factor_k
//
// where each factor is a block-marginal value. All values are exact
// rationals; zero cells appear as ordinary equations (0 = product).
type CellEquation struct {
	Assignment []int
	Joint      *big.Rat
	Factors    []*big.Rat
	Product    *big.Rat
}

// Holds reports whether Joint equals Product exactly.
func (e CellEquation) Holds() bool { return e.Joint.Cmp(e.Product) == 0 }

// SplitCertificate certifies one hierarchical split step: the marginal
// over Remaining factorizes as the marginal of Block times the marginal
// of the rest, proven by one exact equation per cell of the remaining
// marginal table.
type SplitCertificate struct {
	Remaining []int
	Block     []int
	Equations []CellEquation
}

// Certificate is an exact, independently checkable proof that a joint
// table factorizes over a partition. It contains the block marginals, one
// exact equation per cell of the full table, and one SplitCertificate per
// hierarchical split. Verification uses only exact rational arithmetic.
type Certificate struct {
	Dims      []int
	Partition Partition
	Marginals []*Table
	Equations []CellEquation
	Splits    []SplitCertificate
}

// BuildCertificate constructs the exact equality certificate for a
// product-form partition of t. It fails if p is not a valid factorization.
func BuildCertificate(t *Table, p Partition) (*Certificate, error) {
	if !IsProductForm(t, p) {
		return nil, fmt.Errorf("factor: %s is not a valid product-form factorization", p)
	}
	margs := blockMarginals(t, p)
	c := &Certificate{
		Dims:      append([]int(nil), t.Dims...),
		Partition: p,
		Marginals: margs,
	}
	// Full-table equations: joint(x) = prod_i marginal_i(x_{B_i}).
	for flat := 0; flat < t.Size(); flat++ {
		assign := t.Assignment(flat)
		eq := CellEquation{
			Assignment: assign,
			Joint:      new(big.Rat).Set(t.Cells[flat]),
			Product:    big.NewRat(1, 1),
		}
		for i, b := range p.Blocks {
			f := new(big.Rat).Set(margs[i].At(project(assign, b)))
			eq.Factors = append(eq.Factors, f)
			eq.Product.Mul(eq.Product, f)
		}
		c.Equations = append(c.Equations, eq)
	}
	// Hierarchical splits: remaining_i = blocks[i..]; split off block i.
	for i, b := range p.Blocks {
		var remaining []int
		for j := i; j < len(p.Blocks); j++ {
			remaining = append(remaining, p.Blocks[j]...)
		}
		var rest []int
		for j := i + 1; j < len(p.Blocks); j++ {
			rest = append(rest, p.Blocks[j]...)
		}
		margRem := t.Marginal(remaining)
		margBlock := t.Marginal(b)
		margRest := t.Marginal(rest) // empty rest: 1-cell table holding 1
		posB := positionsOf(b, remaining)
		posR := positionsOf(rest, remaining)
		sc := SplitCertificate{Remaining: remaining, Block: append([]int(nil), b...)}
		for flat := 0; flat < margRem.Size(); flat++ {
			assign := margRem.Assignment(flat)
			fb := new(big.Rat).Set(margBlock.At(project(assign, posB)))
			fr := new(big.Rat).Set(margRest.At(project(assign, posR)))
			prod := new(big.Rat).Mul(fb, fr)
			sc.Equations = append(sc.Equations, CellEquation{
				Assignment: assign,
				Joint:      new(big.Rat).Set(margRem.Cells[flat]),
				Factors:    []*big.Rat{fb, fr},
				Product:    prod,
			})
		}
		c.Splits = append(c.Splits, sc)
	}
	return c, nil
}

// VerifyCertificate independently checks every equation of c against the
// original table t using exact rational arithmetic, and confirms that the
// certified partition is canonical and finest (by full enumeration of all
// set partitions). It returns nil only if every check passes.
func VerifyCertificate(t *Table, c *Certificate) error {
	if t == nil || c == nil {
		return errors.New("factor: nil table or certificate")
	}
	if len(c.Dims) != len(t.Dims) {
		return errors.New("factor: certificate dims mismatch")
	}
	for i := range c.Dims {
		if c.Dims[i] != t.Dims[i] {
			return errors.New("factor: certificate dims mismatch")
		}
	}
	if !NewPartition(c.Partition.Blocks).Equal(c.Partition) {
		return fmt.Errorf("factor: partition %s is not in canonical block order", c.Partition)
	}
	// Recompute marginals from the original table and compare.
	margs := blockMarginals(t, c.Partition)
	if len(c.Marginals) != len(margs) {
		return errors.New("factor: certificate marginal count mismatch")
	}
	for i := range margs {
		if !margs[i].Equal(c.Marginals[i]) {
			return fmt.Errorf("factor: certificate marginal of block %v does not match the table", c.Partition.Blocks[i])
		}
	}
	// Full-table equations.
	if len(c.Equations) != t.Size() {
		return errors.New("factor: certificate equation count mismatch")
	}
	for _, eq := range c.Equations {
		if err := checkEquation(t, c.Partition, margs, eq); err != nil {
			return err
		}
	}
	// Hierarchical split equations, recomputed from scratch.
	if len(c.Splits) != len(c.Partition.Blocks) {
		return errors.New("factor: certificate split count mismatch")
	}
	for i, sc := range c.Splits {
		var remaining, rest []int
		for j := i; j < len(c.Partition.Blocks); j++ {
			remaining = append(remaining, c.Partition.Blocks[j]...)
		}
		for j := i + 1; j < len(c.Partition.Blocks); j++ {
			rest = append(rest, c.Partition.Blocks[j]...)
		}
		if !sameSet(sc.Remaining, remaining) || !sameSet(sc.Block, c.Partition.Blocks[i]) {
			return fmt.Errorf("factor: split %d variable sets mismatch", i)
		}
		margRem := t.Marginal(remaining)
		margBlock := t.Marginal(c.Partition.Blocks[i])
		margRest := t.Marginal(rest)
		if len(sc.Equations) != margRem.Size() {
			return fmt.Errorf("factor: split %d equation count mismatch", i)
		}
		for _, eq := range sc.Equations {
			if len(eq.Factors) != 2 {
				return fmt.Errorf("factor: split %d equation must have 2 factors", i)
			}
			if margRem.At(eq.Assignment).Cmp(eq.Joint) != 0 {
				return fmt.Errorf("factor: split %d equation joint mismatch at %v", i, eq.Assignment)
			}
			if margBlock.At(project(eq.Assignment, positionsOf(sc.Block, sc.Remaining))).Cmp(eq.Factors[0]) != 0 ||
				margRest.At(project(eq.Assignment, positionsOf(rest, sc.Remaining))).Cmp(eq.Factors[1]) != 0 {
				return fmt.Errorf("factor: split %d equation factor mismatch at %v", i, eq.Assignment)
			}
			prod := new(big.Rat).Mul(eq.Factors[0], eq.Factors[1])
			if prod.Cmp(eq.Product) != 0 || !eq.Holds() {
				return fmt.Errorf("factor: split %d equation does not hold at %v", i, eq.Assignment)
			}
		}
	}
	// Finestness, independently of how the partition was produced.
	return VerifyFinest(t, c.Partition)
}

func checkEquation(t *Table, p Partition, margs []*Table, eq CellEquation) error {
	if len(eq.Factors) != len(p.Blocks) {
		return fmt.Errorf("factor: equation at %v has wrong factor count", eq.Assignment)
	}
	if t.At(eq.Assignment).Cmp(eq.Joint) != 0 {
		return fmt.Errorf("factor: equation joint mismatch at %v", eq.Assignment)
	}
	prod := big.NewRat(1, 1)
	for i, b := range p.Blocks {
		want := margs[i].At(project(eq.Assignment, b))
		if want.Cmp(eq.Factors[i]) != 0 {
			return fmt.Errorf("factor: equation factor %d mismatch at %v", i, eq.Assignment)
		}
		prod.Mul(prod, eq.Factors[i])
	}
	if prod.Cmp(eq.Product) != 0 {
		return fmt.Errorf("factor: equation product mismatch at %v", eq.Assignment)
	}
	if !eq.Holds() {
		return fmt.Errorf("factor: equation %s = %s does not hold at %v", eq.Joint, eq.Product, eq.Assignment)
	}
	return nil
}

func sameSet(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	inB := make(map[int]bool, len(b))
	for _, x := range b {
		inB[x] = true
	}
	for _, x := range a {
		if !inB[x] {
			return false
		}
	}
	return true
}

// String renders the certificate as exact rational equations: one block
// per hierarchical split, then the full joint factorization.
func (c *Certificate) String() string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "partition: %s\n", c.Partition)
	for i, sc := range c.Splits {
		fmt.Fprintf(&sb, "split %d: P{%s} = P{%s} * P{%s}\n", i, joinInts(sc.Remaining), joinInts(sc.Block), joinInts(diffSet(sc.Remaining, sc.Block)))
		for _, eq := range sc.Equations {
			fmt.Fprintf(&sb, "  P(%s) = %s = %s * %s\n",
				joinInts(eq.Assignment), eq.Joint.RatString(), eq.Factors[0].RatString(), eq.Factors[1].RatString())
		}
	}
	sb.WriteString("joint factorization:\n")
	for _, eq := range c.Equations {
		factors := make([]string, len(eq.Factors))
		for i, f := range eq.Factors {
			factors[i] = f.RatString()
		}
		fmt.Fprintf(&sb, "  P(%s) = %s = %s\n", joinInts(eq.Assignment), eq.Joint.RatString(), strings.Join(factors, " * "))
	}
	return sb.String()
}

func joinInts(v []int) string {
	if len(v) == 0 {
		return "-"
	}
	parts := make([]string, len(v))
	for i, x := range v {
		parts[i] = fmt.Sprint(x)
	}
	return strings.Join(parts, ",")
}

func diffSet(a, b []int) []int {
	inB := map[int]bool{}
	for _, x := range b {
		inB[x] = true
	}
	var out []int
	for _, x := range a {
		if !inB[x] {
			out = append(out, x)
		}
	}
	return out
}

// positionsOf returns, for each variable in sub, its position within the
// sorted variable set super. Both sub and super must be sorted ascending
// and sub must be a subset of super.
func positionsOf(sub, super []int) []int {
	pos := make([]int, len(sub))
	j := 0
	for i, v := range sub {
		for super[j] != v {
			j++
		}
		pos[i] = j
	}
	return pos
}
