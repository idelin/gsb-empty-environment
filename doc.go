// Package hvsched diagnoses integer weights for hierarchical virtual-finish
// schedulers.
//
// The package keeps all virtual-time arithmetic exact with math/big.Rat.
// The solver uses rational branch expressions and interval bisection rather
// than substituting every candidate integer weight.
package hvsched
