// Package replicaread implements quorum reads over versioned replica
// responses. Each replica answers with a value or a tombstone stamped
// with a (Term, Seq) version. Once a configurable number of replies
// arrive, the session decides on the highest version; equal versions
// carrying different content are reported as conflicts. Read-repair is
// emitted only for replicas that have already confirmed an older
// version, and a late higher-version reply opens a new result
// generation without mutating previously decided receipts.
package replicaread

import (
	"bytes"
	"fmt"
)

// Version identifies a write by the term it was issued in and its
// sequence number within that term. Versions order lexicographically
// by (Term, Seq).
type Version struct {
	Term uint64
	Seq  uint64
}

// Compare returns -1, 0 or +1 depending on whether v orders before,
// equal to, or after other.
func (v Version) Compare(other Version) int {
	if v.Term != other.Term {
		if v.Term < other.Term {
			return -1
		}
		return 1
	}
	if v.Seq != other.Seq {
		if v.Seq < other.Seq {
			return -1
		}
		return 1
	}
	return 0
}

func (v Version) String() string {
	return fmt.Sprintf("%d.%d", v.Term, v.Seq)
}

// Content is the payload a replica stores for a version: either a
// tombstone (the value was deleted) or an opaque byte value.
type Content struct {
	Tombstone bool
	Value     []byte
}

// Equal reports whether two contents are identical, including the
// tombstone marker.
func (c Content) Equal(other Content) bool {
	return c.Tombstone == other.Tombstone && bytes.Equal(c.Value, other.Value)
}

func (c Content) clone() Content {
	out := Content{Tombstone: c.Tombstone}
	if c.Value != nil {
		out.Value = append([]byte(nil), c.Value...)
	}
	return out
}

// Response is a single replica's answer to a read.
type Response struct {
	Replica string
	Version Version
	Content Content
}

// Receipt is the immutable record of one decided generation. Acks
// lists the replicas that vouched for the decided version.
type Receipt struct {
	Generation uint64
	Version    Version
	Content    Content
	Acks       []string
}

func (r Receipt) clone() Receipt {
	out := r
	out.Content = r.Content.clone()
	out.Acks = append([]string(nil), r.Acks...)
	return out
}

// Repair instructs the caller to write the decided content back to a
// replica that confirmed it holds an older version.
type Repair struct {
	Replica string
	Version Version
	Content Content
}

// ConflictError is returned when two replicas report different content
// for the same version.
type ConflictError struct {
	Version Version
	// Replicas holds the IDs known to disagree, sorted for
	// determinism.
	Replicas []string
}

func (e *ConflictError) Error() string {
	return fmt.Sprintf("replicaread: conflicting content for version %s from replicas %v", e.Version, e.Replicas)
}
