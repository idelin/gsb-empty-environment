package snapconsist

import (
	"context"
	"sync"
)

// entryKind distinguishes ACL writes from resource-content writes in the
// leader's append-only log.
type entryKind int

const (
	kindACL entryKind = iota
	kindContent
)

// logEntry is one durable write. Entries are ordered by revision, which is
// also the zookie value handed back to the writer.
type logEntry struct {
	revision uint64
	kind     entryKind
	// object identifies the resource targeted by the write; it doubles as the
	// ACL object for kindACL tuples.
	object string
	// changes holds the ACL delta for kindACL entries, including removes.
	changes []ACLChange
}

// Tuple is one relation membership, for example subject "user:ann" is a
// "viewer" of object "doc:1".
type Tuple struct {
	Object   string
	Relation string
	Subject  string
}

// ACLChange is one ACL write request. Remove=true deletes the tuple, which
// models revoking a previously granted relation.
type ACLChange struct {
	Tuple  Tuple
	Remove bool
}

// Leader is the authoritative store. It serializes every write, appends it to
// a revisioned log and tracks per-resource freshness floors.
type Leader struct {
	mu sync.RWMutex
	// entries is the append-only write log; entries[i] has revision i+1, so
	// entries[revision-1] locates any revision without a separate index.
	entries []logEntry
	// aclFloor maps a resource to the revision of its most recent ACL write.
	aclFloor map[string]uint64
	// contentFloor maps a resource to the revision of its most recent content
	// write. Checks of the resource may never use an older snapshot.
	contentFloor map[string]uint64
}

// NewLeader creates an empty leader store at revision zero.
func NewLeader() *Leader {
	return &Leader{
		aclFloor:     make(map[string]uint64),
		contentFloor: make(map[string]uint64),
	}
}

// Latest returns the zookie of the newest applied write. It is the upper bound
// against which future tokens are rejected.
func (l *Leader) Latest() Zookie {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return zookieForRevision(uint64(len(l.entries)))
}

// ResourceFloor returns the minimum snapshot revision a check of object may
// use: the later of the object's last ACL change and its last content change.
// This is the authoritative source a replica consults before answering, so a
// default check can never read a snapshot older than a revocation.
func (l *Leader) ResourceFloor(object string) Zookie {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return zookieForRevision(max(l.aclFloor[object], l.contentFloor[object]))
}

// WriteACL appends an ACL batch (grants and revocations) and returns its
// monotonic zookie. The object's ACL floor advances to this revision so that
// every later check observes the change.
func (l *Leader) WriteACL(object string, changes []ACLChange) Zookie {
	l.mu.Lock()
	defer l.mu.Unlock()

	revision := uint64(len(l.entries)) + 1
	l.entries = append(l.entries, logEntry{
		revision: revision,
		kind:     kindACL,
		object:   object,
		changes:  changes,
	})
	l.aclFloor[object] = revision
	return zookieForRevision(revision)
}

// SaveContent records a resource content write. The returned zookie is stored
// as the resource's content floor; subsequent checks of the resource must use
// a snapshot at least that fresh, matching the zookie the writer received.
func (l *Leader) SaveContent(object string) Zookie {
	l.mu.Lock()
	defer l.mu.Unlock()

	revision := uint64(len(l.entries)) + 1
	l.entries = append(l.entries, logEntry{
		revision: revision,
		kind:     kindContent,
		object:   object,
	})
	l.contentFloor[object] = revision
	return zookieForRevision(revision)
}

// Check evaluates a membership query against the exact snapshot atRev. Rev
// zero is the empty initial snapshot. It is used both by the leader itself
// and when a lagging replica forwards a check.
func (l *Leader) Check(ctx context.Context, atRev uint64, tuple Tuple) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	l.mu.RLock()
	defer l.mu.RUnlock()

	if atRev > uint64(len(l.entries)) {
		return false, ErrFutureZookie
	}
	tuples := materialize(l.entries[:atRev])
	return tuples[tupleKey(tuple)], nil
}

// entriesAfter returns log entries with revisions strictly greater than
// afterRev. Replicas poll this to catch up; the slice is a copy so callers
// can release the lock before applying it.
func (l *Leader) entriesAfter(afterRev uint64) []logEntry {
	l.mu.RLock()
	defer l.mu.RUnlock()

	if afterRev >= uint64(len(l.entries)) {
		return nil
	}
	out := make([]logEntry, len(l.entries)-int(afterRev))
	copy(out, l.entries[afterRev:])
	return out
}

// tupleKey is the map key for membership sets.
type tupleKey struct {
	Object   string
	Relation string
	Subject  string
}

func tupleKeyOf(t Tuple) tupleKey {
	return tupleKey{Object: t.Object, Relation: t.Relation, Subject: t.Subject}
}

// materialize replays a log prefix into the membership set visible at that
// revision. Later writes win: a remove after an add revokes the tuple.
func materialize(prefix []logEntry) map[tupleKey]bool {
	members := make(map[tupleKey]bool)
	for _, entry := range prefix {
		if entry.kind != kindACL {
			continue
		}
		for _, change := range entry.changes {
			key := tupleKeyOf(change.Tuple)
			if change.Remove {
				delete(members, key)
			} else {
				members[key] = true
			}
		}
	}
	return members
}
