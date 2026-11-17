// Package netsec verifies rewrites of small signed-integer SSA graphs.
//
// A graph has at most twelve unsigned input bytes and acyclic control flow
// made of branches, returns, and errors. Arithmetic nodes perform wrapping
// 16- or 32-bit operations (other widths are representable as well); explicit
// Overflow nodes test whether the mathematical result fits the original
// signed range. This makes it possible to compare an old narrow graph that
// branches to an Error(source) with a widened graph that must report the same
// source and otherwise return the same value.
//
// Verify enumerates control-flow paths, turns branch conditions and result
// comparisons into bit-vector CNF, and solves them with a bounded DPLL
// engine. StatusVerified is emitted only after every path pair has been shown
// impossible, StatusCounterexample carries a model replayed by the independent
// Interpret function, and StatusUnknown means the budget was exhausted.
package netsec
