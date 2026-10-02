package lsm

import "errors"

// ErrNotFound is returned by Get when a key is absent or has been deleted.
var ErrNotFound = errors.New("lsm: key not found")

// Entry is a single versioned record inside the LSM tree.
//
// A delete is materialized as an Entry with Tombstone=true (a "tombstone
// marker"). Tombstones shadow every older version of the same key during
// reads, and are physically dropped only when they reach the bottom-most
// level, where no older version can possibly exist underneath.
type Entry struct {
	Key       string
	Value     []byte
	Tombstone bool
	Seq       uint64 // globally monotonic; higher wins during merges
}

// KV is a user-visible key/value pair returned by Scan.
type KV struct {
	Key   string
	Value []byte
}
