package netsec

import (
	"errors"
	"fmt"
)

// Effect is the decision carried by a policy statement or an organization
// boundary layer.
type Effect string

const (
	// EffectAllow grants the covered actions and resources.
	EffectAllow Effect = "allow"
	// EffectDeny overrides any allow for the covered actions and resources.
	EffectDeny Effect = "deny"
)

// Action is a finite, host-defined operation id. The evaluator only reasons
// about actions explicitly registered on the Engine; unknown actions are
// rejected up front instead of being silently denied.
type Action string

// Statement is a single policy rule.
//
// Actions lists the finite actions the rule applies to. Resources is the set
// of closed resource-id intervals the rule applies to; an empty IntervalSet
// matches nothing.
type Statement struct {
	Name      string      `json:"name"`
	Effect    Effect      `json:"effect"`
	Actions   []Action    `json:"actions"`
	Resources IntervalSet `json:"resources"`
}

// Policy is an ordered collection of statements. Statement order does not
// change the result — deny always wins within one policy and within the union
// of the identity and resource policies — but statement names are preserved
// so decisions can be traced back to their source.
type Policy struct {
	Name       string      `json:"name"`
	Statements []Statement `json:"statements"`
}

// compiledStatement is a statement expanded against the known action set and
// pre-split by effect for fast per-action evaluation.
type compiledStatement struct {
	source    string
	effect    Effect
	resources IntervalSet
}

type compiledPolicy struct {
	// allows and denies are keyed by action; each entry lists the statements
	// that mention that action in deterministic input order.
	allows map[Action][]compiledStatement
	denies map[Action][]compiledStatement
}

func compilePolicy(p Policy, known map[Action]struct{}) (compiledPolicy, error) {
	cp := compiledPolicy{
		allows: map[Action][]compiledStatement{},
		denies: map[Action][]compiledStatement{},
	}
	for idx, st := range p.Statements {
		source := st.Name
		if source == "" {
			source = fmt.Sprintf("%s.statement[%d]", p.Name, idx)
		} else {
			source = fmt.Sprintf("%s/%s", p.Name, st.Name)
		}
		switch st.Effect {
		case EffectAllow, EffectDeny:
		default:
			return compiledPolicy{}, fmt.Errorf("netsec: statement %q has invalid effect %q", source, st.Effect)
		}
		if len(st.Actions) == 0 {
			return compiledPolicy{}, fmt.Errorf("netsec: statement %q declares no actions", source)
		}
		for _, action := range st.Actions {
			if _, ok := known[action]; !ok {
				return compiledPolicy{}, fmt.Errorf("netsec: statement %q references unknown action %q", source, action)
			}
		}
		cs := compiledStatement{source: source, effect: st.Effect, resources: st.Resources}
		for _, action := range st.Actions {
			if st.Effect == EffectAllow {
				cp.allows[action] = append(cp.allows[action], cs)
			} else {
				cp.denies[action] = append(cp.denies[action], cs)
			}
		}
	}
	return cp, nil
}

// grantSet folds all allow statements for one action into a single interval
// set, recording every contributing statement for evidence.
func (cp compiledPolicy) allowsFor(action Action) (IntervalSet, []string) {
	statements := cp.allows[action]
	union := IntervalSet{}
	contributors := make([]string, 0, len(statements))
	for _, st := range statements {
		if st.resources.Empty() {
			continue
		}
		union = union.Union(st.resources)
		contributors = append(contributors, st.source)
	}
	return union, contributors
}

// denySet folds all deny statements for one action.
func (cp compiledPolicy) deniesFor(action Action) IntervalSet {
	union := IntervalSet{}
	for _, st := range cp.denies[action] {
		union = union.Union(st.resources)
	}
	return union
}

// firstDenyStatement returns the earliest (input-order) deny statement
// covering resource id v for the given action, plus whether one matched.
func (cp compiledPolicy) firstDenyStatement(action Action, resource int64) (compiledStatement, bool) {
	for _, st := range cp.denies[action] {
		if st.resources.Contains(resource) {
			return st, true
		}
	}
	return compiledStatement{}, false
}

var (
	errEmptyActionSet = errors.New("netsec: engine requires at least one action")
)
