// Package netsec implements role-chain session-label propagation, provenance,
// immutable policy evaluation, and differential search for label-escalation
// witnesses.
//
// ApplyHop is the secure reference transition.  AuditedHop implements a
// configurable legacy transition whose individual defects are selected with
// AuditConfig.  FindEscalation performs a breadth-first search of at most four
// hops and returns the shortest path on which the audited implementation
// accepts a transition that the reference model rejects while the resource
// policy changes from deny to allow.
package netsec
