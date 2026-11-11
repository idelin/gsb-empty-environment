// Package netsec implements authenticated range writes for a backup
// restore host.
//
// A restore image is described as a set of byte ranges: authenticated
// data ranges and sparse hole ranges. The ranges must partition the full
// logical image: they start at offset zero, never overlap, leave no gaps,
// and end exactly at the logical size.
//
// Sparse holes must read back as zero. Target blocks may be recycled
// blocks that still contain stale data, so the writer never relies on a
// freshly allocated block being zero: every block is assembled in a
// zero-initialized buffer (hole bytes are explicitly cleared, data bytes
// are copied from authenticated payloads) and written as one complete
// block. No read-modify-write of stale block contents is ever performed.
//
// A segment-level coverage ledger is maintained while writing. Before the
// restore is published, the ledger is checked segment by segment to prove
// that the entire logical length was covered, and every hole segment is
// re-read from the target device to prove it returns zero.
package netsec
