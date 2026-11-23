// Package migrationwitness produces importable, independently re-checkable
// migration witnesses for a content-addressed repository host that is moving
// from a collision-prone legacy digest to a new, collision-resistant identity.
//
// The legacy manifest is a signed directed reference graph. A graph node is
// either a raw blob or an ordered concatenation of node references. A legacy
// reference carries only an old digest and the expected logical length; old
// digests may collide, so one legacy name may denote more than one new object.
// Alias statements may redirect one legacy name through a chain to the batch
// of objects that actually has to be migrated.
//
// The host feeds signed evidence in batches: blob and concat node statements,
// alias statements, partial disambiguation receipts that pin the candidate on
// a particular manifest edge, a closure receipt that seals the candidate
// table, and withdrawals that retract a previously supplied receipt.
//
// For a requested root, Evaluate decides whether every resolution compatible
// with the current evidence converges on a single complete new identity. A
// new identity binds the node kind, child order, full length and the new
// digests of the children. Witnesses are expressed as a shared subgraph and
// are always pinned to an evidence version, so certificates remain re-checkable
// after later withdrawals while re-evaluation uses the newest evidence.
package migrationwitness
