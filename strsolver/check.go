package strsolver

import (
	"fmt"
	"strings"
)

// Verify independently checks that assignment satisfies every constraint of
// p: variable count, length bounds, alphabet membership, prefix/suffix,
// forbidden substrings (including ones spanning a concatenation boundary,
// since the concatenated result is itself a constrained variable), and the
// concatenation equalities. It returns nil iff the assignment is valid.
func Verify(p *Problem, assignment []string) error {
	if len(assignment) != len(p.Vars) {
		return fmt.Errorf("expected %d variables, got %d", len(p.Vars), len(assignment))
	}
	var inAlpha [256]bool
	for i := 0; i < len(p.Alphabet); i++ {
		inAlpha[p.Alphabet[i]] = true
	}
	for i, v := range p.Vars {
		w := assignment[i]
		if len(w) < v.Min || len(w) > v.Max {
			return fmt.Errorf("var %d: length %d outside [%d, %d]", i, len(w), v.Min, v.Max)
		}
		for j := 0; j < len(w); j++ {
			if !inAlpha[w[j]] {
				return fmt.Errorf("var %d: byte %q not in alphabet", i, w[j])
			}
		}
		if !strings.HasPrefix(w, v.Prefix) {
			return fmt.Errorf("var %d: %q misses prefix %q", i, w, v.Prefix)
		}
		if !strings.HasSuffix(w, v.Suffix) {
			return fmt.Errorf("var %d: %q misses suffix %q", i, w, v.Suffix)
		}
		for _, f := range p.Forbidden {
			if strings.Contains(w, f) {
				return fmt.Errorf("var %d: %q contains forbidden substring %q", i, w, f)
			}
		}
	}
	n := len(p.Vars)
	for _, c := range p.Concat {
		for _, idx := range [3]int{c.Left, c.Right, c.Result} {
			if idx < 0 || idx >= n {
				return fmt.Errorf("concat constraint references invalid variable %d", idx)
			}
		}
		if got, want := assignment[c.Result], assignment[c.Left]+assignment[c.Right]; got != want {
			return fmt.Errorf("concat violated: x%d = %q, want x%d+x%d = %q",
				c.Result, got, c.Left, c.Right, want)
		}
	}
	return nil
}
