// Package netsec implements a bounded, non-executing model of stack writes
// for firmware exploitability triage.
//
// The model never touches a real process. A Problem describes a 64-byte
// stack, an input slot holding at most three attacker-controlled bytes, a
// four-byte saved return address, up to eight copy operations, an optional
// four-byte canary, and the set of return addresses that count as a
// successful control-flow transfer.
//
// Replay is the naive, deterministic replayer. Solve enumerates the allowed
// input bytes in lexicographic order and returns the first input that
// transfers control to an allowed target. If the search is cut short by a
// caller-supplied replay budget, Solve reports StatusBudgetUnknown instead of
// claiming that no exploit exists.
package netsec
