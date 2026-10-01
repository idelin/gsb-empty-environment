// Package reasoner implements a reversible fact-deduction engine based on
// forward chaining over Horn clauses, with proof trees, minimal explanations
// and truth maintenance (cascading retraction).
package reasoner

import (
	"fmt"
	"sort"
	"strings"
)

// Fact is an atomic proposition identified by its name.
type Fact string

// Rule is a Horn clause: when every premise holds, the conclusion holds.
type Rule struct {
	Name       string
	Premises   []Fact
	Conclusion Fact
}

func (r *Rule) String() string {
	if len(r.Premises) == 0 {
		return fmt.Sprintf("%s: => %s", r.Name, r.Conclusion)
	}
	return fmt.Sprintf("%s: %s => %s", r.Name, joinFacts(r.Premises), r.Conclusion)
}

// factState tracks why a fact currently holds: it is either asserted as a
// base fact and/or derived by one or more rules whose premises all hold.
type factState struct {
	asserted    bool
	derivations []*Rule
}

// Engine is a forward-chaining reasoner with truth maintenance.
type Engine struct {
	rules     []*Rule
	byPremise map[Fact][]*Rule // premise -> rules that mention it
	facts     map[Fact]*factState
}

// New builds an Engine over the given Horn clauses.
func New(rules ...*Rule) *Engine {
	e := &Engine{
		byPremise: make(map[Fact][]*Rule),
		facts:     make(map[Fact]*factState),
	}
	for _, r := range rules {
		e.rules = append(e.rules, r)
		for _, p := range r.Premises {
			e.byPremise[p] = append(e.byPremise[p], r)
		}
	}
	return e
}

// Known reports whether f currently holds (asserted or derived).
func (e *Engine) Known(f Fact) bool {
	st, ok := e.facts[f]
	return ok && (st.asserted || len(st.derivations) > 0)
}

// Asserted reports whether f is an asserted base fact.
func (e *Engine) Asserted(f Fact) bool {
	st, ok := e.facts[f]
	return ok && st.asserted
}

// Facts returns all currently known facts in sorted order.
func (e *Engine) Facts() []Fact {
	out := make([]Fact, 0, len(e.facts))
	for f, st := range e.facts {
		if st.asserted || len(st.derivations) > 0 {
			out = append(out, f)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// Assert records f as a base fact and forward-chains any new conclusions.
func (e *Engine) Assert(f Fact) {
	st := e.state(f)
	if st.asserted {
		return
	}
	wasKnown := st.asserted || len(st.derivations) > 0
	st.asserted = true
	if !wasKnown {
		e.propagate(f)
	}
}

// Retract removes the base fact f. Derived conclusions that lose their last
// independent support are removed cascadingly; conclusions with remaining
// independent derivations are kept.
func (e *Engine) Retract(f Fact) error {
	st, ok := e.facts[f]
	if !ok || !st.asserted {
		return fmt.Errorf("cannot retract %q: not an asserted base fact", f)
	}
	st.asserted = false
	if len(st.derivations) > 0 {
		return nil // still supported by derivation
	}
	e.removeFact(f)
	return nil
}

func (e *Engine) state(f Fact) *factState {
	st, ok := e.facts[f]
	if !ok {
		st = &factState{}
		e.facts[f] = st
	}
	return st
}

// propagate fires every rule that has f as a premise and is now fully satisfied.
func (e *Engine) propagate(f Fact) {
	for _, r := range e.byPremise[f] {
		if e.allKnown(r.Premises) {
			e.derive(r)
		}
	}
}

func (e *Engine) allKnown(premises []Fact) bool {
	for _, p := range premises {
		if !e.Known(p) {
			return false
		}
	}
	return true
}

// derive records rule r as a support for its conclusion and propagates if the
// conclusion is newly known.
func (e *Engine) derive(r *Rule) {
	st := e.state(r.Conclusion)
	for _, d := range st.derivations {
		if d == r {
			return
		}
	}
	wasKnown := st.asserted || len(st.derivations) > 0
	st.derivations = append(st.derivations, r)
	if !wasKnown {
		e.propagate(r.Conclusion)
	}
}

// removeFact deletes f and cascades: every rule depending on f loses its
// derivation, and conclusions left without any support are removed too.
func (e *Engine) removeFact(f Fact) {
	delete(e.facts, f)
	for _, r := range e.byPremise[f] {
		st := e.facts[r.Conclusion]
		if st == nil {
			continue
		}
		for i, d := range st.derivations {
			if d == r {
				st.derivations = append(st.derivations[:i], st.derivations[i+1:]...)
				break
			}
		}
		if !st.asserted && len(st.derivations) == 0 {
			e.removeFact(r.Conclusion)
		}
	}
}

func joinFacts(fs []Fact) string {
	parts := make([]string, len(fs))
	for i, f := range fs {
		parts[i] = string(f)
	}
	return strings.Join(parts, " & ")
}
