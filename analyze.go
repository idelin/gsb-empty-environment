package netsec

import (
	"fmt"
)

// version is the lattice element for object field versions. Labels are
// assigned per check/write/call node; top joins two different versions (the
// location may differ across incoming paths).
type version struct {
	label int
	top   bool
}

func joinVersion(a, b version) version {
	if a == b {
		return a
	}
	return version{top: true}
}

// fact is a check fact bound to one concrete target's version at the check.
type fact struct {
	check  string
	target Target
	bound  version
}

// flowState is the per-node dataflow state. Facts only shrink (a write removes
// them) while versions only rise toward top, so the transfer is monotone and
// the worklist reaches a fixpoint on a finite lattice.
type flowState struct {
	versions map[Target]version
	facts    map[fact]struct{}
	// mayDef is the set of targets that may have been defined (checked or
	// written) along the path that produced this state.
	mayDef map[Target]bool
}

func initialState(relevant []Target) *flowState {
	s := &flowState{
		versions: make(map[Target]version, len(relevant)),
		facts:    map[fact]struct{}{},
		mayDef:   map[Target]bool{},
	}
	for _, t := range relevant {
		s.versions[t] = version{}
	}
	return s
}

func (s *flowState) clone() *flowState {
	c := &flowState{
		versions: make(map[Target]version, len(s.versions)),
		facts:    make(map[fact]struct{}, len(s.facts)),
		mayDef:   make(map[Target]bool, len(s.mayDef)),
	}
	for k, v := range s.versions {
		c.versions[k] = v
	}
	for f := range s.facts {
		c.facts[f] = struct{}{}
	}
	for t := range s.mayDef {
		c.mayDef[t] = true
	}
	return c
}

func statesEqual(a, b *flowState) bool {
	if len(a.versions) != len(b.versions) || len(a.facts) != len(b.facts) || len(a.mayDef) != len(b.mayDef) {
		return false
	}
	for k, v := range a.versions {
		if b.versions[k] != v {
			return false
		}
	}
	for f := range a.facts {
		if _, ok := b.facts[f]; !ok {
			return false
		}
	}
	for t := range a.mayDef {
		if !b.mayDef[t] {
			return false
		}
	}
	return true
}

// joinStates merges predecessor states. A field version is raised to top only
// when two merged paths both define that target with conflicting labels; a
// field that no merged path defines keeps its inherited version unchanged, so
// a length fact survives a loop body that only writes unrelated fields.
func joinStates(parts []*flowState, relevant []Target) *flowState {
	if len(parts) == 0 {
		return initialState(relevant)
	}
	out := parts[0].clone()
	for i := 1; i < len(parts); i++ {
		p := parts[i]
		for _, t := range relevant {
			if !p.mayDef[t] {
				// This predecessor never defines t; it cannot introduce a
				// competing version, so leave the merged version untouched.
				continue
			}
			a := out.versions[t]
			b := p.versions[t]
			if a == b {
				continue
			}
			if !out.mayDef[t] {
				out.versions[t] = b
				continue
			}
			out.versions[t] = joinVersion(a, b)
		}
		for f := range p.facts {
			out.facts[f] = struct{}{}
		}
		for t := range p.mayDef {
			out.mayDef[t] = true
		}
	}

	facts := make(map[fact]struct{}, len(out.facts))
	for f := range out.facts {
		joinedVersion := out.versions[f.target]
		// Keep the fact only when the bound label still equals the joined field
		// version and is not top: then every path reaching the join agrees that
		// this check's version is the current one.
		if !f.bound.top && !joinedVersion.top && f.bound == joinedVersion {
			facts[f] = struct{}{}
		}
	}
	out.facts = facts
	return out
}

func lookupFact(facts map[fact]struct{}, checkID string, t Target) (fact, bool) {
	for f := range facts {
		if f.check == checkID && f.target == t {
			return f, true
		}
	}
	return fact{}, false
}

type analyzer struct {
	prog  *Program
	nodes map[string]Node
	preds map[string][]string

	checkTargets map[string][]Target
	readTargets  map[string][]Target
	writeTargets map[string][]Target
	callTargets  map[string][]Target

	relevant []Target
	labels   map[string]int
	states   map[string]*flowState
}

// Analyze runs the check-validity analysis and returns the fixpoint states
// together with every check-to-read gap that may be exploitable.
func Analyze(prog *Program) *Result {
	a := newAnalyzer(prog)
	a.solve()
	return a.buildResult()
}

func (a *analyzer) summary(id string) *CallSummary {
	for i := range a.prog.Summaries {
		if a.prog.Summaries[i].ID == id {
			return &a.prog.Summaries[i]
		}
	}
	return nil
}

func (a *analyzer) gapMessage(kind, readID string, t Target, invalidator string) string {
	loc := t.Object
	if t.Field != "" {
		loc += "." + t.Field
	}
	switch kind {
	case GapInvalidated:
		return fmt.Sprintf("read %s: check fact for %s invalidated by %s", readID, loc, invalidator)
	default:
		return fmt.Sprintf("read %s: no valid check fact for %s", readID, loc)
	}
}
