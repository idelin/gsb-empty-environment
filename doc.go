// Package netsec provides exact verification of sensitive-attribute
// distributions in a privacy publishing host.
//
// The sensitive attribute takes values in an ordered finite domain (Domain).
// For every publishing group, the verifier compares the group's empirical
// distribution with the population distribution using the one-dimensional
// earth-mover (Wasserstein-1) distance, where the ground distance between
// adjacent domain positions is one. All probabilities, edge flows, distances
// and thresholds are exact rational numbers (math/big); the threshold decision
// therefore contains no floating-point rounding.
//
// Adding a record updates both the group and the population counts, and
// withdrawing a record removes it from both in the same operation. Audit
// reports name every group whose distance strictly exceeds the threshold and
// include the canonical transport edge flows as auditable evidence. Groups
// without samples are reported separately because no distribution, and hence
// no distance, is defined for them.
package netsec
