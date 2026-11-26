package admission

import (
	"encoding/json"
	"errors"
	"math/big"
	"math/rand"
	"testing"
)

func TestAuxiliaryConstraintJointPolicy(t *testing.T) {
	cfg := Config{
		Variables: vars6("a", "b"),
		Policies: []CollusionPolicy{{
			Name:      "both",
			Protected: []ProtectedForm{form("a", 6, term("a", 1)), form("b", 6, term("b", 1))},
			MaxShrink: Shrink(6, 1),
		}},
	}
	g, err := NewGate(cfg)
	if err != nil {
		t.Fatal(err)
	}
	rep, err := g.Admit(&Batch{ID: "arc", Intervals: []ModIntervalConstraint{{
		ID: "i", Terms: []Term{term("a", 1), term("b", 1)}, Modulus: big.NewInt(6),
		Low: big.NewInt(0), High: big.NewInt(0),
	}}})
	if err != nil {
		t.Fatal(err)
	}
	if rep.CommittedFeasibleCount.Cmp(big.NewInt(6)) != 0 {
		t.Fatalf("feasible=%s want 6", rep.CommittedFeasibleCount)
	}
	if rep.Policies[0].Post.Cmp(big.NewInt(6)) != 0 {
		t.Fatalf("joint post=%s want 6", rep.Policies[0].Post)
	}
	if rep.Policies[0].Method != "exact-enumeration" {
		t.Fatalf("method=%s", rep.Policies[0].Method)
	}
	if err := rep.Certificate.Verify(); err != nil {
		t.Fatalf("auxiliary certificate: %v", err)
	}
}

