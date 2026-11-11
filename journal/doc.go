// Package journal maintains a directory index for a file-collection service
// by consuming an append-only change log transactionally.
//
// # Object identity
//
// Objects are identified by a FileID: a reference number plus a sequence
// number, mirroring an NTFS file reference. When a reference number is reused
// after deletion its sequence is bumped, so the new occupant is a distinct
// FileID. Records carrying a dead (ref, seq) pair land in a graveyard and can
// never mutate the new object, even when the change log is replayed.
//
// # Renames and hard links
//
// A rename is represented by two independently-delivered records,
// ReasonRenameOld and ReasonRenameNew. Both halves are buffered per object and
// the directory entry is moved in a single mutation only once both halves are
// present, so a Snapshot never observes a half-applied rename. Each object
// keeps its full hard-link set (the LinkKeys naming it); rename moves one
// entry, link add/remove grow and shrink the set, and delete tears it down.
//
// # Checkpoints and restart
//
// Records are applied strictly in USN order; records that arrive ahead of the
// next contiguous USN are buffered in the processor, while rename halves that
// have been applied but not yet paired live in the index. Every batch ends
// with an atomic checkpoint (temp file plus rename), including unpaired
// rename halves, so processing resumes across restarts. A change log
// truncated past the resume point is reported as ErrGap and a changed log
// incarnation as ErrJournalID, instead of silently corrupting the index.
package journal
