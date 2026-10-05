package fd

import "fmt"

// EquivReport details an equivalence check between two FD sets.
type EquivReport struct {
	Equivalent bool
	// Failures lists FDs of one set not derivable from the other.
	Failures []string
	// Proofs holds one closure trace per checked FD (both directions),
	// as evidence for the verdict.
	Proofs []string
}

// Equivalent checks whether F and G cover each other: every FD of G is
// implied by F and vice versa. The check is deterministic and records
// a derivation trace for each verified dependency.
func Equivalent(f, g []FD) EquivReport {
	rep := EquivReport{Equivalent: true}
	check := func(from, to []FD, dir string) {
		for _, dep := range to {
			closure, trace := Closure(dep.LHS, from)
			if !closure.Contains(dep.RHS) {
				rep.Equivalent = false
				rep.Failures = append(rep.Failures,
					fmt.Sprintf("%s: (%s) not derivable", dir, dep))
				continue
			}
			rep.Proofs = append(rep.Proofs,
				fmt.Sprintf("%s: (%s) derived\n%s", dir, dep, Indent(trace.Render())))
		}
	}
	check(f, g, "F |= G")
	check(g, f, "G |= F")
	return rep
}

// Indent prefixes every line of s with four spaces, for nested output.
func Indent(s string) string {
	out := ""
	for _, line := range splitLines(s) {
		out += "    " + line + "\n"
	}
	return out
}

func splitLines(s string) []string {
	var lines []string
	cur := ""
	for _, r := range s {
		if r == '\n' {
			lines = append(lines, cur)
			cur = ""
			continue
		}
		cur += string(r)
	}
	if cur != "" {
		lines = append(lines, cur)
	}
	return lines
}
