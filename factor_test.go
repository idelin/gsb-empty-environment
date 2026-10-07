package factor

import (
	"fmt"
	"math/big"
	"strings"
	"testing"
)

func mustTable(t *testing.T, dims []int, probs []string) *Table {
	t.Helper()
	tb, err := NewTableFromStrings(dims, probs)
	if err != nil {
		t.Fatalf("NewTableFromStrings: %v", err)
	}
	return tb
}

func mustDecompose(t *testing.T, tb *Table) (Partition, *Certificate) {
	t.Helper()
	p, cert, err := Decompose(tb)
	if err != nil {
		t.Fatalf("Decompose: %v", err)
	}
	if err := VerifyCertificate(tb, cert); err != nil {
		t.Fatalf("VerifyCertificate: %v", err)
	}
	return p, cert
}

func TestAllPartitionsBellNumbers(t *testing.T) {
	want := []int{1, 2, 5, 15, 52, 203}
	for n := 1; n <= 6; n++ {
		if got := len(AllPartitions(n)); got != want[n-1] {
			t.Errorf("n=%d: got %d partitions, want Bell(%d)=%d", n, got, n, want[n-1])
		}
	}
}

func TestCanonicalPartitionOrder(t *testing.T) {
	p := NewPartition([][]int{{3, 1}, {2}, {0}})
	want := NewPartition([][]int{{0}, {1, 3}, {2}})
	if !p.Equal(want) {
		t.Fatalf("canonical form: got %s, want %s", p, want)
	}
	if p.String() != "{0}|{1,3}|{2}" {
		t.Fatalf("String: got %s", p)
	}
}

func TestUniformIndependent(t *testing.T) {
	cells := make([]string, 8)
	for i := range cells {
		cells[i] = "1/8"
	}
	tb := mustTable(t, []int{2, 2, 2}, cells)
	p, _ := mustDecompose(t, tb)
	if !p.Equal(DiscretePartition(3)) {
		t.Fatalf("got %s, want fully independent singletons", p)
	}
}

func TestCorrelatedPairPlusIndependent(t *testing.T) {
	// (x0,x1) perfectly correlated, x2 uniform independent.
	tb := mustTable(t, []int{2, 2, 2},
		[]string{"1/4", "1/4", "0", "0", "0", "0", "1/4", "1/4"})
	p, _ := mustDecompose(t, tb)
	want := NewPartition([][]int{{0, 1}, {2}})
	if !p.Equal(want) {
		t.Fatalf("got %s, want %s", p, want)
	}
}

// Zero-probability cells in an independent table: the factorization check
// must verify 0 = marginal*marginal directly, never dividing by a zero
// marginal or skipping the cell.
func TestZeroCellsIndependent(t *testing.T) {
	// P_X = (1/2, 1/2, 0), P_Y = (1/3, 2/3); joint is the outer product.
	tb := mustTable(t, []int{3, 2},
		[]string{"1/6", "1/3", "1/6", "1/3", "0", "0"})
	p, cert := mustDecompose(t, tb)
	if !p.Equal(DiscretePartition(2)) {
		t.Fatalf("got %s, want independence", p)
	}
	// The zero cells must appear as ordinary exact equations 0 = 0*... .
	zeros := 0
	for _, eq := range cert.Equations {
		if eq.Joint.Sign() == 0 {
			zeros++
			if !eq.Holds() {
				t.Fatalf("zero-cell equation does not hold: %v", eq.Assignment)
			}
		}
	}
	if zeros != 2 {
		t.Fatalf("expected 2 zero-cell equations, got %d", zeros)
	}
}

// Zero cells inside a dependent block that is itself independent of a
// third variable: zeros must not break the split certificate either.
func TestZeroCellsInsideBlock(t *testing.T) {
	// A(x0,x1) = [[1/2,0],[1/4,1/4]] (rank 2, has a zero), x2 uniform.
	tb := mustTable(t, []int{2, 2, 2},
		[]string{"1/4", "1/4", "0", "0", "1/8", "1/8", "1/8", "1/8"})
	p, _ := mustDecompose(t, tb)
	want := NewPartition([][]int{{0, 1}, {2}})
	if !p.Equal(want) {
		t.Fatalf("got %s, want %s", p, want)
	}
}

func TestZeroCellsDependent(t *testing.T) {
	// Perfectly correlated bits: zeros off-diagonal, not independent.
	tb := mustTable(t, []int{2, 2}, []string{"1/2", "0", "0", "1/2"})
	p, _ := mustDecompose(t, tb)
	if !p.Equal(OneBlockPartition(2)) {
		t.Fatalf("got %s, want single block", p)
	}
}

// Marginal (pairwise) independence does not imply mutual independence:
// X, Y uniform bits and Z = X xor Y are pairwise independent, but the
// joint does not equal the product of the three singleton marginals, so
// the finest partition keeps all three variables in one block.
func TestPairwiseIndependentNotMutual(t *testing.T) {
	tb := mustTable(t, []int{2, 2, 2},
		[]string{"1/4", "0", "0", "1/4", "0", "1/4", "1/4", "0"})
	// Every pair marginal is the uniform product: pairwise independent.
	for _, pair := range [][]int{{0, 1}, {0, 2}, {1, 2}} {
		m := tb.Marginal(pair)
		for _, c := range m.Cells {
			if c.Cmp(big.NewRat(1, 4)) != 0 {
				t.Fatalf("pair %v not uniformly independent", pair)
			}
		}
	}
	// But no split is valid; the finest partition is the single block.
	p, _ := mustDecompose(t, tb)
	if !p.Equal(OneBlockPartition(3)) {
		t.Fatalf("got %s, want single block (pairwise != mutual)", p)
	}
	if IsProductForm(tb, DiscretePartition(3)) {
		t.Fatal("joint must not equal product of singleton marginals")
	}
}

