// Package policysafe implements a safety compiler and static
// privilege-escalation verifier for a declarative, Datalog-style
// authorization language (a Rego-like Datalog subset).
//
// The package takes a rule set composed of ground fact tables, allow/deny
// rule bodies, local negation (not) and stratified dependencies, and
// statically enforces:
//
//   - Safety: every named variable must be bound by a positive body atom.
//     Unsafe (unbound) variables and variables that leak out of negated
//     literals are rejected at compile time.
//   - Stratification: recursive cycles that traverse a negated edge are
//     rejected (non-stratified negation).
//   - Fail-closed evaluation: an undefined allow decision and an
//     allow/deny conflict are both tightened to a deny.
//   - Bounded counterexample search: host-declared "must never be allowed"
//     scenarios are checked by searching for a cardinality-minimal set of
//     input facts that makes the forbidden allow goal derivable.
package policysafe
