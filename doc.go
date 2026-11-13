// Package netsec implements a finite-program, control-flow-sensitive
// information-flow analysis for a Go host.
//
// Programs are CFGs of at most 48 nodes. Registers hold eight-bit values and
// sets of secret labels. The analysis combines explicit data labels with
// branch-condition PC labels, removes PC labels at the nearest common
// post-dominator of a branch, retains them conservatively for irreducible
// merges, audits declassification, and reports public writes whose value or
// PC label is non-empty.
//
// CrossCheck provides a concrete small-domain reference: it enumerates
// pairwise secret boolean assignments with identical public inputs and checks
// that no sensitive trace difference is missed by the static analysis.
package netsec
