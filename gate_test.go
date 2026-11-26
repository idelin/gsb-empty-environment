package admission

import (
	"errors"
	"math/big"
	"testing"
)

func vars6(names ...string) []Variable {
	out := make([]Variable, len(names))
	for i, n := range names {
		out[i] = Variable{Name: n, Modulus: big.NewInt(6)}
	}
	return out
}

func form(name string, mod int64, terms ...Term) ProtectedForm {
	return ProtectedForm{Name: name, Modulus: big.NewInt(mod), Terms: terms}
}

func term(v string, c int64) Term {
	return Term{Variable: v, Coefficient: big.NewInt(c)}
}

func TestAdmitExactJointShrink(t *testing.T) {
	cfg := Config{
		Variables: vars6("a", "b"),
		Policies: []CollusionPolicy{{
			Name:      "ab",
			Protected: []ProtectedForm{form("a", 6, term("a", 1)), form("b", 6, term("b", 1))},
			MaxShrink: Shrink(6, 1),
		}},
	}
	g, err := NewGate(cfg)
	if err != nil {
		t.Fatal(err)
	}
	rep, err := g.Admit(&Batch{ID: "b1", Releases: []ReleaseRow{{
		ID: "r1", Modulus: big.NewInt(6), Terms: []Term{term("a", 1), term("b", 1)}, Value: big.NewInt(0),
	}}})
	if err != nil {
		t.Fatal(err)
	}
	if !rep.Admitted || !rep.Committed {
		t.Fatalf("batch not admitted: %+v", rep)
	}
	if rep.CommittedFeasibleCount.Cmp(big.NewInt(6)) != 0 {
		t.Fatalf("feasible=%s want 6", rep.CommittedFeasibleCount)
	}
	if got := rep.Policies[0].Post; got.Cmp(big.NewInt(6)) != 0 {
		t.Fatalf("joint post projection=%s want 6", got)
	}
	if got := rep.Policies[0].CumulativeShrink; got.Cmp(Shrink(6, 1)) != 0 {
		t.Fatalf("shrink=%s want 6", got)
	}

	// The same release with a cap of 5 must be rejected atomically.
	before := g.State()
	cfg.Policies[0].MaxShrink = Shrink(5, 1)
	g2, err := NewGate(cfg)
	if err != nil {
		t.Fatal(err)
	}
	before2 := g2.State()
	_, err = g2.Admit(&Batch{ID: "b1", Releases: []ReleaseRow{{
		ID: "r1", Modulus: big.NewInt(6), Terms: []Term{term("a", 1), term("b", 1)}, Value: big.NewInt(0),
	}}})
	if !errors.Is(err, ErrCollusionExceeded) {
		t.Fatalf("err=%v want ErrCollusionExceeded", err)
	}
	after2 := g2.State()
	if before2.Digest != after2.Digest || before2.Version != after2.Version || before2.FeasibleCount.Cmp(after2.FeasibleCount) != 0 {
		t.Fatalf("state changed on rejection: before=%+v after=%+v", before2, after2)
	}
	_ = before
}

func TestJointProjectionNotMarginalProduct(t *testing.T) {
	// x1+x2=0 leaves 6 joint pairs out of 36, while each marginal remains
	// full (6/6).  A product of marginal shrink factors would be 1 and would
	// silently miss the combinatorial leak.
	cfg := Config{
		Variables: vars6("a", "b"),
		Policies: []CollusionPolicy{{
			Name:      "both",
			Protected: []ProtectedForm{form("a", 6, term("a", 1)), form("b", 6, term("b", 1))},
			MaxShrink: Shrink(2, 1),
		}},
	}
	g, err := NewGate(cfg)
	if err != nil {
		t.Fatal(err)
	}
	before := g.State()
	rep, err := g.Admit(&Batch{ID: "b1", Releases: []ReleaseRow{{
		ID: "r1", Modulus: big.NewInt(6), Terms: []Term{term("a", 1), term("b", 1)}, Value: big.NewInt(0),
	}}})
	if !errors.Is(err, ErrCollusionExceeded) {
		t.Fatalf("err=%v want collusion rejection", err)
	}
	if rep == nil || rep.Policies[0].Post.Cmp(big.NewInt(6)) != 0 {
		t.Fatalf("joint post should be 6: %+v", rep)
	}
	if rep.Certificate != nil {
		if err := rep.Certificate.Verify(); err != nil {
			t.Fatalf("rejected-batch certificate: %v", err)
		}
	}
	after := g.State()
	if before.Digest != after.Digest || before.FeasibleCount.Cmp(after.FeasibleCount) != 0 {
		t.Fatalf("state changed on rejection")
	}
}

