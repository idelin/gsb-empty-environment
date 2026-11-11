// Package netsec implements a dual-execution checker for secret-dependent
// exception paths.
//
// The target threat is a cryptographic component whose return value is fixed
// but whose control flow leaks a secret through whether it raises an
// exception (or which host calls it performs). To decide whether such a leak
// exists the package models the component as a small, fully bounded abstract
// machine and executes it twice (or many times) under identical public inputs
// while enumerating secret inputs.
//
// # Bounds
//
// A Program contains at most MaxInstr (12) instructions acting on bounded
// unsigned variables marked Public or Secret. The combined width of all
// secret variables is at most MaxInputBytes (6) bytes, so the secret domain
// never exceeds 2^48. Branches are strictly forward, which makes every
// validated program terminate without an external step counter.
//
// Instructions can raise explicit, typed exceptions (ExcCode): overflow,
// underflow, division by zero, failed assertion, host fault and running off
// the program without a return. OpHost delegates to a Host supplied by the Go
// verification host and records every call, including its argument and result.
//
// # Differential verification
//
// Verify fixes the public variables and enumerates secret assignments. Each
// run produces an Observation combining the return value, the typed exception
// together with its faulting pc, and the ordered host-call sequence. Any
// divergence between two runs yields Verdict LeakFound and a
// Counterexample holding the two complete, replayable state sequences
// (Trace). Replay rebuilds either trace from its recorded host calls without
// contacting the real host.
//
// Search is bounded by a run budget. When the budget is exhausted before the
// whole secret domain is covered, Verify returns Verdict Unknown. Only an
// exhaustive, divergence-free exploration returns SecretIndependent.
package netsec
