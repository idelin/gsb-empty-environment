package decomp

import (
	"fmt"
	"strings"
)

// SplitStep certifies one exact split of the decomposition:
// the joint marginal over SplitOff+Remaining equals the marginal over
// SplitOff times the marginal over Remaining, checked on every cell.
type SplitStep struct {
	SplitOff  []int  // block split off at this step
	Remaining []int  // variables still joined after the split
	Cells     int    // number of cells checked exactly
	Equation  string // human-readable exact equation
}

// Certificate is an exact, independently re-verifiable proof of a
// decomposition and of its finest-ness.
type Certificate struct {
	Partition            Partition   // the certified finest partition
	Marginals            []*Table    // exact joint distribution of each block
	FullCheck            SplitStep   // P(x) == prod of block marginals, all cells
	Steps                []SplitStep // one exact equality per split
	PartitionsEnumerated int         // Bell(n); all set partitions tested
	ValidPartitions      []Partition // every partition that factors, in canonical order
	FinestNumBlocks      int         // max blocks among ValidPartitions
}

func blockVars(block []int) string {
	parts := make([]string, len(block))
	for i, v := range block {
		parts[i] = fmt.Sprintf("x%d", v)
	}
	return strings.Join(parts, ",")
}

func unionOfBlocks(blocks [][]int, from int) []int {
	var out []int
	for _, b := range blocks[from:] {
		out = append(out, b...)
	}
	return out
}

func buildCertificate(t *Table, p Partition, margs []*Table, all, valid []Partition) *Certificate {
	cert := &Certificate{
		Partition:            p,
		Marginals:            margs,
		PartitionsEnumerated: len(all),
		ValidPartitions:      valid,
		FinestNumBlocks:      p.NumBlocks(),
	}
	rhs := make([]string, p.NumBlocks())
	for i, b := range p.Blocks {
		rhs[i] = "P(" + blockVars(b) + ")"
	}
	cert.FullCheck = SplitStep{
		SplitOff: unionOfBlocks(p.Blocks, 0),
		Cells:    t.Size(),
		Equation: "P(" + blockVars(unionOfBlocks(p.Blocks, 0)) + ") = " + strings.Join(rhs, " * "),
	}
	for i := 0; i+1 < p.NumBlocks(); i++ {
		rest := unionOfBlocks(p.Blocks, i)
		cert.Steps = append(cert.Steps, SplitStep{
			SplitOff:  append([]int(nil), p.Blocks[i]...),
			Remaining: unionOfBlocks(p.Blocks, i+1),
			Cells:     tableSizeFor(t, rest),
			Equation: "P(" + blockVars(rest) + ") = P(" + blockVars(p.Blocks[i]) +
				") * P(" + blockVars(unionOfBlocks(p.Blocks, i+1)) + ")",
		})
	}
	return cert
}

func tableSizeFor(t *Table, vars []int) int {
	size := 1
	for _, v := range vars {
		size *= t.Dims[v]
	}
	return size
}

// checkSplit verifies one split step exactly on the marginal table.
func checkSplit(t *Table, step SplitStep) bool {
	vars := append(append([]int(nil), step.SplitOff...), step.Remaining...)
	joint := t.Marginal(vars)
	left := t.Marginal(step.SplitOff)
	right := t.Marginal(step.Remaining)
	sub := Partition{Blocks: [][]int{
		rangeInts(0, len(step.SplitOff)),
		rangeInts(len(step.SplitOff), len(step.SplitOff)+len(step.Remaining)),
	}}
	return productEquals(joint, sub, []*Table{left, right})
}

func rangeInts(lo, hi int) []int {
	out := make([]int, hi-lo)
	for i := range out {
		out[i] = lo + i
	}
	return out
}

func sameRGS(a, b Partition) bool {
	ra, rb := a.RGS(), b.RGS()
	if len(ra) != len(rb) {
		return false
	}
	for i := range ra {
		if ra[i] != rb[i] {
			return false
		}
	}
	return true
}

// VerifyCertificate independently re-checks every claim in c against
// the table, from scratch: the recorded marginals, the full product
// identity, every split step, and the exhaustive finest-ness evidence.
func VerifyCertificate(t *Table, c *Certificate) bool {
	if err := t.Validate(); err != nil {
		return false
	}
	p := c.Partition
	if len(c.Marginals) != p.NumBlocks() {
		return false
	}
	// 1. Recorded marginals must equal freshly computed ones, exactly.
	margs := make([]*Table, p.NumBlocks())
	for i, b := range p.Blocks {
		margs[i] = t.Marginal(b)
		if !tablesEqual(margs[i], c.Marginals[i]) {
			return false
		}
	}
	// 2. Full product identity on every cell (zeros included).
	if !productEquals(t, p, margs) {
		return false
	}
	// 3. Every recorded split step.
	if len(c.Steps) != max(0, p.NumBlocks()-1) {
		return false
	}
	for _, step := range c.Steps {
		if !checkSplit(t, step) {
			return false
		}
	}
	// 4. Finest-ness: re-enumerate all set partitions independently.
	all := AllPartitions(t.NumVars())
	if len(all) != c.PartitionsEnumerated {
		return false
	}
	var valid []Partition
	best := 0
	for _, q := range all {
		if t.Factors(q) {
			valid = append(valid, q)
			if q.NumBlocks() > best {
				best = q.NumBlocks()
			}
		}
	}
	if best != c.FinestNumBlocks || best != p.NumBlocks() {
		return false
	}
	if len(valid) != len(c.ValidPartitions) {
		return false
	}
	for i := range valid {
		if !sameRGS(valid[i], c.ValidPartitions[i]) {
			return false
		}
	}
	// 5. Canonical choice: lexicographically smallest RGS among the
	//    finest valid partitions (enumeration order guarantees the
	//    first max-block valid partition is the canonical one).
	for _, q := range valid {
		if q.NumBlocks() == best {
			return sameRGS(q, p)
		}
	}
	return false
}

func tablesEqual(a, b *Table) bool {
	if len(a.Dims) != len(b.Dims) || len(a.Probs) != len(b.Probs) {
		return false
	}
	for i := range a.Dims {
		if a.Dims[i] != b.Dims[i] {
			return false
		}
	}
	for i := range a.Probs {
		if a.Probs[i].Cmp(b.Probs[i]) != 0 {
			return false
		}
	}
	return true
}

// String renders the certificate as human-readable exact equalities.
func (c *Certificate) String() string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "Finest partition: %s\n", c.Partition)
	fmt.Fprintf(&sb, "Certificate of decomposition (exact rational equalities):\n")
	fmt.Fprintf(&sb, "  [full] %s   -- checked %d/%d cells, exact\n",
		c.FullCheck.Equation, c.FullCheck.Cells, c.FullCheck.Cells)
	for i, s := range c.Steps {
		fmt.Fprintf(&sb, "  [split %d] %s   -- checked %d/%d cells, exact\n",
			i+1, s.Equation, s.Cells, s.Cells)
	}
	fmt.Fprintf(&sb, "Finest-ness proof: all %d set partitions enumerated; %d factor exactly;\n",
		c.PartitionsEnumerated, len(c.ValidPartitions))
	fmt.Fprintf(&sb, "  no valid partition has more than %d blocks.\n", c.FinestNumBlocks)
	var b strings.Builder
	for i, q := range c.ValidPartitions {
		if i > 0 {
			b.WriteString("; ")
		}
		fmt.Fprintf(&b, "%v", q.RGS())
	}
	fmt.Fprintf(&sb, "  valid partitions (RGS, canonical order): %s\n", b.String())
	return sb.String()
}
