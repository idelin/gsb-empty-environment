package netsec

import "sort"

// Decision is the resource policy outcome.
type Decision string

const (
	Deny  Decision = "deny"
	Allow Decision = "allow"
)

// DecisionDeny and DecisionAllow are compatibility aliases.
const (
	DecisionDeny  = Deny
	DecisionAllow = Allow
)

// LabelView is an immutable snapshot of the labels shown to a resource
// policy callback.  There are no mutating methods and the wrapped map is not
// exposed.  NewLabelView copies its argument, so later mutation of the caller's
// map cannot change the view.
type LabelView struct {
	labels map[string]string
}

// ImmutableLabels, FrozenLabels, and LabelsView are compatibility aliases for
// LabelView.
type ImmutableLabels = LabelView
type FrozenLabels = LabelView
type LabelsView = LabelView

func NewLabelView(labels map[string]string) LabelView {
	cp := make(map[string]string, len(labels))
	for k, v := range labels {
		cp[k] = v
	}
	return LabelView{labels: cp}
}

func (v LabelView) Get(key string) (string, bool) {
	value, ok := v.labels[key]
	return value, ok
}

func (v LabelView) Has(key string) bool {
	_, ok := v.labels[key]
	return ok
}

func (v LabelView) Len() int { return len(v.labels) }

func (v LabelView) Keys() []string {
	keys := make([]string, 0, len(v.labels))
	for k := range v.labels {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// Range calls fn for each label in canonical key order.  Returning false
// stops the iteration.
func (v LabelView) Range(fn func(key, value string) bool) {
	for _, k := range v.Keys() {
		if !fn(k, v.labels[k]) {
			return
		}
	}
}

// Map returns a defensive copy.  Mutating it cannot affect this view or any
// chain state.
func (v LabelView) Map() map[string]string {
	cp := make(map[string]string, len(v.labels))
	for k, val := range v.labels {
		cp[k] = val
	}
	return cp
}

// Clone is an alias for Map.
func (v LabelView) Clone() map[string]string { return v.Map() }

// Policy is the policy callback contract.  A policy sees only a frozen view.
type Policy interface {
	Allow(LabelView) bool
}

// PolicyFunc is the convenient callback form.  It receives a fresh copy of
// the final labels; mutating that copy cannot alter the session, result, or
// any later evaluation.
type PolicyFunc func(map[string]string) bool

func (p PolicyFunc) Allow(v LabelView) bool { return p(v.Clone()) }

// ViewPolicyFunc is for callers that want the read-only view directly.
type ViewPolicyFunc func(LabelView) bool

func (p ViewPolicyFunc) Allow(v LabelView) bool { return p(v) }

// Decide evaluates p against labels and returns deny or allow.  A nil policy
// denies by default.  p may be a Policy, PolicyFunc, ViewPolicyFunc,
// func(map[string]string) bool, or func(LabelView) bool.
func Decide(p any, labels map[string]string) Decision {
	pol := asPolicy(p)
	if pol == nil {
		return Deny
	}
	if pol.Allow(NewLabelView(labels)) {
		return Allow
	}
	return Deny
}

// EvaluatePolicy is an alias for Decide.
func EvaluatePolicy(p any, labels map[string]string) Decision {
	return Decide(p, labels)
}

// AllowLabels is a tiny helper for table-driven tests.
func AllowLabels(p any, labels map[string]string) bool {
	return Decide(p, labels) == Allow
}

func asPolicy(p any) Policy {
	switch v := p.(type) {
	case nil:
		return nil
	case PolicyFunc:
		return v
	case ViewPolicyFunc:
		return v
	case func(map[string]string) bool:
		return PolicyFunc(v)
	case func(LabelView) bool:
		return ViewPolicyFunc(v)
	case func(map[string]string) Decision:
		return PolicyFunc(func(labels map[string]string) bool { return v(labels) == Allow })
	case func(map[string]string) (bool, error):
		return PolicyFunc(func(labels map[string]string) bool {
			ok, err := v(labels)
			return err == nil && ok
		})
	case Policy:
		return v
	default:
		return nil
	}
}
