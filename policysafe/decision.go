package policysafe

import "sort"

// Effect is the fail-closed authorization outcome.
type Effect string

const (
	// EffectDeny is the single externally visible failure outcome. Every
	// error, undefined result and conflict collapses to EffectDeny.
	EffectDeny Effect = "deny"
	// EffectAllow is emitted only when allow derives a matching tuple and
	// no deny tuple does.
	EffectAllow Effect = "allow"
)

// Reason explains why a decision was taken.
type Reason string

const (
	// ReasonAllow: an allow tuple matched and no deny tuple matched.
	ReasonAllow Reason = "allow_matched"
	// ReasonUndefinedDeny: neither allow nor deny produced a match, so the
	// missing `default allow := false` case is closed as a deny.
	ReasonUndefinedDeny Reason = "undefined_denied"
	// ReasonExplicitDeny: allow did not match but an explicit deny did.
	ReasonExplicitDeny Reason = "explicit_deny"
	// ReasonConflictDeny: allow and deny both matched; the conflict is
	// tightened to a deny.
	ReasonConflictDeny Reason = "conflict_denied"
)

// Decision is the fail-closed result of an authorization request.
type Decision struct {
	Effect      Effect
	Reason      Reason
	AllowTuples []Tuple
	DenyTuples  []Tuple
	Err         error
}

// Allowed reports whether the request may proceed. It is false for every
// conflict, undefined result and evaluation error.
func (d Decision) Allowed() bool { return d.Effect == EffectAllow && d.Err == nil }

// Authorize evaluates the policy for one request. requestFacts are the
// request-scoped inputs (e.g. subject(alice), action(write),
// tenant(t1)). allow/deny predicates may carry any arity; a zero-argument
// allow/deny rule set works directly.
//
// The decision never leaks an undefined result: when no allow tuple
// matches, or when allow and deny conflict, the effect is deny.
func (c *Compiled) Authorize(requestFacts []Atom) Decision {
	allowTuples, allowErr := c.relationTuples("allow", requestFacts)
	denyTuples, denyErr := c.relationTuples("deny", requestFacts)

	if allowErr != nil {
		return Decision{Effect: EffectDeny, Reason: ReasonUndefinedDeny, Err: allowErr}
	}
	if denyErr != nil {
		return Decision{Effect: EffectDeny, Reason: ReasonUndefinedDeny, Err: denyErr}
	}

	allowed := len(allowTuples) > 0
	denied := len(denyTuples) > 0

	decision := Decision{AllowTuples: allowTuples, DenyTuples: denyTuples}
	switch {
	case allowed && denied:
		decision.Effect = EffectDeny
		decision.Reason = ReasonConflictDeny
	case allowed:
		decision.Effect = EffectAllow
		decision.Reason = ReasonAllow
	case denied:
		decision.Effect = EffectDeny
		decision.Reason = ReasonExplicitDeny
	default:
		decision.Effect = EffectDeny
		decision.Reason = ReasonUndefinedDeny
	}
	return decision
}

func (c *Compiled) relationTuples(pred string, session []Atom) ([]Tuple, error) {
	db, err := c.evaluate(session)
	if err != nil {
		return nil, err
	}
	rel := db.relations[pred]
	if rel == nil {
		return nil, nil
	}
	keys := make([]string, 0, len(rel.tuples))
	for key := range rel.tuples {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	tuples := make([]Tuple, 0, len(keys))
	for _, key := range keys {
		tuples = append(tuples, append(Tuple{}, rel.tuples[key]...))
	}
	return tuples, nil
}
