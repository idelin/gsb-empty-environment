package snapconsist

import (
	"context"
	"sync"
)

// Replica is a read node that asynchronously applies the leader's log. It can
// only serve checks whose required revision is no newer than appliedRev.
type Replica struct {
	leader *Leader

	mu           sync.RWMutex
	cond         *sync.Cond
	appliedRev   uint64
	members      map[tupleKey]bool
	aclFloor     map[string]uint64
	contentFloor map[string]uint64
}

// NewReplica creates an empty replica that follows leader.
func NewReplica(leader *Leader) *Replica {
	r := &Replica{
		leader:       leader,
		members:      make(map[tupleKey]bool),
		aclFloor:     make(map[string]uint64),
		contentFloor: make(map[string]uint64),
	}
	r.cond = sync.NewCond(&r.mu)
	return r
}

// AppliedZookie returns the zookie of the newest log entry the replica has
// applied. A default check uses this as its snapshot timestamp.
func (r *Replica) AppliedZookie() Zookie {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return zookieForRevision(r.appliedRev)
}

// AppliedRevision is the numeric form of AppliedZookie.
func (r *Replica) AppliedRevision() uint64 {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.appliedRev
}

// CatchUp pulls and applies every leader entry newer than the replica's
// current revision. It returns the resulting applied revision.
func (r *Replica) CatchUp(ctx context.Context) (uint64, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	entries := r.leader.entriesAfter(r.AppliedRevision())
	r.apply(entries)
	return r.AppliedRevision(), nil
}

// CatchUpTo applies entries until the replica has reached at least target or
// the context expires. It returns the revision actually reached.
func (r *Replica) CatchUpTo(ctx context.Context, target uint64) (uint64, error) {
	for r.AppliedRevision() < target {
		if err := ctx.Err(); err != nil {
			return r.AppliedRevision(), err
		}
		if _, err := r.CatchUp(ctx); err != nil {
			return r.AppliedRevision(), err
		}
	}
	return r.AppliedRevision(), nil
}

// waitApplied blocks until the replica has applied at least minRev or ctx is
// done. The context cancellation wakes the condition via AfterFunc so callers
// never miss the deadline while parked.
func (r *Replica) waitApplied(ctx context.Context, minRev uint64) error {
	stop := context.AfterFunc(ctx, func() {
		r.cond.Broadcast()
	})
	defer stop()

	r.mu.Lock()
	defer r.mu.Unlock()
	for r.appliedRev < minRev {
		if err := ctx.Err(); err != nil {
			return err
		}
		r.cond.Wait()
	}
	return nil
}

// checkLocal answers a membership query from the replica's current snapshot.
// Callers are responsible for ensuring the snapshot is fresh enough.
func (r *Replica) checkLocal(tuple Tuple) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.members[tupleKeyOf(tuple)]
}

// apply folds a batch of leader entries into the local snapshot and wakes any
// check blocked in waitApplied.
func (r *Replica) apply(entries []logEntry) {
	if len(entries) == 0 {
		return
	}
	r.mu.Lock()
	for _, entry := range entries {
		switch entry.kind {
		case kindACL:
			for _, change := range entry.changes {
				key := tupleKeyOf(change.Tuple)
				if change.Remove {
					delete(r.members, key)
				} else {
					r.members[key] = true
				}
			}
			r.aclFloor[entry.object] = entry.revision
		case kindContent:
			r.contentFloor[entry.object] = entry.revision
		}
		r.appliedRev = entry.revision
	}
	r.cond.Broadcast()
	r.mu.Unlock()
}
