// Package netsec implements a secure materialized-view layer for an
// in-process Go database host.
//
// Materialized views are refreshed by a high-privilege owner but queried by
// low-privilege subjects. A naive shared cache would let a querier bypass the
// current row-level security (RLS) policy. The package therefore supports two
// refresh modes:
//
//   - OwnerSnapshot: only public, tenant-free aggregates may be stored.
//     Creation requires a static information-flow proof for every output
//     showing that it depends on no protected column, cannot throw and has no
//     side effect. Missing or invalid proofs are rejected.
//   - PerPolicyPartition: one cache partition per (policyDigest, subjectClass),
//     where subjectClass is the digest of the subject's transitive role
//     closure. Partitions are bound to the source version, policy epoch,
//     subject class and refresh-transaction LSN, and are revalidated before
//     every read.
//
// Incremental refresh retracts the contribution of rows that become
// invisible through a retractable monoid; views containing a non-retractable
// aggregate fall back to a full refresh. Concurrent refreshes publish a fully
// built entry in one atomic swap, so readers never observe partial state.
package netsec
