// Package relexec provides relational execution of two loop-free programs for
// patch review.
//
// The target use case is confirming that a newly added array bounds check does
// not change observable behavior on inputs that the original program handled
// safely. Both programs are expressed in the same small instruction set
// ([Instr]) operating on eight 8-bit registers, a byte array of at most 16
// bytes, 8-bit integer arithmetic and conditional forward jumps.
//
// The verifier ([Verify]) enumerates the common input space in lexicographic
// order, executes both programs on each input and compares the return value and
// the ordered array write trace. Inputs on which the old program traps with an
// out-of-bounds access are exempt: the patched program may reject them early.
// It returns the lexicographically smallest counterexample, a full
// equivalence conclusion, or [VerdictUnknown] when the step budget is
// exhausted before the input space is covered.
package relexec
