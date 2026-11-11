// Package session implements generation-aware connection-tracking decisions
// for a gateway data plane.
//
// Policy updates are numbered in generations. Every session pins the policy
// generation that was in effect when its connection was established, so a
// later update only affects new connections unless the update explicitly
// revokes a session. Session tuples carry the flow direction and the startup
// generation: a reused transport tuple is therefore always re-evaluated and
// can never inherit a previous session's authorization, and a late end event
// targeting an old generation cannot remove a newer session on the same flow.
//
// The clock and the event scheduler are injected through NewEngine, which
// makes the ordering between policy updates, connection establishment and
// first-packet transmission fully deterministic and testable.
package session
