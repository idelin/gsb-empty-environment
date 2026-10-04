package provenance

import (
	"math/big"
	"testing"
)

func mustProv(t *testing.T, r *Relation, want string, values ...string) {
	t.Helper()
	p, ok := r.Lookup(values...)
	if !ok {
		t.Fatalf("tuple %v not found in result", values)
	}
	if got := p.String(); got != want {
		t.Fatalf("tuple %v: got %s, want %s", values, got, want)
	}
}

func TestMultisetProjectionAddsDuplicates(t *testing.T) {
	r := NewBaseRelation([]string{"A", "B"}, []BaseRow{
		{ID: "r1", Values: []string{"1", "x"}},
		{ID: "r2", Values: []string{"1", "y"}},
		{ID: "r3", Values: []string{"1", "x"}},
		{ID: "r4", Values: []string{"2", "x"}},
	})
	p := r.Project("A")
	mustProv(t, p, "r1 + r2 + r3", "1")
	mustProv(t, p, "r4", "2")
}

func TestUnionAddsPolynomials(t *testing.T) {
	r := NewBaseRelation([]string{"A"}, []BaseRow{
		{ID: "r1", Values: []string{"1"}},
		{ID: "r2", Values: []string{"2"}},
	})
	s := NewBaseRelation([]string{"A"}, []BaseRow{
		{ID: "s1", Values: []string{"1"}},
	})
	u := r.Union(s)
	mustProv(t, u, "r1 + s1", "1")
	mustProv(t, u, "r2", "2")

	// Multiset union with itself doubles every coefficient.
	uu := r.Union(r)
	mustProv(t, uu, "2*r1", "1")
	mustProv(t, uu, "2*r2", "2")
}

func TestJoinMultipliesPolynomials(t *testing.T) {
	r := NewBaseRelation([]string{"A", "B"}, []BaseRow{
		{ID: "r1", Values: []string{"1", "x"}},
		{ID: "r2", Values: []string{"1", "y"}},
	})
	s := NewBaseRelation([]string{"B", "C"}, []BaseRow{
		{ID: "s1", Values: []string{"x", "u"}},
		{ID: "s2", Values: []string{"x", "v"}},
	})
	j := r.Join(s)
	if len(j.Schema) != 3 || j.Schema[0] != "A" || j.Schema[1] != "B" || j.Schema[2] != "C" {
		t.Fatalf("unexpected join schema %v", j.Schema)
	}
	mustProv(t, j, "r1*s1", "1", "x", "u")
	mustProv(t, j, "r1*s2", "1", "x", "v")
	if _, ok := j.Lookup("1", "y", "u"); ok {
		t.Fatalf("unexpected join tuple")
	}

	// Self-join on A squares the symbol of a row joined with itself.
	jj := r.Project("A").Join(r.Project("A"))
	mustProv(t, jj, "2*r1*r2 + r1^2 + r2^2", "1")
}

func TestProjectionAfterJoinCombinesLikeTerms(t *testing.T) {
	// Two different B values collapse under projection; the join products
	// share monomials only when the same base rows are involved.
	r := NewBaseRelation([]string{"A", "B"}, []BaseRow{
		{ID: "r1", Values: []string{"1", "x"}},
		{ID: "r2", Values: []string{"1", "y"}},
	})
	s := NewBaseRelation([]string{"B", "C"}, []BaseRow{
		{ID: "s1", Values: []string{"x", "u"}},
		{ID: "s2", Values: []string{"y", "u"}},
	})
	q := r.Join(s).Project("A", "C")
	mustProv(t, q, "r1*s1 + r2*s2", "1", "u")
}

func TestBigIntegerCoefficients(t *testing.T) {
	// 2^80 exceeds int64; coefficients must stay exact.
	p := Var("x")
	for i := 0; i < 80; i++ {
		p = p.Add(p)
	}
	want := new(big.Int).Lsh(big.NewInt(1), 80)
	if p.Coefficient("x").Cmp(want) != 0 {
		t.Fatalf("got %s, want %s", p.Coefficient("x"), want)
	}
	// Multiplication combines exponents and multiplies big coefficients.
	q := p.Mul(Var("x"))
	if q.Coefficient("x^2").Cmp(want) != 0 {
		t.Fatalf("got %s, want %s", q.Coefficient("x^2"), want)
	}
}

