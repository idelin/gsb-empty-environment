// Package admission implements release admission for a query gateway that
// publishes linear aggregates over secrets living in arbitrary composite
// modulus domains Z/(M_j Z).
//
// The central safety property is exact: for every collusion policy the gate
// computes the cardinality of the *joint* projection of the protected linear
// quantities onto the remaining feasible secret set.  It never substitutes a
// product of marginal shrink factors for that joint cardinality.
//
// Linear congruences are decomposed by CRT into prime-power moduli and the
// resulting integer presentation matrices are reduced to Smith normal form.
// Cardinality certificates contain the canonical invariant factors, so they
// are independent of row, column, or elimination order.
package admission
