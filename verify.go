package main

import (
	"fmt"
	"strings"
)

// Verify independently checks an assignment against a problem. It shares no
// search logic with the solver, so a SAT answer can be validated on its own.
func Verify(p *Problem, assignment map[string]string) error {
	if assignment == nil {
		return fmt.Errorf("no assignment provided")
	}
	for name := range assignment {
		found := false
		for _, v := range p.Variables {
			if v.Name == name {
				found = true
				break
			}
		}
		if !found {
			return fmt.Errorf("assignment contains unknown variable %q", name)
		}
	}
	alpha := make(map[byte]bool)
	for i := 0; i < len(p.Alphabet); i++ {
		alpha[p.Alphabet[i]] = true
	}
	var concat []byte
	for _, v := range p.Variables {
		val, ok := assignment[v.Name]
		if !ok {
			return fmt.Errorf("variable %q is not assigned", v.Name)
		}
		for k := 0; k < len(val); k++ {
			if !alpha[val[k]] {
				return fmt.Errorf("variable %q contains character %q outside the alphabet", v.Name, val[k])
			}
		}
		if len(val) < v.MinLen || len(val) > v.MaxLen {
			return fmt.Errorf("variable %q has length %d outside [%d,%d]", v.Name, len(val), v.MinLen, v.MaxLen)
		}
		if !strings.HasPrefix(val, v.Prefix) {
			return fmt.Errorf("variable %q does not have required prefix %q", v.Name, v.Prefix)
		}
		if !strings.HasSuffix(val, v.Suffix) {
			return fmt.Errorf("variable %q does not have required suffix %q", v.Name, v.Suffix)
		}
		concat = append(concat, val...)
	}
	full := string(concat)
	for _, f := range p.Forbidden {
		if strings.Contains(full, f) {
			return fmt.Errorf("concatenation contains forbidden substring %q", f)
		}
	}
	for _, eq := range p.Equalities {
		l := joinVars(eq.Left, assignment)
		r := joinVars(eq.Right, assignment)
		if l != r {
			return fmt.Errorf("equality violated: %q != %q", l, r)
		}
	}
	return nil
}

func joinVars(names []string, assignment map[string]string) string {
	out := ""
	for _, n := range names {
		out += assignment[n]
	}
	return out
}
