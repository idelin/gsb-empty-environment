// Package netsec resolves crate audit chains for a deployment gate.
//
// The gate answers one question: may a specific crate version be used
// under a given criteria such as safe-to-deploy or safe-to-run?
//
// Evidence comes in three shapes:
//
//   - FullAudit anchors one version as fully reviewed for a criteria.
//   - DeltaAudit records a reviewed jump From -> To for a criteria, so
//     trust can be inherited along a chain without re-reviewing every
//     release.
//   - Violation marks every version inside a semver range as untrusted.
//     Violations always win: an otherwise reachable version is rejected
//     as soon as it falls in a matching violation range.
//
// A version passes a criteria exactly when some full audit anchors a
// chain whose delta edges all carry a criteria that implies the queried
// one, and the chain reaches the target. A delta chain starting at an
// unaudited version never passes (broken chain), and cycles are handled
// with a visited set so resolution always terminates.
//
// Criteria implication is transitive and configurable. NewStore seeds
// safe-to-deploy -> safe-to-run, so passing the stronger deploy review
// proves the run review; the reverse direction is deliberately absent.
//
// Versions follow strict semver 2.0 precedence. Prereleases are ordered
// per spec (e.g. 1.0.0-alpha < 1.0.0-alpha.1 < 1.0.0-beta
// < 1.0.0), and build metadata after "+" never affects precedence or
// graph identity, so "1.0.0+x" and "1.0.0+y" are the same node.
//
// A small end-to-end example:
//
//	store := netsec.NewStore()
//	_ = store.AddRecords(
//	    netsec.Record{Full: &netsec.FullAudit{
//	        Crate: "serde", Version: "1.0.0", Criteria: netsec.SafeToDeploy,
//	    }},
//	    netsec.Record{Delta: &netsec.DeltaAudit{
//	        Crate: "serde", From: "1.0.0", To: "1.1.0", Criteria: netsec.SafeToDeploy,
//	    }},
//	    netsec.Record{Viol: &netsec.Violation{
//	        Crate: "serde", Range: ">=2.0.0",
//	    }},
//	)
//	verdict, err := store.Evaluate("serde", "1.1.0", netsec.SafeToDeploy)
//	// verdict.Allowed == true, verdict.Reason == netsec.ReasonReachable
package netsec
