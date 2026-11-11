package netsec

import (
	"errors"
	"fmt"
)

// Verdict is the terminal decision produced by a policy evaluation.
type Verdict uint8

const (
	// VerdictNone means no accept/reject was reached and execution
	// falls through to the caller.
	VerdictNone Verdict = iota
	// VerdictAccept exports the route to the neighbor.
	VerdictAccept
	// VerdictReject suppresses the route.
	VerdictReject
)

func (v Verdict) String() string {
	switch v {
	case VerdictAccept:
		return "ACCEPT"
	case VerdictReject:
		return "REJECT"
	default:
		return "FALLTHROUGH"
	}
}

// Action is executed when a rule's condition matches. Non-terminal
// actions return VerdictNone so the next rule keeps executing.
type Action interface {
	run(rt *Route, call func(callee string) (Verdict, error)) (Verdict, error)
	validate(ps *PolicySet) error
}

// AddTags adds community tags to the route.
type AddTags struct{ Tags TagSet }

func (a AddTags) run(rt *Route, _ func(string) (Verdict, error)) (Verdict, error) {
	rt.Tags = rt.Tags.Union(a.Tags)
	return VerdictNone, nil
}

func (a AddTags) validate(_ *PolicySet) error { return validateTagSet(a.Tags, "add-tags") }

// DelTags removes community tags from the route.
type DelTags struct{ Tags TagSet }

func (a DelTags) run(rt *Route, _ func(string) (Verdict, error)) (Verdict, error) {
	for _, t := range a.Tags.List() {
		rt.Tags = rt.Tags.Without(t)
	}
	return VerdictNone, nil
}

func (a DelTags) validate(_ *PolicySet) error { return validateTagSet(a.Tags, "del-tags") }

// Accept terminates evaluation with VerdictAccept.
type Accept struct{}

func (Accept) run(*Route, func(string) (Verdict, error)) (Verdict, error) {
	return VerdictAccept, nil
}
func (Accept) validate(*PolicySet) error { return nil }

// Reject terminates evaluation with VerdictReject.
type Reject struct{}

func (Reject) run(*Route, func(string) (Verdict, error)) (Verdict, error) {
	return VerdictReject, nil
}
func (Reject) validate(*PolicySet) error { return nil }

// CallPolicy invokes a named sub-policy without recursion. If the
// sub-policy reaches a terminal verdict it propagates immediately;
// otherwise the caller continues with its next rule.
type CallPolicy struct{ Name string }

func (c CallPolicy) run(_ *Route, call func(string) (Verdict, error)) (Verdict, error) {
	return call(c.Name)
}

func (c CallPolicy) validate(ps *PolicySet) error {
	if c.Name == "" {
		return errors.New("call-policy requires a policy name")
	}
	if _, ok := ps.Policies[c.Name]; !ok {
		return fmt.Errorf("call-policy references unknown policy %q", c.Name)
	}
	return nil
}

// Rule is one conditional branch. A nil When condition always matches.
type Rule struct {
	Name string
	When Condition
	Do   Action
}

// Policy is an ordered, non-recursive rule list.
type Policy struct {
	Name  string
	Rules []Rule
}

// PolicySet is the validated collection of mutually referencing policies.
type PolicySet struct {
	Policies map[string]*Policy
}

// StepKind labels a single entry in the execution trace.
type StepKind uint8

const (
	StepEnterPolicy StepKind = iota
	StepRuleMatch
	StepRuleSkip
	StepLeavePolicy
	StepEarlySkip
)

// Step records one evaluation event. Nested sub-policy calls appear in
// the parent rule step's Children.
type Step struct {
	Kind       StepKind
	Policy     string
	Rule       string
	Matched    bool
	Verdict    Verdict
	BeforeTags TagSet
	AfterTags  TagSet
	Children   []*Step
}

// Trace is the full per-rule execution history of one Eval run.
type Trace struct {
	Steps []*Step
	Final Verdict
}

// Eval runs the named entry policy against a copy of route and returns
// the terminal verdict, the resulting route and the full trace.
func (ps *PolicySet) Eval(entry string, route Route) (Verdict, Route, *Trace, error) {
	if err := ps.Validate(); err != nil {
		return VerdictNone, route, nil, err
	}
	if _, ok := ps.Policies[entry]; !ok {
		return VerdictNone, route, nil, fmt.Errorf("unknown entry policy %q", entry)
	}
	trace := &Trace{}
	rt := route
	v, err := ps.evalPolicy(entry, &rt, nil, &trace.Steps)
	if err != nil {
		return VerdictNone, rt, trace, err
	}
	trace.Final = v
	return v, rt, trace, nil
}

func (ps *PolicySet) evalPolicy(name string, rt *Route, stack []string, list *[]*Step) (Verdict, error) {
	for _, parent := range stack {
		if parent == name {
			return VerdictNone, fmt.Errorf("recursive policy call detected: %q already on stack %v", name, stack)
		}
	}
	policy := ps.Policies[name]
	enter := &Step{Kind: StepEnterPolicy, Policy: name, BeforeTags: rt.Tags, AfterTags: rt.Tags}
	*list = append(*list, enter)

	verdict := VerdictNone
	for i := range policy.Rules {
		rule := &policy.Rules[i]
		step := &Step{
			Policy:     name,
			Rule:       rule.Name,
			BeforeTags: rt.Tags,
			AfterTags:  rt.Tags,
		}
		if verdict != VerdictNone {
			// An earlier rule already returned; record the rule as
			// never evaluated so the trace covers every rule.
			step.Kind = StepEarlySkip
			step.Verdict = VerdictNone
			enter.Children = append(enter.Children, step)
			continue
		}
		if rule.When == nil || rule.When.Match(*rt) {
			step.Kind = StepRuleMatch
			step.Matched = true
			call := func(callee string) (Verdict, error) {
				return ps.evalPolicy(callee, rt, append(stack, name), &step.Children)
			}
			v, err := rule.Do.run(rt, call)
			if err != nil {
				enter.Children = append(enter.Children, step)
				return VerdictNone, err
			}
			step.Verdict = v
			verdict = v
		} else {
			step.Kind = StepRuleSkip
			step.Verdict = VerdictNone
		}
		step.AfterTags = rt.Tags
		enter.Children = append(enter.Children, step)
	}

	leave := &Step{Kind: StepLeavePolicy, Policy: name, Verdict: verdict, BeforeTags: rt.Tags, AfterTags: rt.Tags}
	*list = append(*list, leave)
	return verdict, nil
}

func validateTagSet(tags TagSet, where string) error {
	if tags.Empty() {
		return fmt.Errorf("%s requires at least one tag", where)
	}
	for _, t := range tags.List() {
		if !validTag(t) {
			return fmt.Errorf("%s references tag %d outside the %d-tag universe", where, t, MaxTags)
		}
	}
	return nil
}
