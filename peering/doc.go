// Package peering implements a Ceph-style Placement Group (PG) peering
// control-plane state machine.
//
// The package reproduces the four safety-critical pieces of Ceph PG peering:
//
//   - past_intervals / maybe_went_rw authoritative history reconstruction and
//     the "every writable interval must have a reachable witness" gate
//     (see BuildPastIntervals and FindUnwitnessedInterval);
//   - authoritative log selection that never blindly trusts the highest
//     last_update, plus divergent-log detection, divergent_priors recording
//     and truncation/rollback of isolated old primaries (see SelectAuthLog and
//     MergeLogs);
//   - up vs acting reconciliation: pg_temp construction for backfill targets so
//     that complete replicas can serve active+degraded early, together with
//     per-OSD missing sets and global missing_locations (see PlanRecovery);
//   - unfound object detection and the min_size write gate that forbids
//     activating (or writing) when too few recoverable replicas survive
//     (see RecoveryPlan and PG.Peer).
//
// The top-level entry point is PG.Peer, which runs the full peering state
// machine and returns a Result describing the terminal PG state, the chosen
// authoritative log, divergent rollbacks, the recovery plan and the precise
// blocking reason when the PG must stay Incomplete.
package peering
