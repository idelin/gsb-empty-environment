// Package netsec provides deterministic trimming for privacy counting
// collection hosts.
//
// Each host may only contribute a bounded number of records per user per
// counting window. Out-of-order delivery and retransmissions make naive
// "first K" selection unstable, so records are instead ranked by a keyed
// digest (HMAC-SHA-256) and the K smallest digests are retained:
//
//   - a local cap K applies per (user, window, group);
//   - a separate global cap bounds the retained total across all groups of one
//     (user, window);
//   - duplicate EventIDs are idempotent when their content matches and are
//     rejected with ErrConflict on content mismatch;
//   - withdrawal tombstones retract events and survive shard merging;
//   - independently collected shards merge so that trimming the merged state
//     equals trimming the complete input collection.
//
// Selector is the stateful API used by hosts; TrimRecords is the stateless
// reference form; Merge combines shards.
package netsec
