// Package replayhandoff implements offline OSCORE replay-state handoff for
// 40-bit sequence numbers.
//
// The package stores immutable interval-set snapshots, chains signed node-log
// batches, authenticates per-range proofs, applies lease barriers, computes
// minimum-cost supplementary proof pages for arbitrary half-open query
// segments, and emits independently verifiable coverage certificates. It never
// performs network I/O and never enumerates the sequence space.
package replayhandoff
