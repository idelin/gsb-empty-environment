// Package netsec evaluates the conditions of a verified (signature-checked)
// SAML-style claim tree for a Go authentication host.
//
// # Semantics
//
// The evaluator never merges separate AudienceRestriction conditions into a
// single union, which would silently widen access. Instead:
//
//   - Audiences inside one AudienceRestriction are combined with OR: the
//     entry relying party only has to appear in that one restriction.
//   - Distinct conditions on a node, including separate audience
//     restrictions, are combined with AND via All / the node condition list.
//   - All and Any nest arbitrarily to model nested conditions.
//   - Each child node is one proxy forwarding step; every step consumes one
//     unit of the strictest applicable ProxyRestriction count, so Count=0
//     forbids any child node.
//   - ValidityWindow is the half-open interval [NotBefore, NotOnOrAfter).
//     A single clock-skew allowance is captured by NewEvaluator at the entry
//     of the host and reused unchanged for every node; skew is never applied
//     again per hop.
//   - Any condition type not represented here (UnknownCondition or a nil
//     value) always fails closed, even when it appears inside an Any group.
//
// Evaluate returns a Report whose Evidences trace every evaluated condition
// and forwarding decision with its node path and satisfied/denied outcome,
// giving the caller node-level evidence for both acceptance and rejection.
package netsec
