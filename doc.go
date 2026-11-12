// Package taskpkg is a compiler for WAF rules whose leaf checks produce
// field-scoped sanitization marks.
//

// A rule is a tree of boolean expressions and control statements. Leaf
// checks return true/false and may emit one mark (trusted or untrusted); they
// may also raise an exception. Dangerous forward nodes require a set of marks
// to already exist, and a trusted requirement can only be satisfied by a
// trusted mark. Short-circuit, exception, rejection and forwarding are all
// represented by explicit control edges in the lowered CFG.
//
// Compile lowers a validated Rule to a Program. The reference interpreter
// (Interpret) executes every leaf occurrence; the compiled Program shares
// pure (side-effect-free, non-raising) checks through a memo table but never
// shares an occurrence that emits a mark or raises. Both executors run the
// same CFG kernel, so their observable traces agree: edge sequence, actual
// leaf order, marks and the forward/deny/reject decision.
//
// Analysis support:
//   - Program.Enumerate gives per-forward mark guarantees over all 2^n
//     assignments (n <= MaxInputs), including always-present marks and the
//     observed deny/forward mark sets.
//   - Program.ShortestMissingWitness returns the shortest concrete assignment
//     that reaches a forward node while a required mark is absent.
package taskpkg
