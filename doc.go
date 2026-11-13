// Package netsec recovers plaintext candidates that share one XOR keystream.
//
// Records may start at different non-negative keystream offsets. Their
// intervals can overlap partially, can be disjoint, or can be empty. A
// candidate combination is a solution exactly when every keystream position
// receives one common byte from every record covering that position.
//
// Recover performs a joint search. It does not conclude satisfiability from
// pairwise compatibility alone, and it only reports a keystream byte as
// determined when every possible completion agrees on that byte.
package netsec
