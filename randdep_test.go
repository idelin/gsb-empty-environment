package netsec

import (
	"fmt"
	"math/big"
	"strings"
	"testing"
)

func rat(v int64) *big.Rat { return big.NewRat(v, 1) }

// scaled builds id*c as an expression.
func scaled(id string, c int64) Expr { return Term(id).Scale(rat(c)) }

func TestBasicNoiseCancellation(t *testing.T) {
	// o1 = s + r, o2 = r  =>  o1 - o2 = s.
	p := &Program{
		Secrets: []string{"s"},
		Randoms: []string{"r"},
		Outputs: []Output{
			{Name: "o1", Expr: Term("s").Add(Term("r"))},
			{Name: "o2", Expr: Term("r")},
		},
	}
	res, err := Solve(p, nil)
	if err != nil {
		t.Fatalf("Solve: %v", err)
	}
	if !res.MinimalProven || res.BudgetExhausted {
		t.Fatalf("expected proven minimal result, got %+v", res)
	}
	if len(res.Combinations) != 1 {
		t.Fatalf("expected 1 combination, got %d", len(res.Combinations))
	}
	c := res.Combinations[0]
	if len(c.Outputs) != 2 {
		t.Fatalf("expected size-2 combination, got %v", c.Outputs)
	}
	if c.Coeffs["o1"].Cmp(rat(1)) != 0 || c.Coeffs["o2"].Cmp(rat(-1)) != 0 {
		t.Fatalf("unexpected coefficients: %v", c.Coeffs)
	}
	if c.Secret.coeff("s").Cmp(rat(1)) != 0 || len(c.Secret.Terms) != 1 {
		t.Fatalf("unexpected secret part: %s", c.Secret)
	}
}

func TestRationalCoefficients(t *testing.T) {
	// o1 = s + r, o2 = 2r  =>  o1 - (1/2)o2 = s, normalized to (2, -1).
	p := &Program{
		Secrets: []string{"s"},
		Randoms: []string{"r"},
		Outputs: []Output{
			{Name: "o1", Expr: Term("s").Add(Term("r"))},
			{Name: "o2", Expr: scaled("r", 2)},
		},
	}
	res, err := Solve(p, nil)
	if err != nil {
		t.Fatalf("Solve: %v", err)
	}
	if len(res.Combinations) != 1 {
		t.Fatalf("expected 1 combination, got %d", len(res.Combinations))
	}
	c := res.Combinations[0]
	if c.Coeffs["o1"].Cmp(rat(2)) != 0 || c.Coeffs["o2"].Cmp(rat(-1)) != 0 {
		t.Fatalf("expected normalized integer coefficients (2,-1), got %v", c.Coeffs)
	}
	if err := SampleVerify(p, &c, 16, nil); err != nil {
		t.Fatalf("SampleVerify: %v", err)
	}
}

func TestDistinctIdentitiesNeverMerged(t *testing.T) {
	// o1 = s + r1, o2 = r2 with r1, r2 independent identities: no
	// combination can cancel the random part. A value-keyed analyzer
	// would wrongly report o1 - o2 whenever the two sources collide.
	p := &Program{
		Secrets: []string{"s"},
		Randoms: []string{"r1", "r2"},
		Outputs: []Output{
			{Name: "o1", Expr: Term("s").Add(Term("r1"))},
			{Name: "o2", Expr: Term("r2")},
		},
	}
	res, err := Solve(p, nil)
	if err != nil {
		t.Fatalf("Solve: %v", err)
	}
	if len(res.Combinations) != 0 {
		t.Fatalf("expected no combination, got %+v", res.Combinations)
	}

	bogus := Combination{
		Outputs: []string{"o1", "o2"},
		Coeffs:  map[string]*big.Rat{"o1": rat(1), "o2": rat(-1)},
	}
	if _, err := VerifyCertificate(p, &bogus); err == nil {
		t.Fatal("certificate accepted a combination that merges distinct random identities")
	}

	// The trap this guards against: under a colliding assignment the bogus
	// combination numerically matches the secret, yet it must still be
	// rejected because identities — not values — define random sources.
	colliding := map[string]*big.Rat{"s": rat(5), "r1": rat(3), "r2": rat(3)}
	combined, err := combine(p, &bogus)
	if err != nil {
		t.Fatalf("combine: %v", err)
	}
	if eval(combined, colliding).Cmp(rat(5)) != 0 {
		t.Fatal("test setup broken: colliding assignment should mask the leak")
	}
	if err := SampleVerify(p, &bogus, 4, nil); err == nil {
		t.Fatal("SampleVerify accepted a combination that only works under value collision")
	}
}

func TestThreeOutputCombination(t *testing.T) {
	// o1 = s + r1, o2 = s + r2, o3 = r1 + r2  =>  o1 + o2 - o3 = 2s.
	p := &Program{
		Secrets: []string{"s"},
		Randoms: []string{"r1", "r2"},
		Outputs: []Output{
			{Name: "o1", Expr: Term("s").Add(Term("r1"))},
			{Name: "o2", Expr: Term("s").Add(Term("r2"))},
			{Name: "o3", Expr: Term("r1").Add(Term("r2"))},
		},
	}
	res, err := Solve(p, nil)
	if err != nil {
		t.Fatalf("Solve: %v", err)
	}
	if !res.MinimalProven {
		t.Fatalf("expected proven minimal result, got %+v", res)
	}
	if len(res.Combinations) != 1 {
		t.Fatalf("expected 1 combination, got %d", len(res.Combinations))
	}
	c := res.Combinations[0]
	if len(c.Outputs) != 3 {
		t.Fatalf("expected size-3 combination, got %v", c.Outputs)
	}
	if c.Secret.coeff("s").Cmp(rat(2)) != 0 {
		t.Fatalf("expected secret part 2s, got %s", c.Secret)
	}
}

