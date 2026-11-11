// Package netsec provides post-handshake identity upgrade isolation for a
// long-lived protocol connection.
//
// A session starts at LevelNone. The protocol host may at any time request a
// stronger client identity with Gate.BeginUpgrade; each request is bound to a
// freshly generated unique context id and its own context.Context, so
// out-of-order or replayed authentication callbacks can never be confused
// between concurrent upgrades.
//
// Inbound application messages are offered with Gate.Submit. A message whose
// required level is already granted is delivered to the protected Sink
// immediately, including while verification of a stronger upgrade is still in
// progress ("old level keeps flowing"). A message requiring a stronger level is
// queued inside the gate and never touches the protected callback. It is
// released, together with all other satisfied messages and in arrival order,
// only after Gate.CompleteUpgrade receives a credential bound to the matching
// context id and the Verifier accepts it. A context-mismatched credential, a
// verification failure, an explicit Gate.CancelUpgrade or a Gate.HalfClose
// rejects the messages that no live upgrade could satisfy instead.
package netsec
