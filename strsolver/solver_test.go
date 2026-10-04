package strsolver

import (
	"math/rand"
	"reflect"
	"testing"
)

func mustSolve(t *testing.T, p *Problem) Result {
	t.Helper()
	if err := p.Validate(); err != nil {
		t.Fatalf("invalid problem: %v", err)
	}
	return Solve(p, 0)
}

func expectSat(t *testing.T, p *Problem, want []string) {
	t.Helper()
	res := mustSolve(t, p)
	if res.Status != Sat {
		t.Fatalf("status = %v, want sat", res.Status)
	}
	if !reflect.DeepEqual(res.Assignment, want) {
		t.Fatalf("assignment = %q, want %q", res.Assignment, want)
	}
	if err := Verify(p, res.Assignment); err != nil {
		t.Fatalf("solver assignment failed independent check: %v", err)
	}
}

// A forbidden substring formed across the boundary of a concatenation:
// x is forced to end in "a", z = x ++ y, and "ab" is forbidden, so y may
// not start with "b".
func TestCrossBoundaryForbiddenSat(t *testing.T) {
	p := &Problem{
		Alphabet: "ab",
		Vars: []VarBounds{
			{Min: 1, Max: 1, Prefix: "a"}, // x
			{Min: 1, Max: 1},              // y
			{Min: 2, Max: 2},              // z = x ++ y
		},
		Forbidden: []string{"ab"},
		Concat:    []ConcatEq{{Left: 0, Right: 1, Result: 2}},
	}
	expectSat(t, p, []string{"a", "a", "aa"})
}

// The boundary case made unsatisfiable: x ends with "a", y starts with "b",
// so z = x ++ y necessarily contains "ab" across the boundary.
func TestCrossBoundaryForbiddenUnsat(t *testing.T) {
	p := &Problem{
		Alphabet: "ab",
		Vars: []VarBounds{
			{Min: 1, Max: 1, Suffix: "a"},
			{Min: 1, Max: 1, Prefix: "b"},
			{Min: 2, Max: 2},
		},
		Forbidden: []string{"ab"},
		Concat:    []ConcatEq{{Left: 0, Right: 1, Result: 2}},
	}
	if res := mustSolve(t, p); res.Status != Unsat {
		t.Fatalf("status = %v, want unsat", res.Status)
	}
}

// Longer boundary span: forbidden word of length 3 crossing the boundary.
func TestCrossBoundaryLongWord(t *testing.T) {
	p := &Problem{
		Alphabet: "ab",
		Vars: []VarBounds{
			{Min: 2, Max: 2, Suffix: "a"}, // x ends in "a"
			{Min: 2, Max: 2, Prefix: "b"},
			{Min: 4, Max: 4},
		},
		Forbidden: []string{"abb"},
		Concat:    []ConcatEq{{Left: 0, Right: 1, Result: 2}},
	}
	// x ends in "a", y starts with "b"; y = "bb" would make z contain "abb"
	// across the boundary, so y must be "ba". x is free except its suffix.
	expectSat(t, p, []string{"aa", "ba", "aaba"})
}

func TestEmptyAssignmentIsSmallest(t *testing.T) {
	p := &Problem{
		Alphabet: "ab",
		Vars:     []VarBounds{{Min: 0, Max: 2}},
	}
	expectSat(t, p, []string{""})
}

// Total length is minimized before lexicographic order: x stays empty even
// though "a" < "b".
func TestTotalLengthBeforeLex(t *testing.T) {
	p := &Problem{
		Alphabet: "ab",
		Vars: []VarBounds{
			{Min: 0, Max: 2},
			{Min: 0, Max: 2, Prefix: "b"},
		},
	}
	expectSat(t, p, []string{"", "b"})
}

func TestPrefixSuffixConflict(t *testing.T) {
	p := &Problem{
		Alphabet: "ab",
		Vars:     []VarBounds{{Min: 1, Max: 1, Prefix: "a", Suffix: "b"}},
	}
	if res := mustSolve(t, p); res.Status != Unsat {
		t.Fatalf("status = %v, want unsat", res.Status)
	}
}

func TestPrefixSuffixOverlapConsistent(t *testing.T) {
	p := &Problem{
		Alphabet: "ab",
		Vars:     []VarBounds{{Min: 3, Max: 3, Prefix: "ab", Suffix: "ba"}},
	}
	expectSat(t, p, []string{"aba"})
}

// Budget exhaustion must yield Unknown, never Unsat.
func TestBudgetExhaustion(t *testing.T) {
	p := &Problem{
		Alphabet:  "ab",
		Vars:      []VarBounds{{Min: 2, Max: 2}},
		Forbidden: []string{"aa", "ab", "ba", "bb"},
	}
	if res := Solve(p, 1); res.Status != Unknown {
		t.Fatalf("status = %v with tiny budget, want unknown", res.Status)
	}
	if res := Solve(p, 0); res.Status != Unsat {
		t.Fatalf("status = %v with unlimited budget, want unsat", res.Status)
	}
}

