// Package netsec implements a miniature speculative-execution machine and a
// bounded search for data-dependent probe observations that are visible only
// after a branch misprediction.
//
// A Program contains at most 64 instructions, one secret input slot, and up to
// two public input slots. Inputs may live in any of the eight registers or in
// one of the 256 memory bytes; all other bytes start at zero.
//
// Run executes a program architecturally, or with exactly one conditional jump
// choosing the opposite successor. Speculation is capped at eight executed
// instructions. Registers and memory are restored on rollback, while probe
// records are retained separately: architectural probes in RunResult.ArchProbes
// and speculative probes in RunResult.SpecProbes.
//
// Analyze exhaustively enumerates input assignments and non-degenerate
// misprediction occurrences, subject to a caller-supplied execution budget. A
// witness has the same public input, different secrets, one misprediction in
// each run, equal architectural probe records, and different speculative probe
// records. VerifyWitness replays both runs and enforces that definition.
package netsec
