// Package volrecover implements recovery and crash-safe upgrade planning for
// an encrypted volume whose block image may carry two generations of metadata
// at the same time.
//
// # Model
//
// A Header binds a generation (Epoch), a parent-header digest, key-slot
// ranges, encrypted segment ranges, and a signed slot-to-segment authorization
// table. Each key slot names independent write units through exact versions
// and content commitments. Slot ranges may overlap geometrically, but a unit
// durable at a newer version can never be interpreted through an older slot
// binding: Analyze reports ReasonVersionRollback in that case.
//
// Analyze verifies signatures under a selected TrustRoot, checks every unit
// reference against the read-only image, walks parent digests to genesis, and
// rejects both same-generation forks (ReasonFork) and headers that do not form
// one linear generation chain (ReasonMixedGeneration). The reliable
// monotonic revocation register is read from the image itself and outranks any
// old header, so an irrevocable revocation cannot be restored.
//
// # Planning
//
// BuildPlan takes a target header, a spare-unit allow list, requested
// revocations, and the 16-action budget. It emits the minimum number of Flush
// barriers needed so that every reachable crash image retains at least one
// complete trusted authorization, using either the old or the new header.
// Within one flush epoch any subset of writes may persist; across flushes the
// durable prefix is ordered. Revocation writes count as actions and are
// separated from spare/header writes by a barrier, and the root pointer can
// only move after every spare unit and the replacement header are durable.
//
// Concrete invariant violations return *UnsafePlan with a verifiable Reason.
// Action-budget or crash-enumeration-budget exhaustion returns *UnknownPlan,
// which means safety could not be established with the available state budget
// rather than proving the transition unsafe.
//
// ExhaustCrashes materializes every persistence subset on MemDevice-backed
// in-memory images and needs no real storage device.
package volrecover
