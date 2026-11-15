// Package netsec implements a tenant- and label-partitioned dictionary
// compressor for an embedding/messaging service.
//
// The wire format is deliberately small and is defined by this package:
//
//	literal := 0x00, byte
//	match   := 0x01, distance_hi, distance_lo, length
//
// A match distance is one-based and addresses bytes before the current output
// position. Distances are at most WindowSize, lengths are between MinMatch and
// MaxMatch, the longest match wins, and ties are broken by the smallest
// distance. Matches may overlap the bytes they produce.
//
// Dictionaries are isolated by tenant ID and by Label. A stream captures an
// immutable dictionary snapshot when OpenStream returns. A successful Finish
// appends the stream's plaintext to that tenant/label partition and publishes
// a new dictionary version atomically. The commit linearizes at the
// partition-lock append inside Finish. Streams that take their snapshot after
// that point see the new version; the committing stream and streams that
// already took a snapshot do not. Cancel, errors, ResetTenant, ReleaseTenant,
// and tenant-ID reuse publish nothing and invalidate old versions.
package netsec
