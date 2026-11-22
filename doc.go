// Package changeaccept verifies multi-plane policy changes for a directed
// network topology shared by managed tenants.
//
// A policy is an ordered list of label predicates and actions. The first match
// decides and no match denies. On an edge u -> v, the label currently at u must
// pass u's egress policy and v's ingress policy. The label may then be rewritten
// by u's label graph before v's ingress check. Cycles are handled by exploring
// the finite product of endpoints, labels, initial labels, and whether at least
// one hop has been taken.
//
// Input describes old rules in the topology, new rules in atomic switch groups,
// and a candidate group order. Check verifies every prefix. Accept additionally
// searches dependency-respecting reorderings with minimum adjacent-swap
// distance from the candidate order. Verification is three-valued: Compliant,
// NonCompliant, or Undetermined when a budget is exhausted.
//
// Connection tracking keys include the exact concrete hop, policy generation,
// source/destination tenants, and a digest of the complete label history.
// CoverageCertificate independently re-checks the complete reachable state and
// transition graph for one tenant pair.
package changeaccept
