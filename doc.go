// Package bgpsim simulates internal BGP route advertisement evolution in an
// autonomous system that deploys route reflectors, with particular support
// for MED-induced persistent oscillation.
//
// Inputs describe the node set (AS border routers, route reflectors and
// clients), an IGP shortest-path cost matrix, the external routes received
// by each border router (LocalPref, ASPathLen, Origin, adjacent AS number,
// MED and RouterID) and reflector/cluster membership with cluster IDs.
//
// Selection follows the standard lexicographic order:
//
//	LocalPref desc, ASPathLen asc, Origin asc,
//	MED asc only between routes with the same adjacent AS
//	  (or across all routes when Options.AlwaysCompareMED is set),
//	IGP cost to the egress asc, carried RouterID asc.
//
// The engine advances in synchronous rounds. Reflectors append their cluster
// ID when forwarding learned routes and discard advertisements that already
// contain their own cluster ID. Each node advertises only its current best
// route to every derived peer. Simulate returns the unique converged best
// routes, or the first repeated control state together with its minimum
// oscillation period. Candidates eliminated after the MED step was skipped
// because of neighboring-AS incomparability are explicitly marked.
package bgpsim
