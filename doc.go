// Package eventlog implements a compressible event log for a deterministic
// state machine, together with a replay validator.
//
// A raw log is a sequence of events (add/mul/set/observe). Compression
// produces a CompressedLog that keeps:
//
//   - the initial state summary,
//   - the final state summary,
//   - every public observation point (tag + observed value + position),
//   - per-segment compressed events plus the replayable summaries of the
//     original events they stand for.
//
// Only runs of the same commutative operation are merged. Non-commutative
// operations (set, and any change of operation kind) are never merged by
// type counts alone, because their relative order affects the result.
//
// Validate replays both the original event summaries and the compressed
// events from the initial summary and rejects the log unless both reproduce
// every observed value and the final summary.
package eventlog
