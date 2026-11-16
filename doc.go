// Package netsec implements secret byte-string comparison for an
// authentication host, plus an interpreted constant-trace checker.
//
// The model is observational, not wall-clock: a tiny custom instruction set
// (addressed reads, XOR, OR, increment, public-bound loops, branch-by-value)
// is interpreted, and every read address and branch outcome is recorded as
// the observable trace. Secrets and candidates are fixed at 32 bytes; length
// validation is public and completes before the secret region is entered.
//
// Equal compares 32-byte values via the branch-free CompareProgram, whose
// trace is identical for every input. Check runs an arbitrary program of at
// most MaxProgramLen instructions over a caller-supplied finite set of
// secrets and candidates, compares traces pairwise, and reports the shortest
// divergence with a full input witness. A missing or exhausted step budget
// yields VerdictUnknown, never a pass. A VerdictPass covers only the checked
// finite set under the given budget; it does not prove constant-time
// behaviour for arbitrary inputs or arbitrary machine code.
package netsec
