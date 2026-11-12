// Package netsec is a streaming low-rate port-scan detector for probe hosts.
//
// # Model
//
// Connection probes emit start, failure and success events. Every event
// carries the probe generation, the per-generation connection instance
// number, the fixed connection start time s, and both the source and target
// address/port endpoints. A connection instance is the pair
// (Generation, Instance): the same instance number may be reused freely in a
// later generation, but within one generation any event whose start time or
// endpoints differ from the first accepted event is rejected as a metadata
// conflict.
//
// Retransmissions and out-of-order delivery are resolved per instance:
//
//   - Repeated events that do not change the resolved state are duplicates
//     and never inflate a count.
//   - A success definitively overrides a prior failure of the same instance;
//     a failure observed after a success stays a duplicate and cannot
//     reintroduce a counted failure.
//
// # Windows and alerts
//
// A query supplies a positive width W, two positive thresholds and a query
// time t. Its window is [t-W, t) measured on the fixed start time s. Only
// instances resolved as failed contribute, and distinct endpoints are
// counted, so retransmissions cannot turn a normal client into a scanner:
//
//   - AlertHostPortSweep: one source IP reaches PortThreshold distinct
//     failed destination ports against one destination host.
//   - AlertPortTargetSweep: one source IP reaches HostThreshold distinct
//     failed destination hosts on one destination port.
//
// # Watermark
//
// AdvanceWatermark moves the event-time watermark monotonically. Any new
// instance or state-changing correction with s < w is rejected and flagged
// late; non-changing duplicates behind the watermark remain duplicates. A
// registered query with t > w stays open and can have its alerts withdrawn by
// an in-time correction. Once w >= t the query window freezes and its answer
// never changes again; queries registered after that point are frozen
// immediately.
//
// # Memory
//
// WithMemoryLimit bounds the approximate resident instance state. When an
// insertion would exceed the budget the event is rejected with
// IngestMemoryLimit and the detector (and every later snapshot) is marked
// incomplete; the flag is sticky because rejected events can never be
// reconstructed.
package netsec
