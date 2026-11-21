// Package admission implements deterministic round-admission accounting for a
// multi-party commitment/reveal audit that assigns rounds to independent
// reviewers.
//
// Participants may publish or abstain after seeing a public prefix. The set of
// rounds that eventually succeed is therefore a selectively missing sample and
// must never be treated as an unconditional random sample. This package
// quantifies, with exact rational arithmetic, two quantities:
//
//   - the worst-case deviation, per audit category, between the realized
//     conditional allocation (normalized over completed rounds) and the ideal
//     allocation, over every admission rule the public protocol permits; and
//   - a tight upper bound on the probability that a round cannot be completed
//     within the publicly declared retry limit.
//
// Inputs are restricted to signed-commitment identities, public message types,
// rational probability tables, each participant's public partition, and the
// rule-allowed abstention positions. No private key material is accepted: the
// API has no field capable of carrying one.
//
// Determinism rules enforced by Model.Compile:
//
//   - A subject facing the same public observation must make the same choice.
//     Decisions are keyed by (subject, observed public prefix); distinct
//     secret states sharing one observation collapse onto one decision
//     variable, so a rule cannot branch on hidden state.
//   - Subjects in one failure domain share one decision variable per public
//     observation and the host supplies its explicit condition in
//     Model.HostConditions.
//   - Retries follow the public rule only: at most MaxRetries (<= 2) retries,
//     re-entering at declared retry-entry nodes, and already drawn fixed
//     random quantities are named values that are never re-drawn.
//
// The round graph is a DAG (shared successors are allowed). The public
// decision points are at most 14, so every deterministic admission rule is
// enumerated completely; there is no sampling and no heuristic bound.
//
// The package emits exact *big.Rat bounds together with a Certificate that an
// independent verifier can re-check with Certificate.Verify. It never outputs
// manipulation instructions: certificates contain bounds and digests only;
// concrete extremal rules are returned in Result for local auditing, never
// serialized into the certificate.
package admission