func TestBudgetExhaustion(t *testing.T) {
	// o_i = s + r_i (i=1..8), o9 = r1+...+r8. The only feasible
	// combination is all nine outputs: sum(o_i) - o9 = 8s.
	p := &Program{Secrets: []string{"s"}}
	all := Expr{Terms: map[string]*big.Rat{}}
	for i := 1; i <= 8; i++ {
		id := fmt.Sprintf("r%d", i)
		p.Randoms = append(p.Randoms, id)
		p.Outputs = append(p.Outputs, Output{Name: fmt.Sprintf("o%d", i), Expr: Term("s").Add(Term(id))})
		all = all.Add(Term(id))
	}
	p.Outputs = append(p.Outputs, Output{Name: "o9", Expr: all})

	res, err := Solve(p, &Options{Budget: 100})
	if err != nil {
		t.Fatalf("Solve: %v", err)
	}
	if !res.BudgetExhausted {
		t.Fatal("expected budget exhaustion")
	}
	if res.MinimalProven {
		t.Fatal("minimality must be unproven when the budget is exhausted")
	}
	if len(res.Combinations) != 0 {
		t.Fatalf("expected no verified combinations within budget, got %d", len(res.Combinations))
	}

	full, err := Solve(p, nil)
	if err != nil {
		t.Fatalf("Solve: %v", err)
	}
	if !full.MinimalProven || len(full.Combinations) != 1 {
		t.Fatalf("expected one proven-minimal combination, got %+v", full)
	}
	if got := len(full.Combinations[0].Outputs); got != 9 {
		t.Fatalf("expected size-9 combination, got %d", got)
	}
	if full.Combinations[0].Secret.coeff("s").Cmp(rat(8)) != 0 {
		t.Fatalf("expected secret part 8s, got %s", full.Combinations[0].Secret)
	}
}

func TestOutputLimit(t *testing.T) {
	p := &Program{Secrets: []string{"s"}, Randoms: []string{"r"}}
	for i := 0; i < DefaultMaxOutputs+1; i++ {
		p.Outputs = append(p.Outputs, Output{Name: strings.Repeat("o", i+1), Expr: Term("r")})
	}
	if _, err := Solve(p, nil); err == nil {
		t.Fatal("expected error for more than twenty outputs")
	}
}

func TestTamperedCertificateRejected(t *testing.T) {
	p := &Program{
		Secrets: []string{"s"},
		Randoms: []string{"r"},
		Outputs: []Output{
			{Name: "o1", Expr: Term("s").Add(Term("r"))},
			{Name: "o2", Expr: Term("r")},
		},
	}
	res, err := Solve(p, nil)
	if err != nil {
		t.Fatalf("Solve: %v", err)
	}
	good := res.Combinations[0]
	if _, err := VerifyCertificate(p, &good); err != nil {
		t.Fatalf("valid certificate rejected: %v", err)
	}

	tampered := Combination{
		Outputs: good.Outputs,
		Coeffs:  map[string]*big.Rat{"o1": rat(1), "o2": rat(1)}, // r survives
	}
	if _, err := VerifyCertificate(p, &tampered); err == nil {
		t.Fatal("tampered certificate accepted: random term survives")
	}

	secretless := Combination{
		Outputs: []string{"o2"},
		Coeffs:  map[string]*big.Rat{"o2": rat(1)},
	}
	if _, err := VerifyCertificate(p, &secretless); err == nil {
		t.Fatal("certificate without secret terms accepted")
	}
}

func TestValidationErrors(t *testing.T) {
	dup := &Program{
		Secrets: []string{"s"},
		Randoms: []string{"s"},
		Outputs: []Output{{Name: "o1", Expr: Term("s")}},
	}
	if _, err := Solve(dup, nil); err == nil {
		t.Fatal("expected error for identity declared as both secret and random")
	}
	undeclared := &Program{
		Secrets: []string{"s"},
		Outputs: []Output{{Name: "o1", Expr: Term("ghost")}},
	}
	if _, err := Solve(undeclared, nil); err == nil {
		t.Fatal("expected error for undeclared variable")
	}
}

func TestSolveDoesNotMutateProgram(t *testing.T) {
	// Regression: elimination works on matrices built from expression
	// coefficients; it must never corrupt the program under analysis.
	p := &Program{
		Secrets: []string{"s"},
		Randoms: []string{"r"},
		Outputs: []Output{
			{Name: "o1", Expr: Term("s").Add(Term("r"))},
			{Name: "o2", Expr: scaled("r", 2)},
		},
	}
	if _, err := Solve(p, nil); err != nil {
		t.Fatalf("Solve: %v", err)
	}
	if got := p.Outputs[0].Expr.coeff("r"); got.Cmp(rat(1)) != 0 {
		t.Fatalf("program mutated: o1 r-coefficient is now %s", got.RatString())
	}
	if got := p.Outputs[1].Expr.coeff("r"); got.Cmp(rat(2)) != 0 {
		t.Fatalf("program mutated: o2 r-coefficient is now %s", got.RatString())
	}
}
