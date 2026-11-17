// Package netsec provides short-circuit policy evaluation with an auditable,
// replayable execution trace.
//
// A policy is a boolean expression over named rules. Rules are checks supplied
// by the host; each check receives a context and returns a verdict together
// with a digest of the evidence that produced it. Evaluation follows ordinary
// short-circuit semantics:
//
//   - AND stops at the first denying rule.
//   - OR stops at the first allowing rule.
//   - NOT evaluates its only child and inverts the result.
//
// Every check that is actually invoked is recorded before evaluation
// continues. Composite nodes are not recorded; they are reconstructed by the
// verifier from the immutable policy. A trace step binds the exact node
// occurrence (not merely a rule name), its sequence number, its verdict, its
// evidence digest, and the rule version. Binding the node occurrence is
// important for expression DAGs: a shared rule reached at two different
// positions has two distinct sites, and evidence from one site never counts
// as evidence for the other. Shared nodes are deliberately not memoized: every
// reached site is a separate check invocation with its own evidence.
//
// Verify independently replays a trace against the policy without calling the
// executor. It rejects missing steps, extra steps, reordered steps, values that
// are inconsistent with short-circuit semantics, and steps for branches that
// short-circuit semantics did not reach. The seal detects edits to recorded
// fields. Aborted traces (cancellation, check errors, or panics) are verified
// as aborted traces; VerifyAllow additionally requires a completed allow.
// A cancellation that occurs before a check is invoked is represented by a
// StepCanceled stop marker with no evidence and Invoked() == false.
//
// A trace seal is a hash chain over all trace fields. It detects accidental or
// post-hoc edits, but it is not a signature: an attacker who can rewrite the
// whole trace can also recompute the seal. VerifyLive closes that gap for
// deterministic checks by re-executing the policy and comparing every step
// with the recorded trace. Hosts that need non-replayable evidence should
// additionally authenticate the sealed trace with a key held outside the rule
// process.
//
// A Rule.Version must change whenever the rule implementation, its inputs, or
// its evidence format changes. The program digest binds the policy version,
// the expression, and all rule versions, but not executable code.
package netsec
