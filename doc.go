// Package netsec provides finite Boolean interpolation for bounded
// model-checking style analyses.
//
// Given two CNFs a (the antecedent / path prefix) and b (the consequent /
// path suffix) and their shared variable set, Interpolate either returns a
// satisfying witness when a & b is satisfiable, or — when a & b is
// unsatisfiable — a formula over only the shared variables such that
//
//	a |= interpolant          and          b & interpolant is unsatisfiable.
//
// The interpolant summarizes the conditions that block the same family of
// unreachable vulnerability paths. The shared set is limited to
// MaxSharedVars (12) variables because both construction and verification
// enumerate all 2**n shared assignments.
//
// Verification is deliberately independent of construction:
// VerifyInterpolant re-enumerates every shared assignment, re-runs the
// bundled DPLL solver for each side, and checks the implication and
// contradiction properties plus the shared-variable language restriction.
// Satisfiable inputs return a witness via VerifyWitness instead of ever
// producing a purported safety proof.
package netsec
