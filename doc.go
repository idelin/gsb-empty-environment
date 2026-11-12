// Package netsec implements a sealing barrier for multi-partition audit
// streams.
//
// A time window is described by a fixed [Manifest]: the exact set of
// partitions participating in the window together with each partition's
// inclusive sequence-number upper bound. A window root may only be signed
// when every partition contributes a continuous, hash-chained prefix proof
// that reaches its upper bound. A missing partition can never be compensated
// with a newer root from another partition.
//
// Every [Seal] is bound to a window, a partition and a sequence number, so
// seals from older windows cannot be replayed into a newer generation.
// Partition migrations (adding, re-anchoring or removing a partition) only
// take effect when the coordinator opens the next window; out-of-order gap
// filling, idempotent duplicate seals and window-switch checks prove that
// generations can never be mixed.
package netsec
