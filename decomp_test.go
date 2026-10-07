package decomp

import (
	"math/big"
	"testing"
)

func rat(s string) *big.Rat {
	r, ok := new(big.Rat).SetString(s)
	if !ok {
		panic("bad rational: " + s)
	}
	return r
}

func rats(ss ...string) []*big.Rat {
	out := make([]*big.Rat, len(ss))
	for i, s := range ss {
		out[i] = rat(s)
	}
	return out
}

func mustTable(t *testing.T, dims []int, probs []*big.Rat) *Table {
	t.Helper()
	tab, err := NewTable(dims, probs)
	if err != nil {
		t.Fatalf("NewTable: %v", err)
	}
	return tab
}

func wantPartition(t *testing.T, tab *Table, wantRGS []int) *Result {
	t.Helper()
	res, err := tab.Decompose()
	if err != nil {
		t.Fatalf("Decompose: %v", err)
	}
	got := res.Partition.RGS()
	if len(got) != len(wantRGS) {
		t.Fatalf("partition RGS = %v, want %v", got, wantRGS)
	}
	for i := range got {
		if got[i] != wantRGS[i] {
			t.Fatalf("partition RGS = %v, want %v", got, wantRGS)
		}
	}
	if !VerifyCertificate(tab, res.Certificate) {
		t.Fatalf("certificate failed independent verification")
	}
	return res
}

func TestBellNumbers(t *testing.T) {
	bell := map[int]int{1: 1, 2: 2, 3: 5, 4: 15, 5: 52, 6: 203}
	for n, want := range bell {
		if got := len(AllPartitions(n)); got != want {
			t.Errorf("AllPartitions(%d) = %d, want Bell(%d) = %d", n, got, n, want)
		}
	}
}

func TestUniformThreeVarsFullyIndependent(t *testing.T) {
	probs := make([]*big.Rat, 8)
	for i := range probs {
		probs[i] = rat("1/8")
	}
	tab := mustTable(t, []int{2, 2, 2}, probs)
	res := wantPartition(t, tab, []int{0, 1, 2})
	if res.Certificate.PartitionsEnumerated != 5 {
		t.Fatalf("enumerated %d partitions, want Bell(3) = 5",
			res.Certificate.PartitionsEnumerated)
	}
}

// XOR distribution: uniform over {000, 011, 101, 110}. Every pair of
// variables is marginally independent, yet the three are not mutually
// independent. The finest partition must stay a single block.
func TestXORMarginalIndependenceIsNotMutual(t *testing.T) {
	probs := rats("1/4", "0", "0", "1/4", "0", "1/4", "1/4", "0")
	tab := mustTable(t, []int{2, 2, 2}, probs)

	// Sanity: every pair marginal is the product of singleton marginals.
	for _, pair := range [][]int{{0, 1}, {0, 2}, {1, 2}} {
		other := 3 - pair[0] - pair[1]
		p := Partition{Blocks: [][]int{pair, {other}}}
		if tab.Factors(p) {
			t.Fatalf("pair %v unexpectedly separated from X%d", pair, other)
		}
		pm := tab.Marginal(pair)
		for _, q := range pm.Probs {
			if q.Cmp(rat("1/4")) != 0 {
				t.Fatalf("pair %v marginal not uniform: %s", pair, q)
			}
		}
	}
	wantPartition(t, tab, []int{0, 0, 0})
}

// A constant variable makes conditional probabilities 0/0 undefined;
// the product-of-marginals test must still isolate it as a singleton.
func TestZeroCellsConstantVariable(t *testing.T) {
	// X1 is deterministically 0; X0, X2 fair and independent.
	// Cells with x1=1 have probability 0.
	probs := rats("1/4", "1/4", "0", "0", "1/4", "1/4", "0", "0")
	tab := mustTable(t, []int{2, 2, 2}, probs)
	wantPartition(t, tab, []int{0, 1, 2})
}

// Perfectly correlated pair (zeros on the off-diagonal) times an
// independent fair variable: finest partition is {X0,X1}|{X2}.
func TestCorrelatedPairWithZeros(t *testing.T) {
	probs := rats("1/4", "1/4", "0", "0", "0", "0", "1/4", "1/4")
	tab := mustTable(t, []int{2, 2, 2}, probs)
	res := wantPartition(t, tab, []int{0, 0, 1})
	if len(res.Certificate.Steps) != 1 {
		t.Fatalf("expected 1 split step, got %d", len(res.Certificate.Steps))
	}
	step := res.Certificate.Steps[0]
	if step.Cells != 8 {
		t.Fatalf("split step checked %d cells, want 8", step.Cells)
	}
}

// Non-uniform rationals: P = f(X0) * g(X1,X2) with dependent (X1,X2).
func TestNonUniformRationalFactor(t *testing.T) {
	f := rats("1/3", "2/3")
	g := rats("1/8", "1/8", "1/4", "1/2") // (x1,x2), x2 fastest; g00*g11 != g01*g10
	probs := make([]*big.Rat, 8)
	for x0 := 0; x0 < 2; x0++ {
		for x1 := 0; x1 < 2; x1++ {
			for x2 := 0; x2 < 2; x2++ {
				p := new(big.Rat).Mul(f[x0], g[x1*2+x2])
				probs[x0*4+x1*2+x2] = p
			}
		}
	}
	tab := mustTable(t, []int{2, 2, 2}, probs)
	res := wantPartition(t, tab, []int{0, 1, 1})
	if got := res.Marginals[0].Probs[1]; got.Cmp(rat("2/3")) != 0 {
		t.Fatalf("block marginal = %s, want 2/3", got)
	}
}

