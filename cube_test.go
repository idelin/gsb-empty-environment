package main

import (
	"math/rand"
	"testing"
)

// cubesFor parses both expressions, computes the diff truth table and the
// prime implicants, and verifies them against the truth table.
func cubesFor(t *testing.T, srcA, srcB string) ([]Cube, []string, []bool) {
	t.Helper()
	ea, err := ParseExpr(srcA)
	if err != nil {
		t.Fatalf("ParseExpr(%q): %v", srcA, err)
	}
	eb, err := ParseExpr(srcB)
	if err != nil {
		t.Fatalf("ParseExpr(%q): %v", srcB, err)
	}
	vars := SortedVars(ea, eb)
	if len(vars) > 10 {
		t.Fatalf("too many variables: %d", len(vars))
	}
	diff := DiffTable(ea, eb, vars)
	cubes := PrimeImplicants(diff, uint(len(vars)))
	if err := VerifyCubes(cubes, diff, uint(len(vars))); err != nil {
		t.Fatalf("VerifyCubes(%q vs %q): %v", srcA, srcB, err)
	}
	return cubes, vars, diff
}

func cubeStrings(cubes []Cube, vars []string) []string {
	out := make([]string, len(cubes))
	for i, c := range cubes {
		out[i] = c.String(vars)
	}
	return out
}

func equalSet(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	m := map[string]int{}
	for _, s := range a {
		m[s]++
	}
	for _, s := range b {
		m[s]--
		if m[s] < 0 {
			return false
		}
	}
	return true
}

func TestPrimeImplicantsFixedCases(t *testing.T) {
	cases := []struct {
		name string
		a, b string
		want []string
	}{
		{"equivalent", "a & b", "b & a", nil},
		{"single literal", "a", "0", []string{"a=1"}},
		{"one cube", "a & b", "a", []string{"a=1 b=0"}},
		{"xor pair", "a", "b", []string{"a=0 b=1", "a=1 b=0"}},
		{"or diff", "a | b", "0", []string{"a=1", "b=1"}},
		{"consensus", "a & b | !a & c", "0", []string{"a=1 b=1", "a=0 c=1", "b=1 c=1"}},
		{"always differ", "1", "0", []string{"(true)"}},
		{"nested", "a & (b | c)", "a & b | c", []string{"a=0 c=1"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cubes, vars, _ := cubesFor(t, tc.a, tc.b)
			got := cubeStrings(cubes, vars)
			if !equalSet(got, tc.want) {
				t.Errorf("cubes = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestMinimalityAndCoverageBruteForce re-checks every reported cube against
// the truth table with independent per-cube assertions, and confirms that no
// implicant cube is missing from the result.
func TestMinimalityAndCoverageBruteForce(t *testing.T) {
	pairs := [][2]string{
		{"a & (b | c)", "a & b | c"},
		{"(a ^ b) & c", "a | (b & !c)"},
		{"a & b & c", "!a | !b"},
		{"a ^ b ^ c", "a & b"},
	}
	for _, pr := range pairs {
		cubes, vars, diff := cubesFor(t, pr[0], pr[1])
		n := uint(len(vars))
		for _, c := range cubes {
			// soundness: every covered assignment differs
			for a := 0; a < len(diff); a++ {
				if c.Covers(uint64(a)) && !diff[a] {
					t.Errorf("%q vs %q: cube %v covers agreeing assignment %d",
						pr[0], pr[1], c.String(vars), a)
				}
			}
			// minimality: dropping any literal covers an agreeing assignment
			for i := uint(0); i < n; i++ {
				bit := uint64(1) << i
				if c.mask&bit == 0 {
					continue
				}
				dropped := Cube{mask: c.mask &^ bit, vals: c.vals &^ bit}
				ok := true
				for a := 0; a < len(diff); a++ {
					if dropped.Covers(uint64(a)) && !diff[a] {
						ok = false
						break
					}
				}
				if ok {
					t.Errorf("%q vs %q: cube %v not minimal (drop var %s)",
						pr[0], pr[1], c.String(vars), vars[i])
				}
			}
		}
		// completeness: every differing assignment is covered
		for a := 0; a < len(diff); a++ {
			if !diff[a] {
				continue
			}
			covered := false
			for _, c := range cubes {
				if c.Covers(uint64(a)) {
					covered = true
					break
				}
			}
			if !covered {
				t.Errorf("%q vs %q: differing assignment %d uncovered",
					pr[0], pr[1], a)
			}
		}
	}
}

// randomExpr builds a random expression over vars with bounded depth.
func randomExpr(r *rand.Rand, vars []string, depth int) string {
	if depth == 0 || r.Intn(3) == 0 {
		return vars[r.Intn(len(vars))]
	}
	switch r.Intn(4) {
	case 0:
		return "!(" + randomExpr(r, vars, depth-1) + ")"
	case 1:
		return "(" + randomExpr(r, vars, depth-1) + " & " + randomExpr(r, vars, depth-1) + ")"
	case 2:
		return "(" + randomExpr(r, vars, depth-1) + " | " + randomExpr(r, vars, depth-1) + ")"
	default:
		return "(" + randomExpr(r, vars, depth-1) + " ^ " + randomExpr(r, vars, depth-1) + ")"
	}
}

// TestRandomPairsVerified generates many random expression pairs and checks
// soundness, minimality, and completeness against the truth table each time.
func TestRandomPairsVerified(t *testing.T) {
	r := rand.New(rand.NewSource(42))
	vars := []string{"a", "b", "c", "d", "e"}
	for iter := 0; iter < 300; iter++ {
		a := randomExpr(r, vars, 3)
		b := randomExpr(r, vars, 3)
		cubes, _, diff := cubesFor(t, a, b)
		if err := VerifyCubes(cubes, diff, uint(len(vars))); err != nil {
			t.Fatalf("iter %d (%q vs %q): %v", iter, a, b, err)
		}
	}
}

// TestTenVariables exercises the maximum supported variable count.
func TestTenVariables(t *testing.T) {
	a := "v0 & v1 & v2 & v3 & v4 & v5 & v6 & v7 & v8 & v9"
	b := "v0 & v1 & v2 & v3 & v4 & v5 & v6 & v7 & v8 & !v9"
	cubes, vars, diff := cubesFor(t, a, b)
	if len(vars) != 10 {
		t.Fatalf("got %d vars, want 10", len(vars))
	}
	got := cubeStrings(cubes, vars)
	want := []string{"v0=1 v1=1 v2=1 v3=1 v4=1 v5=1 v6=1 v7=1 v8=1"}
	if !equalSet(got, want) {
		t.Errorf("cubes = %v, want %v", got, want)
	}
	diffCount := 0
	for _, d := range diff {
		if d {
			diffCount++
		}
	}
	if diffCount != 2 {
		t.Errorf("diff count = %d, want 2", diffCount)
	}
}
