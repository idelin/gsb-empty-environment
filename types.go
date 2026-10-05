// Package checkpoint implements a content-addressed checkpoint store.
//
// Multiple snapshots share base chunks; an incremental snapshot only
// records the references it adds and removes relative to its parents.
// Snapshots form an acyclic dependency graph, and each snapshot digest
// binds its parent snapshots and the exact order of its operations.
package checkpoint

import (
	"crypto/sha256"
	"encoding/hex"
)

// Hash is a content address (SHA-256).
type Hash [32]byte

// HashBytes returns the content address of b.
func HashBytes(b []byte) Hash { return sha256.Sum256(b) }

func (h Hash) String() string { return hex.EncodeToString(h[:8]) }

// OpKind selects the kind of a reference operation.
type OpKind int

const (
	// OpAdd adds (or replaces) a named reference to a chunk.
	OpAdd OpKind = iota
	// OpRemove removes a named reference. It only affects the branch
	// the snapshot is committed on; other branches keep their state.
	OpRemove
)

// Op is a single reference mutation recorded by an incremental snapshot.
type Op struct {
	Kind  OpKind
	Name  string
	Chunk Hash // meaningful only for OpAdd
}

// Add is a convenience constructor for an OpAdd.
func Add(name string, chunk Hash) Op { return Op{Kind: OpAdd, Name: name, Chunk: chunk} }

// Remove is a convenience constructor for an OpRemove.
func Remove(name string) Op { return Op{Kind: OpRemove, Name: name} }

// Snapshot is an immutable node in the checkpoint DAG.
//
// Parents holds 0 (root), 1 (linear) or 2 (merge) parent snapshot IDs.
// Ops are applied in order on top of the merged parent state.
// Digest commits to the parent digests and the operation sequence.
type Snapshot struct {
	ID      string
	Parents []string
	Ops     []Op
	Digest  Hash
}
