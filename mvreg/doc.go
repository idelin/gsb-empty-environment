// Package mvreg implements an op-free, state-based multi-value register.
//
// Every write or explicit resolve gets a unique (Origin, Nonce) pair and a
// version vector. Merge keeps all vector-concurrent items and removes items
// whose vectors are dominated. Wall-clock timestamps are deliberately absent:
// they cannot determine causality.
package mvreg
