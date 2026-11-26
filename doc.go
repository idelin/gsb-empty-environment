// Package gin implements an in-memory, concurrent GIN (generalized inverted
// index) with a fast-update pending list and tri-state query evaluation.
//
// The package is deliberately independent of a particular heap or storage
// engine. A caller supplies an opaque TID, index keys extracted from an array
// or a JSONB document, and a tri-state predicate used to combine postings.
//
// Writes normally go to a pending list. Flush atomically folds pending entries
// into an ordered entry tree containing per-key posting lists. A posting list
// that exceeds the configured threshold is represented by an ordered posting
// tree. Deletions are logical until a flush, so a TID that is deleted and then
// reused cannot be resurrected by an older pending entry.
package gin