func TestOverlappingGroupsAtomicRollback(t *testing.T) {
	cfg := Config{
		Variables: vars6("a", "b", "c"),
		Policies: []CollusionPolicy{
			{
				Name:      "ab",
				Protected: []ProtectedForm{form("a", 6, term("a", 1)), form("b", 6, term("b", 1))},
				MaxShrink: Shrink(6, 1),
			},
			{
				Name:      "bc",
				Protected: []ProtectedForm{form("b", 6, term("b", 1)), form("c", 6, term("c", 1))},
				MaxShrink: Shrink(2, 1),
			},
		},
	}
	g, err := NewGate(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := g.Admit(&Batch{ID: "one", Releases: []ReleaseRow{{
		ID: "r1", Modulus: big.NewInt(6), Terms: []Term{term("a", 1), term("b", 1)}, Value: big.NewInt(0),
	}}}); err != nil {
		t.Fatal(err)
	}
	state := g.State()
	if state.FeasibleCount.Cmp(big.NewInt(36)) != 0 {
		t.Fatalf("feasible after first batch=%s want 36", state.FeasibleCount)
	}
	hist := len(g.History())

	rep, err := g.Admit(&Batch{ID: "two", Releases: []ReleaseRow{{
		ID: "r2", Modulus: big.NewInt(6), Terms: []Term{term("b", 1), term("c", 1)}, Value: big.NewInt(0),
	}}})
	if !errors.Is(err, ErrCollusionExceeded) {
		t.Fatalf("err=%v want collusion rejection", err)
	}
	if rep == nil || rep.Policies[1].Allowed {
		t.Fatalf("policy bc should be the violating policy: %+v", rep)
	}
	after := g.State()
	if state.Digest != after.Digest || state.Version != after.Version || state.FeasibleCount.Cmp(after.FeasibleCount) != 0 {
		t.Fatalf("state changed on overlapping-group rollback")
	}
	if len(g.History()) != hist {
		t.Fatalf("history changed on rollback")
	}
}

func TestInconsistentBatchRollsBack(t *testing.T) {
	cfg := Config{Variables: vars6("a")}
	g, err := NewGate(cfg)
	if err != nil {
		t.Fatal(err)
	}
	before := g.State()
	_, err = g.Admit(&Batch{ID: "bad", Releases: []ReleaseRow{
		{ID: "r1", Modulus: big.NewInt(6), Terms: []Term{term("a", 1)}, Value: big.NewInt(1)},
		{ID: "r2", Modulus: big.NewInt(6), Terms: []Term{term("a", 1)}, Value: big.NewInt(2)},
	}})
	if !errors.Is(err, ErrInconsistent) {
		t.Fatalf("err=%v want ErrInconsistent", err)
	}
	after := g.State()
	if before.Digest != after.Digest || before.Version != after.Version || before.FeasibleCount.Cmp(after.FeasibleCount) != 0 {
		t.Fatalf("state changed on inconsistent batch")
	}
	if len(g.History()) != 0 {
		t.Fatalf("history changed on inconsistent batch")
	}
}

func TestNonWellDefinedRejected(t *testing.T) {
	cfg := Config{Variables: []Variable{{Name: "a", Modulus: big.NewInt(6)}}}
	g, err := NewGate(cfg)
	if err != nil {
		t.Fatal(err)
	}
	_, err = g.Admit(&Batch{ID: "bad", Releases: []ReleaseRow{{
		ID: "r", Modulus: big.NewInt(4), Terms: []Term{term("a", 1)}, Value: big.NewInt(0),
	}}})
	if !errors.Is(err, ErrNonWellDefined) {
		t.Fatalf("err=%v want ErrNonWellDefined", err)
	}
	// A coefficient divisible by the required factor is well defined.
	if _, err := g.Admit(&Batch{ID: "ok", Releases: []ReleaseRow{{
		ID: "r", Modulus: big.NewInt(4), Terms: []Term{term("a", 2)}, Value: big.NewInt(0),
	}}}); err != nil {
		t.Fatalf("well-defined row rejected: %v", err)
	}
}

func TestIntervalAndBitSliceExactCounts(t *testing.T) {
	cfg := Config{Variables: []Variable{{Name: "x", Modulus: big.NewInt(12)}}}
	g, err := NewGate(cfg)
	if err != nil {
		t.Fatal(err)
	}
	// Wrap-around residues {10,11,0,1,2}: 5 values.
	arcRep, err := g.Admit(&Batch{ID: "arc", Intervals: []ModIntervalConstraint{{
		ID: "i", Terms: []Term{term("x", 1)}, Modulus: big.NewInt(12),
		Low: big.NewInt(10), High: big.NewInt(2), Wrap: true,
	}}})
	if err != nil {
		t.Fatal(err)
	}
	if err := arcRep.Certificate.Verify(); err != nil {
		t.Fatalf("interval certificate: %v", err)
	}
	if got := g.State().FeasibleCount; got.Cmp(big.NewInt(5)) != 0 {
		t.Fatalf("wrap interval count=%s want 5", got)
	}
	g2, _ := NewGate(Config{Variables: []Variable{{Name: "x", Modulus: big.NewInt(12)}}})
	if _, err := g2.Admit(&Batch{ID: "bits", BitSlices: []BitSliceConstraint{{
		ID: "b", Terms: []Term{term("x", 1)}, Modulus: big.NewInt(12),
		Shift: 1, Width: 2, Value: big.NewInt(3),
	}}}); err != nil {
		t.Fatal(err)
	}
	// x in 0..11 with bits 1..2 == 3: x in {6,7}; 7 has bits 11 -> 3.
	if got := g2.State().FeasibleCount; got.Cmp(big.NewInt(2)) != 0 {
		t.Fatalf("bit slice count=%s want 2", got)
	}
}

func TestCrossGenerationMapping(t *testing.T) {
	cfg := Config{Variables: []Variable{
		{Name: "parent", Modulus: big.NewInt(6)},
		{Name: "child", Modulus: big.NewInt(12)},
	}}
	g, err := NewGate(cfg)
	if err != nil {
		t.Fatal(err)
	}
	rep, err := g.Admit(&Batch{ID: "map", Mappings: []MappingConstraint{{
		ID: "m", Source: "parent", Target: "child", Multiplier: big.NewInt(2), Offset: big.NewInt(1),
	}}})
	if err != nil {
		t.Fatal(err)
	}
	if rep.CommittedFeasibleCount.Cmp(big.NewInt(6)) != 0 {
		t.Fatalf("mapping feasible=%s want 6", rep.CommittedFeasibleCount)
	}
	// 3*parent is not a well-defined map into Z/4Z.
	g2, _ := NewGate(cfg)
	_, err = g2.Admit(&Batch{ID: "bad", Mappings: []MappingConstraint{{
		ID: "m", Source: "parent", Target: "child", Multiplier: big.NewInt(3), Offset: big.NewInt(0),
		Modulus: big.NewInt(4),
	}}})
	if !errors.Is(err, ErrNonWellDefined) {
		t.Fatalf("err=%v want ErrNonWellDefined", err)
	}
}

func TestCertificateOrderIndependence(t *testing.T) {
	baseCfg := Config{
		Variables: vars6("a", "b"),
		Policies: []CollusionPolicy{{
			Name:      "ab",
			Protected: []ProtectedForm{form("a", 6, term("a", 1)), form("b", 6, term("b", 1))},
			MaxShrink: Shrink(6, 1),
		}},
	}
	batch := &Batch{ID: "b1", Releases: []ReleaseRow{{
		ID: "r1", Modulus: big.NewInt(6),
		Terms: []Term{term("a", 1), term("b", 1)}, Value: big.NewInt(0),
	}}}
	g1, err := NewGate(baseCfg)
	if err != nil {
		t.Fatal(err)
	}
	r1, err := g1.Admit(batch)
	if err != nil {
		t.Fatal(err)
	}

	perm := baseCfg
	perm.Variables = []Variable{baseCfg.Variables[1], baseCfg.Variables[0]}
	perm.Policies = []CollusionPolicy{{
		Name:      "ab",
		Protected: []ProtectedForm{form("b", 6, term("b", 1)), form("a", 6, term("a", 1))},
		MaxShrink: Shrink(6, 1),
	}}
	batch2 := &Batch{ID: "b1", Releases: []ReleaseRow{{
		ID: "r1", Modulus: big.NewInt(6),
		Terms: []Term{term("b", 1), term("a", 1)}, Value: big.NewInt(0),
	}}}
	g2, err := NewGate(perm)
	if err != nil {
		t.Fatal(err)
	}
	r2, err := g2.Admit(batch2)
	if err != nil {
		t.Fatal(err)
	}
	if r1.Certificate.CanonicalSHA256 != r2.Certificate.CanonicalSHA256 {
		t.Fatalf("certificate digests differ:\n%s\n%s", r1.Certificate.CanonicalSHA256, r2.Certificate.CanonicalSHA256)
	}
}

func TestMixedModuliGateAgainstBruteForce(t *testing.T) {
	mods := []int64{6, 12, 15}
	vars, byName := mustVars(t, mods...)
	cfg := Config{Variables: []Variable{
		{Name: "a", Modulus: big.NewInt(6)},
		{Name: "b", Modulus: big.NewInt(12)},
		{Name: "c", Modulus: big.NewInt(15)},
	}}
	g, err := NewGate(cfg)
	if err != nil {
		t.Fatal(err)
	}
	batch := &Batch{ID: "mix", Releases: []ReleaseRow{
		{ID: "r1", Modulus: big.NewInt(6), Terms: []Term{term("a", 2), term("b", 3), term("c", 0)}, Value: big.NewInt(5)},
		{ID: "r2", Modulus: big.NewInt(15), Terms: []Term{term("a", 5), term("b", 0), term("c", 3)}, Value: big.NewInt(8)},
	}}
	rep, err := g.Admit(batch)
	if err != nil {
		t.Fatal(err)
	}
	pb, err := buildBatch(batch, vars, byName)
	if err != nil {
		t.Fatal(err)
	}
	want := bruteFeasible(vars, pb.rows, pb.aux, nil)
	if rep.CommittedFeasibleCount.Cmp(want) != 0 {
		t.Fatalf("gate feasible=%s brute=%s", rep.CommittedFeasibleCount, want)
	}
}