// query builds the test query: project(A, C) of R join S, unioned with T.
func query(rRows, sRows, tRows []BaseRow) *Relation {
	r := NewBaseRelation([]string{"A", "B"}, rRows)
	s := NewBaseRelation([]string{"B", "C"}, sRows)
	tr := NewBaseRelation([]string{"A", "C"}, tRows)
	return r.Join(s).Project("A", "C").Union(tr)
}

func baseRows() (r, s, tr []BaseRow) {
	r = []BaseRow{
		{ID: "r1", Values: []string{"1", "x"}},
		{ID: "r2", Values: []string{"1", "y"}},
		{ID: "r3", Values: []string{"2", "x"}},
	}
	s = []BaseRow{
		{ID: "s1", Values: []string{"x", "u"}},
		{ID: "s2", Values: []string{"x", "v"}},
		{ID: "s3", Values: []string{"y", "u"}},
	}
	tr = []BaseRow{
		{ID: "t1", Values: []string{"1", "u"}},
	}
	return
}

func dropRow(rows []BaseRow, id string) []BaseRow {
	out := make([]BaseRow, 0, len(rows))
	for _, row := range rows {
		if row.ID != id {
			out = append(out, row)
		}
	}
	return out
}

func asMap(r *Relation) map[string]Poly {
	m := make(map[string]Poly)
	for _, row := range r.Rows {
		m[tupleKey(row.Values)] = row.Prov
	}
	return m
}

func TestDeleteDeltaMatchesRecomputation(t *testing.T) {
	rRows, sRows, tRows := baseRows()
	full := query(rRows, sRows, tRows)

	// Sanity: the tuple (1,u) is derivable via r1*s1, r2*s3 and t1.
	mustProv(t, full, "r1*s1 + r2*s3 + t1", "1", "u")

	symbols := []string{"r1", "r2", "r3", "s1", "s2", "s3", "t1"}
	for _, sym := range symbols {
		incremental := asMap(full.Delete(sym))
		recomputed := asMap(query(
			dropRow(rRows, sym), dropRow(sRows, sym), dropRow(tRows, sym)))

		if len(incremental) != len(recomputed) {
			t.Fatalf("delete %s: incremental has %d tuples, recomputed %d", sym, len(incremental), len(recomputed))
		}
		for k, p := range recomputed {
			if !incremental[k].Equal(p) {
				t.Fatalf("delete %s: tuple %q incremental %s != recomputed %s", sym, k, incremental[k], p)
			}
		}

		// The per-row delta must equal full minus recomputed.
		deltas := full.DeleteDelta(sym)
		reMap := recomputed
		for i, row := range full.Rows {
			want := row.Prov.Sub(reMap[tupleKey(row.Values)])
			if !deltas[i].Equal(want) {
				t.Fatalf("delete %s: tuple %v delta %s != full-recomputed %s", sym, row.Values, deltas[i], want)
			}
		}
	}
}

func TestDeleteDeltaContents(t *testing.T) {
	rRows, sRows, tRows := baseRows()
	full := query(rRows, sRows, tRows)

	deltas := full.DeleteDelta("r1")
	var found bool
	for i, row := range full.Rows {
		if row.Values[0] == "1" && row.Values[1] == "u" {
			found = true
			if got := deltas[i].String(); got != "r1*s1" {
				t.Fatalf("delta for (1,u): got %s, want r1*s1", got)
			}
		}
	}
	if !found {
		t.Fatal("tuple (1,u) missing")
	}

	// Deleting t1 only affects the (1,u) tuple.
	after := full.Delete("t1")
	mustProv(t, after, "r1*s1 + r2*s3", "1", "u")
}
