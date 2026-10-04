package provenance

import (
	"math/big"
	"strings"
	"testing"
)

func mustPoly(t *testing.T, p *Poly, want string) {
	t.Helper()
	if got := p.String(); got != want {
		t.Fatalf("got %s, want %s", got, want)
	}
}

func TestBaseRowsGetIndependentSymbols(t *testing.T) {
	alloc := NewVarAllocator()
	r := NewBaseRelation(alloc, "R", []string{"A"}, [][]Value{{1}, {1}, {2}})
	if len(r.Rows) != 3 {
		t.Fatalf("want 3 rows, got %d", len(r.Rows))
	}
	seen := map[uint64]bool{}
	for _, row := range r.Rows {
		vars := row.Prov.Vars()
		if len(vars) != 1 || len(row.Prov.Terms) != 1 {
			t.Fatalf("base row annotation must be a single variable, got %s", row.Prov)
		}
		if seen[vars[0]] {
			t.Fatalf("variable x%d reused across base rows", vars[0])
		}
		seen[vars[0]] = true
	}
}

func TestMulCombinesLikeTerms(t *testing.T) {
	// (x1 + x2)^2 = x1^2 + 2*x1*x2 + x2^2
	p := VarPoly(1).Add(VarPoly(2))
	got := p.Mul(p)
	if c := got.Terms[VarMonomial(1).Mul(VarMonomial(2)).Key()].Coef; c.Cmp(big.NewInt(2)) != 0 {
		t.Fatalf("x1*x2 coefficient = %s, want 2 (duplicate contributions must be kept)", c)
	}
	mustPoly(t, got, "2*x1*x2 + x1^2 + x2^2")
}

func TestBigIntegerCoefficients(t *testing.T) {
	// Coefficients far beyond int64 must be exact.
	pow := func(e uint) *big.Int { return new(big.Int).Lsh(big.NewInt(1), e) }
	p := ConstPoly(pow(100)).Mul(VarPoly(1)) // 2^100 * x1
	q := ConstPoly(pow(100)).Mul(VarPoly(1)) // 2^100 * x1
	sum := p.Add(q)
	if c := sum.Terms["x1"].Coef; c.Cmp(pow(101)) != 0 {
		t.Fatalf("coefficient = %s, want 2^101", c)
	}
	prod := p.Mul(q) // 2^200 * x1^2
	if c := prod.Terms["x1^2"].Coef; c.Cmp(pow(200)) != 0 {
		t.Fatalf("coefficient = %s, want 2^200", c)
	}
}

func TestProjectionKeepsMultisetContributions(t *testing.T) {
	alloc := NewVarAllocator()
	// Three tuples projecting onto A=1, one onto A=2.
	r := NewBaseRelation(alloc, "R", []string{"A", "B"}, [][]Value{
		{1, "a"}, {1, "b"}, {1, "a"}, {2, "c"},
	})
	out := Project(r, 0)
	if len(out.Rows) != 2 {
		t.Fatalf("want 2 distinct output rows, got %d", len(out.Rows))
	}
	byVal := map[any]*Poly{}
	for _, row := range out.Rows {
		byVal[row.Vals[0]] = row.Prov
	}
	// A=1 must be x1 + x2 + x3: three distinct witnesses, none dropped.
	mustPoly(t, byVal[1], "x1 + x2 + x3")
	mustPoly(t, byVal[2], "x4")
}

func TestUnionSumsProvenance(t *testing.T) {
	alloc := NewVarAllocator()
	r := NewBaseRelation(alloc, "R", []string{"A"}, [][]Value{{1}, {2}})
	s := NewBaseRelation(alloc, "S", []string{"A"}, [][]Value{{1}, {1}})
	out := Union(r, s)
	byVal := map[any]*Poly{}
	for _, row := range out.Rows {
		byVal[row.Vals[0]] = row.Prov
	}
	// Tuple (1) has one witness in R and two in S.
	mustPoly(t, byVal[1], "x1 + x3 + x4")
	mustPoly(t, byVal[2], "x2")
}

func TestJoinMultipliesProvenance(t *testing.T) {
	alloc := NewVarAllocator()
	r := NewBaseRelation(alloc, "R", []string{"A", "B"}, [][]Value{
		{1, "x"}, {1, "y"}, {2, "x"},
	})
	s := NewBaseRelation(alloc, "S", []string{"B", "C"}, [][]Value{
		{"x", 10}, {"x", 20},
	})
	out := Join(r, s, []int{1}, []int{0})
	byKey := map[string]*Poly{}
	for _, row := range out.Rows {
		byKey[rowKey(row.Vals)] = row.Prov
	}
	// (1,"x",10) = x1*x4 ; (1,"x",20) = x1*x5 ; (2,"x",10) = x3*x4 ; (2,"x",20) = x3*x5
	mustPoly(t, byKey[rowKey([]Value{1, "x", 10})], "x1*x4")
	mustPoly(t, byKey[rowKey([]Value{1, "x", 20})], "x1*x5")
	mustPoly(t, byKey[rowKey([]Value{2, "x", 10})], "x3*x4")
	mustPoly(t, byKey[rowKey([]Value{2, "x", 20})], "x3*x5")
	if len(out.Rows) != 4 {
		t.Fatalf("want 4 join results, got %d", len(out.Rows))
	}
}

