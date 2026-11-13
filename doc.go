// Package netsec validates X.509 certificate paths with RFC 5280 name
// constraints.
//
// Paths are supplied leaf first and root last. Verification covers
// signatures, validity periods, CA basic constraints, path length limits, and
// name constraints.
//
// Name constraints are only honoured on CA certificates. A non-CA
// certificate that carries the name constraints extension is rejected with
// ErrNameConstraintsOnNonCA, even when the constraint would otherwise be
// harmless. This prevents an end-entity certificate from using a wider
// permitted subtree to relax constraints that are already in force.
//
// Constraints from every CA in the path are applied independently to all of
// its descendants. The effective permitted namespace is therefore the
// intersection of the CA constraints, while excluded subtrees accumulate. A
// subordinate CA can narrow its parent's constraints but can never widen
// them.
package netsec
