// Package netsec reconstructs a degree-bounded secret polynomial from
// possibly corrupted Shamir-style shares over a caller-supplied prime field.
//
// Recover accepts n shares with distinct, nonzero x coordinates in F_p,
// a degree bound k, and an error budget t satisfying n >= k+2t. It finds a
// polynomial of degree less than k consistent with at least n-t of the
// received shares. If no such polynomial exists, Recover reports failure
// rather than interpolating an arbitrary k-subset.
//
// Shares returned as inconsistent are inconsistent with the reconstructed
// polynomial. That classification is a statement about the received data
// only; it is not an attribution of which participant supplied malicious
// input, nor proof that those shares were the original errors.
package netsec