func TestJoinThenProjectCombinesAcrossRows(t *testing.T) {
	alloc := NewVarAllocator()
	// Duplicate join partners collapse onto the same projected tuple.
	r := NewBaseRelation(alloc, "R", []string{"A", "B"}, [][]Value{{1, "k"}})
	s := NewBaseRelation(alloc, "S", []string{"B", "C"}, [][]Value{{"k", 7}, {"k", 7}})
	out := Project(Join(r, s, []int{1}, []int{0}), 0)
	if len(out.Rows) != 1 {
		t.Fatalf("want 1 output row, got %d", len(out.Rows))
	}
	// Two identical S tuples are two independent witnesses: x1*x2 + x1*x3.
	mustPoly(t, out.Rows[0].Prov, "x1*x2 + x1*x3")
}

// buildComposedQuery builds a query mixing union, join and projection:
//
//	out = π_A( (R ⋈_B S) ∪ T )
func buildComposedQuery(R, S, T *Relation) *Relation {
	return Project(Union(Join(R, S, []int{1}, []int{0}), T), 0)
}

func composedInputs() (*Relation, *Relation, *Relation) {
	alloc := NewVarAllocator()
	R := NewBaseRelation(alloc, "R", []string{"A", "B"}, [][]Value{
		{1, "x"}, {1, "y"}, {2, "x"}, {1, "x"}, // x1..x4 (note the duplicate)
	})
	S := NewBaseRelation(alloc, "S", []string{"B", "C"}, [][]Value{
		{"x", 10}, {"x", 10}, {"y", 20}, // x5..x7
	})
	T := NewBaseRelation(alloc, "T", []string{"A"}, [][]Value{
		{1}, {3}, // x8, x9
	})
	return R, S, T
}

func TestDeleteVarMatchesDirectRecompute(t *testing.T) {
	R, S, T := composedInputs()
	out := buildComposedQuery(R, S, T)

	// Collect every base variable.
	vars := map[uint64]bool{}
	for _, rel := range []*Relation{R, S, T} {
		for _, row := range rel.Rows {
			vars[row.Prov.Vars()[0]] = true
		}
	}

	for id := uint64(1); id <= 9; id++ {
		if !vars[id] {
			continue
		}
		incremental, deltas := DeleteVar(out, id)
		recomputed := buildComposedQuery(WithoutVar(R, id), WithoutVar(S, id), WithoutVar(T, id))
		if !EqualRelation(incremental, recomputed) {
			t.Fatalf("deleting x%d:\nincremental:\n%s\nrecomputed:\n%s", id, incremental, recomputed)
		}
		// Every delta must only remove monomials containing x%d.
		for _, d := range deltas {
			for _, term := range d.Delta.Terms {
				if term.Coef.Sign() > 0 {
					t.Fatalf("deleting x%d: delta %s has positive term", id, d.Delta)
				}
				if !term.Mon.HasVar(id) {
					t.Fatalf("deleting x%d: delta %s mentions unrelated monomial", id, d.Delta)
				}
			}
		}
	}
}

func TestDeleteVarSpecificResult(t *testing.T) {
	R, S, T := composedInputs()
	out := buildComposedQuery(R, S, T)

	// Full provenance of A=1 before any deletion:
	// join side: (x1+x4)*(x5+x6) [B="x"] + x2*x7 [B="y"], union side: x8.
	var a1 *Poly
	for _, row := range out.Rows {
		if row.Vals[0] == 1 {
			a1 = row.Prov
		}
	}
	mustPoly(t, a1, "x8 + x1*x5 + x1*x6 + x2*x7 + x4*x5 + x4*x6")

	// Delete x5 (one of the duplicated S rows): only its monomials vanish.
	after, deltas := DeleteVar(out, 5)
	var a1New *Poly
	for _, row := range after.Rows {
		if row.Vals[0] == 1 {
			a1New = row.Prov
		}
	}
	mustPoly(t, a1New, "x8 + x1*x6 + x2*x7 + x4*x6")
	// x5 also witnesses A=2 via (2,"x") ⋈ ("x",10), so two rows are affected.
	if len(deltas) != 2 {
		t.Fatalf("want exactly 2 affected output rows, got %d", len(deltas))
	}
	for _, d := range deltas {
		if d.Vals[0] == 1 {
			mustPoly(t, d.Delta, "-x1*x5 - x4*x5")
		}
	}

	// Delete x8 (the only union-side witness of A=1): its constant term dies.
	after2, _ := DeleteVar(out, 8)
	for _, row := range after2.Rows {
		if row.Vals[0] == 1 {
			if strings.Contains(row.Prov.String(), "x8") {
				t.Fatalf("x8 must be gone, got %s", row.Prov)
			}
		}
	}

	// Delete x9: A=3 disappears entirely from the output.
	after3, _ := DeleteVar(out, 9)
	for _, row := range after3.Rows {
		if row.Vals[0] == 3 {
			t.Fatalf("tuple (3) must vanish after deleting x9")
		}
	}
}