// An unsat problem with a generous budget must be proved Unsat, and the
// same problem with a tiny budget must not be reported Unsat.
func TestBudgetNeverFalseUnsat(t *testing.T) {
	p := &Problem{
		Alphabet:  "abc",
		Vars:      []VarBounds{{Min: 3, Max: 3}, {Min: 3, Max: 3}},
		Forbidden: []string{"a", "b", "c"},
	}
	if res := Solve(p, 2); res.Status == Unsat {
		t.Fatalf("reported unsat on incomplete search")
	}
	if res := Solve(p, 0); res.Status != Unsat {
		t.Fatalf("status = %v, want unsat", res.Status)
	}
}

func TestVerifyRejectsTampering(t *testing.T) {
	p := &Problem{
		Alphabet:  "ab",
		Vars:      []VarBounds{{Min: 1, Max: 2}, {Min: 1, Max: 2}},
		Forbidden: []string{"ab"},
		Concat:    []ConcatEq{{Left: 0, Right: 0, Result: 1}},
	}
	good := []string{"a", "aa"}
	if err := Verify(p, good); err != nil {
		t.Fatalf("valid assignment rejected: %v", err)
	}
	bads := [][]string{
		{"a"},        // wrong variable count
		{"a", "aaa"}, // length bound
		{"a", "ac"},  // alphabet
		{"b", "aa"},  // concat equality
		{"a", "ab"},  // concat equality + forbidden
		{"", ""},     // length lower bound
	}
	for i, bad := range bads {
		if err := Verify(p, bad); err == nil {
			t.Fatalf("bad assignment %d (%q) accepted", i, bad)
		}
	}
}

func TestEmptyForbiddenWordIsUnsat(t *testing.T) {
	p := &Problem{
		Alphabet:  "ab",
		Vars:      []VarBounds{{Min: 0, Max: 1}},
		Forbidden: []string{""},
	}
	if res := mustSolve(t, p); res.Status != Unsat {
		t.Fatalf("status = %v, want unsat", res.Status)
	}
}

// randomProblem builds a small random instance suitable for brute force.
func randomProblem(r *rand.Rand) *Problem {
	alphaSize := 1 + r.Intn(3)
	p := &Problem{Alphabet: "abc"[:alphaSize]}
	n := 1 + r.Intn(3)
	randWord := func(maxLen int) string {
		l := r.Intn(maxLen + 1)
		b := make([]byte, l)
		for i := range b {
			b[i] = p.Alphabet[r.Intn(len(p.Alphabet))]
		}
		return string(b)
	}
	for i := 0; i < n; i++ {
		min := r.Intn(3)
		max := min + r.Intn(3)
		if max > 3 {
			max = 3
		}
		v := VarBounds{Min: min, Max: max}
		if r.Intn(2) == 0 {
			v.Prefix = randWord(2)
		}
		if r.Intn(2) == 0 {
			v.Suffix = randWord(2)
		}
		p.Vars = append(p.Vars, v)
	}
	for i, nf := 0, r.Intn(4); i < nf; i++ {
		p.Forbidden = append(p.Forbidden, randWord(2)+string(p.Alphabet[r.Intn(len(p.Alphabet))]))
	}
	for i, nc := 0, r.Intn(3); i < nc; i++ {
		p.Concat = append(p.Concat, ConcatEq{r.Intn(n), r.Intn(n), r.Intn(n)})
	}
	return p
}

// domainSize estimates the brute-force search space size.
func domainSize(p *Problem) int64 {
	total := int64(1)
	alpha := int64(len(p.Alphabet))
	for _, v := range p.Vars {
		var perVar int64
		pow := int64(1)
		for l := 0; l <= v.Max; l++ {
			if l >= v.Min {
				perVar += pow
			}
			pow *= alpha
		}
		total *= perVar
		if total > 300000 {
			return total
		}
	}
	return total
}

// Cross-check Solve against exhaustive enumeration on random small
// instances, which exercises cross-variable boundary forbidden substrings
// among everything else.
func TestSolveMatchesBruteForce(t *testing.T) {
	r := rand.New(rand.NewSource(1))
	tested := 0
	for tested < 150 {
		p := randomProblem(r)
		if domainSize(p) > 300000 {
			continue
		}
		if err := p.Validate(); err != nil {
			t.Fatalf("generated invalid problem: %v", err)
		}
		got := Solve(p, 0)
		want := BruteForce(p)
		if got.Status != want.Status {
			t.Fatalf("problem %+v: solve = %v, brute = %v", p, got.Status, want.Status)
		}
		if got.Status == Sat {
			if !reflect.DeepEqual(got.Assignment, want.Assignment) {
				t.Fatalf("problem %+v: solve = %q, brute = %q", p, got.Assignment, want.Assignment)
			}
			if err := Verify(p, got.Assignment); err != nil {
				t.Fatalf("problem %+v: assignment failed check: %v", p, err)
			}
		}
		tested++
	}
}
