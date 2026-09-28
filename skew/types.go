// Package skew derives feasible clock-offset intervals per node from
// causal send/receive constraints observed across the nodes.
//
// Model. Each node n has a local clock. x_n denotes the clock offset of
// node n relative to a chosen reference node R:
//
//	true_time(event) = observed_local_time(event) + x_n.
//
// For the reference node x_R is fixed to 0. A causal edge e: u@t1 -> v@t2
// (a request/response send at u at local time t1, received at v at t2)
// requires the effect not to happen before its cause:
//
//	t1 + x_u <= t2 + x_v   <=>   x_v - x_u <= t1 - t2 = -delta,
//	                                              delta = t2 - t1.
//
// The observed delta is signed and is never clamped to zero: a negative
// delta is a legitimate (possibly very tight) constraint, and a closed
// inconsistent set of constraints yields a negative cycle, i.e. a
// contradiction reported as a conflict chain.
package skew

// Event is a telemetry event observed on a node using the node's local
// clock. TS is the observed local timestamp in nanoseconds since epoch.
type Event struct {
	ID   string
	Node string
	TS   int64
}

// Edge is one causal constraint From -> To: the To event happened at or
// after the From event in true time. Request send -> server receive and
// response send -> client receive are both expressed this way.
type Edge struct {
	From string
	To   string
}

// Graph holds the local telemetry and the causal edges between events.
// Edges refer to events by ID.
type Graph struct {
	Events []Event
	Edges  []Edge

	// nodes discovered while building, in first-seen order
	nodes  []string
	nodeAt map[string]int
	event  map[string]Event
}
