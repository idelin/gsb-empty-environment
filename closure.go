package fd

import (
	"fmt"
	"sort"
)

// TraceStep records one derivation step: applying FD fds[FDIndex]
// (rendered as Applied) added the attributes Added to the closure.
type TraceStep struct {
	FDIndex int
	Applied FD
	Added   AttrSet
}

// Trace is the full derivation evidence for a closure computation.
type Trace struct {
	Start AttrSet
	Steps []TraceStep
}

// Render formats the trace as human-readable derivation evidence.
func (t Trace) Render() string {
	if len(t.Steps) == 0 {
		return fmt.Sprintf("start %s; no rule applies", t.Start)
	}
	out := fmt.Sprintf("start %s", t.Start)
	cur := append(AttrSet{}, t.Start...)
	inCur := make(map[string]bool, len(cur))
	for _, a := range cur {
		inCur[a] = true
	}
	for i, st := range t.Steps {
		for _, a := range st.Added {
			if !inCur[a] {
				inCur[a] = true
				cur = append(cur, a)
			}
		}
		sort.Strings(cur)
		out += fmt.Sprintf("\n  step %d: apply (%s) => +%s, closure now %s",
			i+1, st.Applied, st.Added, AttrSet(cur))
	}
	return out
}

// Closure computes attrs+ under fds, recording every derivation step.
// fds are scanned in their given (sorted) order each pass, so the trace
// — and therefore the reported evidence — is deterministic.
func Closure(attrs AttrSet, fds []FD) (AttrSet, Trace) {
	tr := Trace{Start: attrs}
	inClosure := make(map[string]bool, len(attrs))
	for _, a := range attrs {
		inClosure[a] = true
	}
	for changed := true; changed; {
		changed = false
		for i, f := range fds {
			if inClosure[f.RHS] {
				continue
			}
			derivable := true
			for _, a := range f.LHS {
				if !inClosure[a] {
					derivable = false
					break
				}
			}
			if !derivable {
				continue
			}
			inClosure[f.RHS] = true
			tr.Steps = append(tr.Steps, TraceStep{
				FDIndex: i,
				Applied: f,
				Added:   AttrSet{f.RHS},
			})
			changed = true
		}
	}
	closure := make(AttrSet, 0, len(inClosure))
	for a := range inClosure {
		closure = append(closure, a)
	}
	sort.Strings(closure)
	return closure, tr
}

// ClosureOf is Closure without the trace.
func ClosureOf(attrs AttrSet, fds []FD) AttrSet {
	c, _ := Closure(attrs, fds)
	return c
}
