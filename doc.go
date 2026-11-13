// Package netsec implements a three-party additive-sharing Beaver-triple
// multiplication host with signed commit-reveal sacrifice checks.
//
// All protocol fields are elements of F_p for a caller-supplied prime
// p <= 65521. Triple lifecycle state is durably persisted so a checked triple
// can be consumed at most once, and interrupted operations are recovered
// conservatively.
package netsec