// Six independent variables with distinct non-uniform marginals must
// split into six singleton blocks; all 203 partitions are checked.
func TestSixIndependentVars(t *testing.T) {
	dims := []int{2, 2, 3, 2, 4, 2}
	margs := [][]*big.Rat{
		rats("1/2", "1/2"),
		rats("1/3", "2/3"),
		rats("1/6", "1/3", "1/2"),
		rats("1/4", "3/4"),
		rats("1/4", "1/4", "1/4", "1/4"),
		rats("2/5", "3/5"),
	}
	size := 1
	for _, d := range dims {
		size *= d
	}
	probs := make([]*big.Rat, size)
	for flat := 0; flat < size; flat++ {
		idx, p := flat, new(big.Rat).SetInt64(1)
		for v := len(dims) - 1; v >= 0; v-- {
			p.Mul(p, margs[v][idx%dims[v]])
			idx /= dims[v]
		}
		probs[flat] = p
	}
	tab := mustTable(t, dims, probs)
	res := wantPartition(t, tab, []int{0, 1, 2, 3, 4, 5})
	if res.Certificate.PartitionsEnumerated != 203 {
		t.Fatalf("enumerated %d partitions, want Bell(6) = 203",
			res.Certificate.PartitionsEnumerated)
	}
	if len(res.Certificate.Steps) != 5 {
		t.Fatalf("expected 5 split steps, got %d", len(res.Certificate.Steps))
	}
}

// A fully dependent table admits only the trivial one-block partition.
func TestFullyDependent(t *testing.T) {
	probs := rats("1/2", "0", "0", "0", "0", "0", "0", "1/2")
	tab := mustTable(t, []int{2, 2, 2}, probs)
	res := wantPartition(t, tab, []int{0, 0, 0})
	if len(res.Certificate.ValidPartitions) != 1 {
		t.Fatalf("valid partitions = %d, want 1", len(res.Certificate.ValidPartitions))
	}
}

func TestCertificateTamperingDetected(t *testing.T) {
	probs := make([]*big.Rat, 8)
	for i := range probs {
		probs[i] = rat("1/8")
	}
	tab := mustTable(t, []int{2, 2, 2}, probs)
	res, err := tab.Decompose()
	if err != nil {
		t.Fatalf("Decompose: %v", err)
	}
	// Tamper: corrupt one recorded marginal entry.
	bad := *res.Certificate
	badMarg := NewTableMustForTest(res.Certificate.Marginals[0])
	badMarg.Probs[0] = rat("1/3")
	bad.Marginals = append([]*Table(nil), res.Certificate.Marginals...)
	bad.Marginals[0] = badMarg
	if VerifyCertificate(tab, &bad) {
		t.Fatalf("tampered certificate passed verification")
	}
	// Tamper: claim a coarser partition is the finest.
	bad2 := *res.Certificate
	bad2.Partition = Partition{Blocks: [][]int{{0, 1}, {2}}}
	bad2.Marginals = bad2.Marginals[:2]
	if VerifyCertificate(tab, &bad2) {
		t.Fatalf("coarser-partition certificate passed verification")
	}
}

// NewTableMustForTest deep-copies a table for tampering tests.
func NewTableMustForTest(src *Table) *Table {
	probs := make([]*big.Rat, len(src.Probs))
	for i, p := range src.Probs {
		probs[i] = new(big.Rat).Set(p)
	}
	return &Table{Dims: append([]int(nil), src.Dims...), Probs: probs}
}

func TestInvalidTablesRejected(t *testing.T) {
	if _, err := NewTable([]int{2, 2, 2, 2, 2, 2, 2}, make([]*big.Rat, 128)); err == nil {
		t.Errorf("accepted 7 variables")
	}
	if _, err := NewTable([]int{5}, make([]*big.Rat, 5)); err == nil {
		t.Errorf("accepted domain size 5")
	}
	tab := mustTable(t, []int{2}, rats("1/4", "1/4"))
	if _, err := tab.Decompose(); err == nil {
		t.Errorf("accepted table not summing to 1")
	}
	tab = mustTable(t, []int{2}, rats("5/4", "-1/4"))
	if _, err := tab.Decompose(); err == nil {
		t.Errorf("accepted negative probability")
	}
}

// Determinism: repeated runs return the identical canonical partition.
func TestCanonicalDeterminism(t *testing.T) {
	probs := rats("1/4", "1/4", "0", "0", "0", "0", "1/4", "1/4")
	tab := mustTable(t, []int{2, 2, 2}, probs)
	r1, err := tab.Decompose()
	if err != nil {
		t.Fatal(err)
	}
	r2, err := tab.Decompose()
	if err != nil {
		t.Fatal(err)
	}
	if !sameRGS(r1.Partition, r2.Partition) {
		t.Fatalf("non-deterministic partition: %v vs %v",
			r1.Partition.RGS(), r2.Partition.RGS())
	}
}
