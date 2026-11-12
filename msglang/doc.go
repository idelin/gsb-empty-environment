// Package msglang models protocol message languages as finite labeled-tree
// automata and computes the intersection of the languages accepted by two
// handshake phases.
//
// The motivating scenario is a label/shape ambiguity: two handshake phases
// share the same wire encoding shape, so a receiver in one phase may interpret
// an acknowledgment produced for the other phase as an authorization. Each
// phase is described by a bottom-up non-deterministic finite tree automaton
// (NFTA) over the same ranked alphabet. Leaves are typed: principal
// identities, nonces, and raw byte strings.
//
// The package exposes:
//
//   - Automaton: a finite labeled-tree automaton with typed leaf rules.
//   - Intersect / MinimalIntersection: product construction and the smallest
//     tree accepted by both phases, with one parsing derivation per phase.
//   - BoundedIntersection: finite-height / finite-budget tree enumeration with
//     a three-valued verdict (non-empty, empty, unknown) so that node-budget
//     exhaustion never turns into a false "empty" answer.
package msglang
