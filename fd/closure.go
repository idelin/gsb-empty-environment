package fd

import (
	"fmt"
	"sort"
	"strings"
)

// Step records one dependency firing during closure computation:
// which FD was applied and which attributes it contributed.
type Step struct {
	FD    FD
	Added []string
	After []string // closure snapshot after this step
}

func (s Step) String() string {
	return fmt.Sprintf("apply %s: +{%s} => {%s}",
		s.FD, strings.Join(s.Added, ","), strings.Join(s.After, ","))
}

// Closure computes attrs+ under fds: the least fixed point reached
// by repeatedly firing every dependency whose LHS is contained in
// the current set. Dependencies with an empty LHS fire immediately,
// so attributes implied by {} -> A belong to every closure.
func Closure(attrs []string, fds []FD) []string {
	closure, _ := ClosureTrace(attrs, fds)
	return closure
}

// ClosureTrace is Closure plus the ordered list of derivation steps,
// which serves as human-readable evidence for why an attribute (or
// dependency) is implied.
func ClosureTrace(attrs []string, fds []FD) ([]string, []Step) {
	in := make(map[string]bool, len(attrs))
	closure := make([]string, 0, len(attrs))
	for _, a := range normalizeAttrs(attrs) {
		if !in[a] {
			in[a] = true
			closure = append(closure, a)
		}
	}
	var steps []Step
	changed := true
	for changed {
		changed = false
		for _, f := range fds {
			if !SubsetOf(f.LHS, closure) {
				continue
			}
			var added []string
			for _, r := range f.RHS {
				if !in[r] {
					in[r] = true
					added = append(added, r)
				}
			}
			if len(added) == 0 {
				continue
			}
			sort.Strings(added)
			closure = append(closure, added...)
			sort.Strings(closure)
			snapshot := make([]string, len(closure))
			copy(snapshot, closure)
			steps = append(steps, Step{FD: f, Added: added, After: snapshot})
			changed = true
		}
	}
	return closure, steps
}

// FormatTrace renders a derivation as a numbered proof, e.g.
//
//	start: {A}
//	1. apply A -> B: +{B} => {A,B}
//	2. apply B -> C: +{C} => {A,B,C}
func FormatTrace(start []string, steps []Step) string {
	var b strings.Builder
	startSet := normalizeAttrs(start)
	if len(startSet) == 0 {
		b.WriteString("start: {}")
	} else {
		fmt.Fprintf(&b, "start: {%s}", strings.Join(startSet, ","))
	}
	for i, s := range steps {
		fmt.Fprintf(&b, "\n  %d. %s", i+1, s)
	}
	return b.String()
}