func TestBaselinePriorAndCumulativeShrink(t *testing.T) {
	cfg := Config{
		Variables: []Variable{{Name: "x", Modulus: big.NewInt(12)}},
		Baseline: &Batch{ID: "prior", Intervals: []ModIntervalConstraint{{
			ID: "i", Terms: []Term{term("x", 1)}, Modulus: big.NewInt(12),
			Low: big.NewInt(0), High: big.NewInt(5),
		}}},
		Policies: []CollusionPolicy{{
			Name:      "x",
			Protected: []ProtectedForm{form("x", 12, term("x", 1))},
			MaxShrink: Shrink(2, 1),
		}},
	}
	g, err := NewGate(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if got := g.State().PriorFeasibleCount; got.Cmp(big.NewInt(6)) != 0 {
		t.Fatalf("prior feasible=%s want 6", got)
	}
	rep, err := g.Admit(&Batch{ID: "release", Releases: []ReleaseRow{{
		ID: "r", Modulus: big.NewInt(2), Terms: []Term{term("x", 1)}, Value: big.NewInt(0),
	}}})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Policies[0].Prior.Cmp(big.NewInt(6)) != 0 {
		t.Fatalf("policy prior=%s want 6", rep.Policies[0].Prior)
	}
	if rep.Policies[0].Post.Cmp(big.NewInt(3)) != 0 {
		t.Fatalf("policy post=%s want 3", rep.Policies[0].Post)
	}
}

func TestCumulativeShrinkAcrossBatches(t *testing.T) {
	cfg := Config{
		Variables: vars6("a"),
		Policies: []CollusionPolicy{{
			Name:      "a",
			Protected: []ProtectedForm{form("a", 6, term("a", 1))},
			MaxShrink: Shrink(3, 1),
		}},
	}
	g, err := NewGate(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := g.Admit(&Batch{ID: "one", Releases: []ReleaseRow{{
		ID: "r1", Modulus: big.NewInt(2), Terms: []Term{term("a", 1)}, Value: big.NewInt(0),
	}}}); err != nil {
		t.Fatal(err)
	}
	if got := g.State().FeasibleCount; got.Cmp(big.NewInt(3)) != 0 {
		t.Fatalf("feasible=%s want 3", got)
	}
	state := g.State()
	// Per-batch shrink would be 3/1 = 3, exactly at the cap.  The cumulative
	// shrink from prior is 6/1 = 6, so the second batch must be rejected.
	_, err = g.Admit(&Batch{ID: "two", Releases: []ReleaseRow{{
		ID: "r2", Modulus: big.NewInt(3), Terms: []Term{term("a", 1)}, Value: big.NewInt(0),
	}}})
	if !errors.Is(err, ErrCollusionExceeded) {
		t.Fatalf("err=%v want cumulative collusion rejection", err)
	}
	after := g.State()
	if state.Digest != after.Digest || state.FeasibleCount.Cmp(after.FeasibleCount) != 0 {
		t.Fatalf("state changed on cumulative rejection")
	}
}

func TestLargePrimePowerModulus(t *testing.T) {
	m := new(big.Int).Lsh(big.NewInt(1), 48)
	n := new(big.Int).Lsh(big.NewInt(1), 47)
	cfg := Config{Variables: []Variable{{Name: "x", Modulus: m}}}
	g, err := NewGate(cfg)
	if err != nil {
		t.Fatal(err)
	}
	rep, err := g.Admit(&Batch{ID: "one", Releases: []ReleaseRow{{
		ID: "r", Modulus: n, Terms: []Term{term("x", 1)}, Value: big.NewInt(0),
	}}})
	if err != nil {
		t.Fatal(err)
	}
	if rep.CommittedFeasibleCount.Cmp(big.NewInt(2)) != 0 {
		t.Fatalf("feasible=%s want 2", rep.CommittedFeasibleCount)
	}
	g2, _ := NewGate(cfg)
	rep, err = g2.Admit(&Batch{ID: "two", Releases: []ReleaseRow{{
		ID: "r", Modulus: n, Terms: []Term{term("x", 2)}, Value: big.NewInt(0),
	}}})
	if err != nil {
		t.Fatal(err)
	}
	if rep.CommittedFeasibleCount.Cmp(big.NewInt(4)) != 0 {
		t.Fatalf("feasible=%s want 4", rep.CommittedFeasibleCount)
	}
}

func TestHigherPrimePowerInReleaseModulus(t *testing.T) {
	cases := []struct {
		m, n, a, b int64
		want       int64
	}{
		{8, 16, 2, 0, 1},
		{8, 16, 4, 0, 2},
		{8, 16, 2, 4, 1},
		{6, 9, 3, 0, 2},
		{6, 9, 3, 3, 2},
		{12, 18, 3, 0, 2},
		{6, 10, 5, 0, 3},
		{6, 10, 5, 5, 3},
	}
	for _, tc := range cases {
		cfg := Config{Variables: []Variable{{Name: "x", Modulus: big.NewInt(tc.m)}}}
		g, err := NewGate(cfg)
		if err != nil {
			t.Fatal(err)
		}
		rep, err := g.Admit(&Batch{ID: "b", Releases: []ReleaseRow{{
			ID: "r", Modulus: big.NewInt(tc.n), Terms: []Term{term("x", tc.a)}, Value: big.NewInt(tc.b),
		}}})
		if err != nil {
			t.Fatalf("M=%d N=%d a=%d b=%d: %v", tc.m, tc.n, tc.a, tc.b, err)
		}
		if rep.CommittedFeasibleCount.Cmp(big.NewInt(tc.want)) != 0 {
			t.Fatalf("M=%d N=%d a=%d b=%d: got %s want %d", tc.m, tc.n, tc.a, tc.b, rep.CommittedFeasibleCount, tc.want)
		}
	}
}

func TestEnumerationLimitRollsBack(t *testing.T) {
	m := new(big.Int).Lsh(big.NewInt(1), 48)
	cfg := Config{
		Variables:      []Variable{{Name: "x", Modulus: m}},
		MaxEnumeration: 100,
	}
	g, err := NewGate(cfg)
	if err != nil {
		t.Fatal(err)
	}
	before := g.State()
	_, err = g.Admit(&Batch{ID: "aux", Intervals: []ModIntervalConstraint{{
		ID: "i", Terms: []Term{term("x", 1)}, Modulus: m,
		Low: big.NewInt(0), High: big.NewInt(10),
	}}})
	if !errors.Is(err, ErrEnumerationLimit) {
		t.Fatalf("err=%v want ErrEnumerationLimit", err)
	}
	after := g.State()
	if before.Digest != after.Digest || before.Version != after.Version {
		t.Fatalf("state changed on enumeration-limit rejection")
	}
}

func TestPreviewDoesNotCommit(t *testing.T) {
	cfg := Config{Variables: vars6("a")}
	g, _ := NewGate(cfg)
	before := g.State()
	rep, err := g.Preview(&Batch{ID: "p", Releases: []ReleaseRow{{
		ID: "r", Modulus: big.NewInt(6), Terms: []Term{term("a", 1)}, Value: big.NewInt(0),
	}}})
	if err != nil {
		t.Fatal(err)
	}
	if !rep.Admitted || rep.Committed {
		t.Fatalf("preview flags: %+v", rep)
	}
	if g.State().Digest != before.Digest || len(g.History()) != 0 {
		t.Fatalf("preview committed state")
	}
}

func TestCertificatePrimePowersReconstructCounts(t *testing.T) {
	cfg := Config{Variables: []Variable{
		{Name: "a", Modulus: big.NewInt(6)},
		{Name: "b", Modulus: big.NewInt(12)},
	}}
	g, err := NewGate(cfg)
	if err != nil {
		t.Fatal(err)
	}
	rep, err := g.Admit(&Batch{ID: "b", Releases: []ReleaseRow{{
		ID: "r", Modulus: big.NewInt(6), Terms: []Term{term("a", 2), term("b", 3)}, Value: big.NewInt(5),
	}}})
	if err != nil {
		t.Fatal(err)
	}
	prod := big.NewInt(1)
	for _, pc := range rep.Certificate.Linear.Primes {
		p, _ := new(big.Int).SetString(pc.Prime, 10)
		domain := big.NewInt(1)
		for _, e := range pc.DomainExponents {
			domain.Mul(domain, new(big.Int).Exp(p, big.NewInt(int64(e.Exponent)), nil))
		}
		codomain := big.NewInt(1)
		for _, r := range pc.RowExponents {
			codomain.Mul(codomain, new(big.Int).Exp(p, big.NewInt(int64(r.Exponent)), nil))
		}
		index := big.NewInt(1)
		for _, d := range pc.InvariantFactors {
			z, _ := new(big.Int).SetString(d, 10)
			index.Mul(index, z)
		}
		if pc.DomainSize != domain.String() || pc.CodomainSize != codomain.String() || pc.DeterminantIndex != index.String() {
			t.Fatalf("certificate prime %s inconsistent: %+v", pc.Prime, pc)
		}
		num := new(big.Int).Mul(domain, index)
		q, rem := new(big.Int).QuoRem(num, codomain, new(big.Int))
		if rem.Sign() != 0 || pc.KernelSize != q.String() {
			t.Fatalf("certificate kernel mismatch for %s: %+v", pc.Prime, pc)
		}
		prod.Mul(prod, q)
	}
	if prod.Cmp(rep.CommittedFeasibleCount) != 0 {
		t.Fatalf("product of prime-power kernels %s != feasible %s", prod, rep.CommittedFeasibleCount)
	}
}

func TestNonWellDefinedAuxiliaryAndProtectedForm(t *testing.T) {
	cfg := Config{Variables: []Variable{{Name: "x", Modulus: big.NewInt(6)}}}
	g, err := NewGate(cfg)
	if err != nil {
		t.Fatal(err)
	}
	_, err = g.Admit(&Batch{ID: "bad", Intervals: []ModIntervalConstraint{{
		ID: "i", Terms: []Term{term("x", 1)}, Modulus: big.NewInt(4),
		Low: big.NewInt(0), High: big.NewInt(1),
	}}})
	if !errors.Is(err, ErrNonWellDefined) {
		t.Fatalf("interval err=%v", err)
	}
	cfg.Policies = []CollusionPolicy{{
		Name:      "bad",
		Protected: []ProtectedForm{{Name: "x", Modulus: big.NewInt(4), Terms: []Term{term("x", 1)}}},
		MaxShrink: Shrink(2, 1),
	}}
	if _, err := NewGate(cfg); !errors.Is(err, ErrNonWellDefined) {
		t.Fatalf("protected form err=%v", err)
	}
}

func TestSNFLargerRandomMatrices(t *testing.T) {
	rng := rand.New(rand.NewSource(31))
	for iter := 0; iter < 200; iter++ {
		r := 2 + rng.Intn(4)
		c := 2 + rng.Intn(4)
		m := newIntMatrix(r, c)
		for i := 0; i < r; i++ {
			for j := 0; j < c; j++ {
				m.A[i][j].SetInt64(int64(rng.Intn(201) - 100))
			}
		}
		d, u, v, err := smithNormalForm(m)
		if err != nil {
			t.Fatalf("iter %d: %v", iter, err)
		}
		got := matMul(matMul(u, m), v)
		if !matEqual(d, got) {
			t.Fatalf("iter %d UAV mismatch\nA=\n%sD=\n%sUAV=\n%s", iter, matString(m), matString(d), matString(got))
		}
		for i := 1; i < minInt(d.Rows, d.Cols); i++ {
			if d.A[i-1][i-1].Sign() != 0 && new(big.Int).Mod(d.A[i][i], d.A[i-1][i-1]).Sign() != 0 {
				t.Fatalf("iter %d divisibility failure: %s", iter, matString(d))
			}
		}
		if detSmall(u) != 1 && detSmall(u) != -1 {
			t.Fatalf("iter %d U not unimodular: det=%d", iter, detSmall(u))
		}
		if detSmall(v) != 1 && detSmall(v) != -1 {
			t.Fatalf("iter %d V not unimodular: det=%d", iter, detSmall(v))
		}
	}
}

func detSmall(m *intMatrix) int64 {
	if m.Rows != m.Cols {
		return 0
	}
	rows := make([]int, m.Rows)
	cols := make([]int, m.Cols)
	for i := range rows {
		rows[i] = i
		cols[i] = i
	}
	return determinantSmall(m, rows, cols, m.Rows).Int64()
}

func TestMixedModuliCertificateOrderIndependence(t *testing.T) {
	cfg := Config{
		Variables: []Variable{
			{Name: "a", Modulus: big.NewInt(6)},
			{Name: "b", Modulus: big.NewInt(12)},
			{Name: "c", Modulus: big.NewInt(15)},
		},
		Policies: []CollusionPolicy{
			{Name: "ab", Protected: []ProtectedForm{
				form("a", 6, term("a", 1)), form("b", 12, term("b", 1)),
			}, MaxShrink: Shrink(6, 1)},
			{Name: "c", Protected: []ProtectedForm{form("c", 15, term("c", 1))}, MaxShrink: Shrink(5, 1)},
		},
	}
	batch := &Batch{ID: "mix", Releases: []ReleaseRow{
		{ID: "r1", Modulus: big.NewInt(6), Terms: []Term{term("a", 2), term("b", 3)}, Value: big.NewInt(5)},
		{ID: "r2", Modulus: big.NewInt(15), Terms: []Term{term("a", 5), term("c", 3)}, Value: big.NewInt(8)},
	}}
	g1, err := NewGate(cfg)
	if err != nil {
		t.Fatal(err)
	}
	r1, err := g1.Admit(batch)
	if err != nil {
		t.Fatal(err)
	}
	perm := Config{
		Variables: []Variable{cfg.Variables[2], cfg.Variables[0], cfg.Variables[1]},
		Policies: []CollusionPolicy{
			{Name: "c", Protected: []ProtectedForm{form("c", 15, term("c", 1))}, MaxShrink: Shrink(5, 1)},
			{Name: "ab", Protected: []ProtectedForm{
				form("b", 12, term("b", 1)), form("a", 6, term("a", 1)),
			}, MaxShrink: Shrink(6, 1)},
		},
	}
	batch2 := &Batch{ID: "mix", Releases: []ReleaseRow{
		{ID: "r2", Modulus: big.NewInt(15), Terms: []Term{term("c", 3), term("a", 5)}, Value: big.NewInt(8)},
		{ID: "r1", Modulus: big.NewInt(6), Terms: []Term{term("b", 3), term("a", 2)}, Value: big.NewInt(5)},
	}}
	g2, err := NewGate(perm)
	if err != nil {
		t.Fatal(err)
	}
	r2, err := g2.Admit(batch2)
	if err != nil {
		t.Fatal(err)
	}
	if r1.Certificate.CanonicalSHA256 != r2.Certificate.CanonicalSHA256 {
		t.Fatalf("certificate digest differs for permuted mixed system")
	}
}

func TestReportJSONMarshal(t *testing.T) {
	cfg := Config{
		Variables: vars6("a"),
		Policies: []CollusionPolicy{{
			Name: "a", Protected: []ProtectedForm{form("a", 6, term("a", 1))}, MaxShrink: Shrink(6, 1),
		}},
	}
	g, err := NewGate(cfg)
	if err != nil {
		t.Fatal(err)
	}
	rep, err := g.Admit(&Batch{ID: "b", Releases: []ReleaseRow{{
		ID: "r", Modulus: big.NewInt(6), Terms: []Term{term("a", 1)}, Value: big.NewInt(0),
	}}})
	if err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(rep)
	if err != nil {
		t.Fatalf("report JSON: %v", err)
	}
	if len(b) == 0 {
		t.Fatalf("empty report JSON")
	}
}

func TestCertificateVerifyAndTamper(t *testing.T) {
	cfg := Config{
		Variables: []Variable{
			{Name: "a", Modulus: big.NewInt(6)},
			{Name: "b", Modulus: big.NewInt(12)},
		},
		Policies: []CollusionPolicy{{
			Name: "ab", Protected: []ProtectedForm{
				form("a", 6, term("a", 1)), form("b", 12, term("b", 1)),
			},
			MaxShrink: Shrink(6, 1),
		}},
	}
	g, _ := NewGate(cfg)
	rep, err := g.Admit(&Batch{ID: "b", Releases: []ReleaseRow{{
		ID: "r", Modulus: big.NewInt(6), Terms: []Term{term("a", 2), term("b", 3)}, Value: big.NewInt(5),
	}}})
	if err != nil {
		t.Fatal(err)
	}
	if err := rep.Certificate.Verify(); err != nil {
		t.Fatalf("certificate verification: %v", err)
	}
	tampered := *rep.Certificate
	tampered.Linear = rep.Certificate.Linear
	if len(tampered.Linear.Primes) > 0 && len(tampered.Linear.Primes[0].InvariantFactors) > 0 {
		tampered.Linear.Primes = append([]PrimePowerCertificate(nil), rep.Certificate.Linear.Primes...)
		pc := tampered.Linear.Primes[0]
		pc.InvariantFactors = append([]string(nil), pc.InvariantFactors...)
		pc.InvariantFactors[0] = "999999"
		tampered.Linear.Primes[0] = pc
		if err := tampered.Verify(); err == nil {
			t.Fatalf("tampered certificate verified")
		}
	}
}

func TestExactIntegerProtectedFormUsesEnumeration(t *testing.T) {
	cfg := Config{
		Variables: vars6("a", "b"),
		Policies: []CollusionPolicy{{
			Name: "exact-sum",
			Protected: []ProtectedForm{{
				Name: "a+b", Modulus: nil,
				Terms: []Term{term("a", 1), term("b", 1)},
			}},
			MaxShrink: Shrink(6, 1),
		}},
	}
	g, err := NewGate(cfg)
	if err != nil {
		t.Fatal(err)
	}
	// Exact sums over 0..5 x 0..5 range over 0..10: 11 prior values.
	prior := g.prior.projections[0]
	if prior.Cmp(big.NewInt(11)) != 0 {
		t.Fatalf("prior exact projection=%s want 11", prior)
	}
	rep, err := g.Admit(&Batch{ID: "b", Releases: []ReleaseRow{{
		ID: "r", Modulus: big.NewInt(6), Terms: []Term{term("a", 1), term("b", 1)}, Value: big.NewInt(0),
	}}})
	if err != nil {
		t.Fatal(err)
	}
	// a+b == 0 (mod 6) and 0 <= a+b <= 10 gives exact sums {0,6}.
	if rep.Policies[0].Post.Cmp(big.NewInt(2)) != 0 {
		t.Fatalf("post exact projection=%s want 2", rep.Policies[0].Post)
	}
	if rep.Policies[0].Method != "exact-enumeration" {
		t.Fatalf("method=%s", rep.Policies[0].Method)
	}
	if err := rep.Certificate.Verify(); err != nil {
		t.Fatalf("exact-form certificate: %v", err)
	}
}

func TestSignedCoefficientsAndMappingDownModulus(t *testing.T) {
	g, err := NewGate(Config{Variables: []Variable{{Name: "x", Modulus: big.NewInt(6)}}})
	if err != nil {
		t.Fatal(err)
	}
	rep, err := g.Admit(&Batch{ID: "neg", Releases: []ReleaseRow{{
		ID: "r", Modulus: big.NewInt(6), Terms: []Term{term("x", -1)}, Value: big.NewInt(5),
	}}})
	if err != nil {
		t.Fatal(err)
	}
	if rep.CommittedFeasibleCount.Cmp(big.NewInt(1)) != 0 {
		t.Fatalf("signed coefficient feasible=%s want 1", rep.CommittedFeasibleCount)
	}

	g2, err := NewGate(Config{Variables: []Variable{
		{Name: "parent", Modulus: big.NewInt(12)},
		{Name: "child", Modulus: big.NewInt(6)},
	}})
	if err != nil {
		t.Fatal(err)
	}
	rep, err = g2.Admit(&Batch{ID: "down", Mappings: []MappingConstraint{{
		ID: "m", Source: "parent", Target: "child", Multiplier: big.NewInt(1), Offset: big.NewInt(0),
	}}})
	if err != nil {
		t.Fatal(err)
	}
	if rep.CommittedFeasibleCount.Cmp(big.NewInt(12)) != 0 {
		t.Fatalf("downward mapping feasible=%s want 12", rep.CommittedFeasibleCount)
	}
}
