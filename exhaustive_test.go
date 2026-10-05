package main

import (
	"fmt"
	"math/rand"
	"testing"
)

// bruteForce enumerates every assignment and returns the optimal one:
// minimal total length, then lexicographically smallest concatenation under
// the alphabet order. Constraint checking is delegated to the independent
// Verify, so the solver is cross-checked against a completely separate path.
func bruteForce(p *Problem, rank *[256]int) (best map[string]string, bestConcat string, ok bool) {
	n := len(p.Variables)
	vals := make([]string, n)
	var rec func(i int)
	rec = func(i int) {
		if i == n {
			assign := make(map[string]string, n)
			concat := ""
			for k, v := range p.Variables {
				assign[v.Name] = vals[k]
				concat += vals[k]
			}
			if Verify(p, assign) != nil {
				return
			}
			if !ok || len(concat) < len(bestConcat) ||
				(len(concat) == len(bestConcat) && cmpRank(concat, bestConcat, rank) < 0) {
				ok = true
				bestConcat = concat
				best = assign
			}
			return
		}
		v := p.Variables[i]
		for l := v.MinLen; l <= v.MaxLen; l++ {
			var gen func(cur []byte)
			gen = func(cur []byte) {
				if len(cur) == l {
					vals[i] = string(cur)
					rec(i + 1)
					return
				}
				for k := 0; k < len(p.Alphabet); k++ {
					gen(append(cur, p.Alphabet[k]))
				}
			}
			gen(nil)
		}
	}
	rec(0)
	return best, bestConcat, ok
}

func computeRank(alphabet string) *[256]int {
	var rank [256]int
	for i := range rank {
		rank[i] = 255
	}
	for i := 0; i < len(alphabet); i++ {
		rank[alphabet[i]] = i
	}
	return &rank
}

func crossCheck(t *testing.T, p *Problem) {
	t.Helper()
	if err := p.Validate(); err != nil {
		t.Fatalf("invalid generated problem: %v", err)
	}
	rank := computeRank(p.Alphabet)
	_, wantConcat, ok := bruteForce(p, rank)
	sol := Solve(p)
	if !ok {
		if sol.Status != StatusUNSAT {
			t.Fatalf("brute force: UNSAT, solver: %s (%v)", sol.Status, sol.Assignment)
		}
		return
	}
	if sol.Status != StatusSAT {
		t.Fatalf("brute force: SAT with concat %q, solver: %s", wantConcat, sol.Status)
	}
	if err := Verify(p, sol.Assignment); err != nil {
		t.Fatalf("solver assignment fails independent verification: %v", err)
	}
	gotConcat := concatOf(p, sol.Assignment)
	if len(gotConcat) != len(wantConcat) {
		t.Fatalf("total length = %d, want %d", len(gotConcat), len(wantConcat))
	}
	if gotConcat != wantConcat {
		t.Fatalf("concat = %q, want lex-min %q", gotConcat, wantConcat)
	}
	if sol.TotalLength != len(wantConcat) {
		t.Fatalf("reported total length = %d, want %d", sol.TotalLength, len(wantConcat))
	}
}

// Every subset of the length-2 words over {a,b} as forbidden sets, with
// one-character variables: forbidden substrings can only ever form across
// the variable boundary.
func TestExhaustiveBoundaryForbidden(t *testing.T) {
	words := []string{"aa", "ab", "ba", "bb"}
	for mask := 0; mask < 1<<uint(len(words)); mask++ {
		p := &Problem{
			Alphabet: "ab",
			Variables: []VarSpec{
				{Name: "X", MinLen: 1, MaxLen: 1},
				{Name: "Y", MinLen: 1, MaxLen: 1},
			},
		}
		for i, w := range words {
			if mask&(1<<uint(i)) != 0 {
				p.Forbidden = append(p.Forbidden, w)
			}
		}
		t.Run(fmt.Sprintf("mask%02d", mask), func(t *testing.T) {
			crossCheck(t, p)
		})
	}
}

// Random small instances cross-checked against brute force, including
// prefixes, suffixes, forbidden substrings and concatenation equalities.
func TestExhaustiveRandom(t *testing.T) {
	for seed := int64(0); seed < 400; seed++ {
		p := randomProblem(rand.New(rand.NewSource(seed)))
		t.Run(fmt.Sprintf("seed%d", seed), func(t *testing.T) {
			crossCheck(t, p)
		})
	}
}

func randomProblem(r *rand.Rand) *Problem {
	alphaSize := 1 + r.Intn(2)
	if r.Intn(10) == 0 {
		alphaSize = 3
	}
	alphabet := string("abc"[:alphaSize])
	maxLen := 3
	if alphaSize == 3 {
		maxLen = 2
	}
	randWord := func(max int) string {
		b := make([]byte, r.Intn(max+1))
		for i := range b {
			b[i] = alphabet[r.Intn(alphaSize)]
		}
		return string(b)
	}
	n := 1 + r.Intn(3)
	p := &Problem{Alphabet: alphabet}
	for i := 0; i < n; i++ {
		lo := r.Intn(2)
		vs := VarSpec{
			Name:   fmt.Sprintf("V%d", i),
			MinLen: lo,
			MaxLen: lo + r.Intn(maxLen-lo+1),
		}
		if r.Intn(2) == 0 {
			vs.Prefix = randWord(2)
		}
		if r.Intn(2) == 0 {
			vs.Suffix = randWord(2)
		}
		p.Variables = append(p.Variables, vs)
	}
	for k := r.Intn(4); k > 0; k-- {
		if w := randWord(3); w != "" {
			p.Forbidden = append(p.Forbidden, w)
		}
	}
	if n >= 2 && r.Intn(2) == 0 {
		side := func() []string {
			out := []string{}
			for cnt := 1 + r.Intn(2); cnt > 0; cnt-- {
				out = append(out, fmt.Sprintf("V%d", r.Intn(n)))
			}
			return out
		}
		p.Equalities = append(p.Equalities, Equality{Left: side(), Right: side()})
	}
	return p
}