func TestSixVariablesMixedBlocks(t *testing.T) {
	// Blocks {0,2}, {1}, {3,4,5}; joint = A(x0,x2) * B(x1) * C(x3,x4,x5).
	dims := []int{2, 3, 2, 4, 2, 3}
	a := [][]string{{"1/3", "1/12"}, {"1/4", "1/3"}} // rank 2
	b := []string{"1/2", "1/3", "1/6"}
	// C[x3][x4][x5] = (x3 + 2*x4 + 3*x5 + 1) / 156: additive, non-factorable.
	rat := func(num, den int64) *big.Rat { return big.NewRat(num, den) }
	cWeight := func(x3, x4, x5 int) *big.Rat {
		return rat(int64(x3+2*x4+3*x5+1), 156)
	}
	size := 1
	for _, d := range dims {
		size *= d
	}
	cells := make([]*big.Rat, size)
	tmp := &Table{Dims: dims}
	for flat := 0; flat < size; flat++ {
		as := tmp.Assignment(flat)
		av, _ := new(big.Rat).SetString(a[as[0]][as[2]])
		bv, _ := new(big.Rat).SetString(b[as[1]])
		cv := cWeight(as[3], as[4], as[5])
		cells[flat] = new(big.Rat).Mul(av, new(big.Rat).Mul(bv, cv))
	}
	tb, err := NewTable(dims, cells)
	if err != nil {
		t.Fatalf("NewTable: %v", err)
	}
	p, cert := mustDecompose(t, tb)
	want := NewPartition([][]int{{0, 2}, {1}, {3, 4, 5}})
	if !p.Equal(want) {
		t.Fatalf("got %s, want %s", p, want)
	}
	if len(cert.Splits) != 3 {
		t.Fatalf("want 3 split certificates, got %d", len(cert.Splits))
	}
	s := cert.String()
	if !strings.Contains(s, "partition: {0,2}|{1}|{3,4,5}") {
		t.Fatalf("certificate string missing partition header:\n%s", s)
	}
}

func TestSingletonDomainVariable(t *testing.T) {
	tb := mustTable(t, []int{1, 2}, []string{"1/2", "1/2"})
	p, _ := mustDecompose(t, tb)
	if !p.Equal(DiscretePartition(2)) {
		t.Fatalf("got %s, want singletons", p)
	}
}

func TestCertificateTamperDetected(t *testing.T) {
	tb := mustTable(t, []int{2, 2}, []string{"1/4", "1/4", "1/4", "1/4"})
	_, cert, err := Decompose(tb)
	if err != nil {
		t.Fatalf("Decompose: %v", err)
	}
	if err := VerifyCertificate(tb, cert); err != nil {
		t.Fatalf("honest certificate rejected: %v", err)
	}
	// Tamper with one factor; verification must fail.
	cert.Equations[0].Factors[0] = big.NewRat(1, 3)
	if err := VerifyCertificate(tb, cert); err == nil {
		t.Fatal("tampered certificate accepted")
	}
}

func TestVerifyFinestRejectsCoarseClaim(t *testing.T) {
	tb := mustTable(t, []int{2, 2}, []string{"1/4", "1/4", "1/4", "1/4"})
	// The one-block partition is valid but not finest here.
	if err := VerifyFinest(tb, OneBlockPartition(2)); err == nil {
		t.Fatal("VerifyFinest accepted a non-finest partition")
	}
	if err := VerifyFinest(tb, DiscretePartition(2)); err != nil {
		t.Fatalf("VerifyFinest rejected the finest partition: %v", err)
	}
}

func TestInvalidInputs(t *testing.T) {
	if _, err := NewTable([]int{2, 2, 2, 2, 2, 2, 2}, nil); err == nil {
		t.Error("accepted 7 variables")
	}
	if _, err := NewTable([]int{5}, nil); err == nil {
		t.Error("accepted domain size 5")
	}
	if _, err := NewTableFromStrings([]int{2}, []string{"1/2", "1/3"}); err == nil {
		t.Error("accepted cells not summing to 1")
	}
	if _, err := NewTableFromStrings([]int{2}, []string{"3/2", "-1/2"}); err == nil {
		t.Error("accepted negative cell")
	}
	if _, err := NewTableFromStrings([]int{2}, []string{"1/2"}); err == nil {
		t.Error("accepted wrong cell count")
	}
}

func TestSingleVariable(t *testing.T) {
	tb := mustTable(t, []int{3}, []string{"1/2", "1/4", "1/4"})
	p, _ := mustDecompose(t, tb)
	if !p.Equal(OneBlockPartition(1)) {
		t.Fatalf("got %s, want single block", p)
	}
}

func ExampleDecompose() {
	// (x0,x1) perfectly correlated, x2 uniform and independent.
	tb, _ := NewTableFromStrings([]int{2, 2, 2},
		[]string{"1/4", "1/4", "0", "0", "0", "0", "1/4", "1/4"})
	p, cert, _ := Decompose(tb)
	fmt.Println("finest partition:", p)
	eq := cert.Equations[0]
	fmt.Printf("certificate eq: P%v = %s = %s * %s\n",
		eq.Assignment, eq.Joint.RatString(), eq.Factors[0].RatString(), eq.Factors[1].RatString())
	// Output:
	// finest partition: {0,1}|{2}
	// certificate eq: P[0 0 0] = 1/4 = 1/2 * 1/2
}
