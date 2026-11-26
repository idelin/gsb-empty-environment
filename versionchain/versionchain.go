// Package versionchain prunes the ordered per-key version chain produced by a
// storage engine whose transactions support nested savepoints and partial
// rollback.
//
// A version chain mixes three kinds of entries:
//
//   - dead versions created by aborted transactions or by sub-branches that
//     were rolled back through a savepoint;
//   - intermediate versions overwritten inside the same committing
//     transaction;
//   - historical versions pinned by active read snapshots or logical
//     replication slots.
//
// Prune decides which versions are reclaimable without truncating the
// visibility path of any active snapshot and without breaking chain
// continuity, then returns the new predecessor pointers of every retained
// version after the reclaimable versions are detached.
package versionchain

import "sort"

// TxnID identifies a transaction (and, by convention, its top-level
// transaction scope).
type TxnID uint64

// VersionID uniquely identifies a version inside one key's chain.
type VersionID uint64

// CommitSeq is the logical commit sequence number. A zero value means the
// creating transaction has not committed yet.
type CommitSeq uint64

// SnapshotID uniquely identifies an active read snapshot.
type SnapshotID uint64

// Version is one entry of a single-key version chain.
type Version struct {
	// ID is the unique version identifier.
	ID VersionID

	// Txn is the transaction that created this version.
	Txn TxnID

	// TxnSeq is the monotonically increasing creation ordinal inside Txn.
	// It orders versions that do not yet have a CommitSeq, and orders same
	// transaction versions against an in-transaction read snapshot.
	TxnSeq uint64

	// Scopes is the subtransaction/savepoint stack active when the version
	// was created, from the outermost scope to the innermost one. The first
	// element is conventionally the transaction's top-level scope ID.
	Scopes []uint64

	// Aborted marks versions of aborted transactions or of a sub-branch
	// rolled back through a savepoint. Such versions are invisible to every
	// read view except un-rolled-back read views that still live inside the
	// rolled-back branch itself.
	Aborted bool

	// CommitSeq is the logical commit sequence number. Zero means the
	// transaction is still in progress.
	CommitSeq CommitSeq

	// Delete marks a deletion tombstone.
	Delete bool
}

// Snapshot is an active read snapshot.
type Snapshot struct {
	// ID is the unique snapshot identifier.
	ID SnapshotID

	// VisibilityUpperBound is the snapshot's read horizon: only committed
	// versions whose CommitSeq is less than or equal to it are visible.
	VisibilityUpperBound CommitSeq

	// Txn is the transaction owning the snapshot. Zero means the snapshot
	// belongs to a completed/foreign read context and has no in-transaction
	// visibility.
	Txn TxnID

	// ScopeStack is the savepoint stack of Txn at the moment the snapshot
	// was taken. It is ignored when Txn is zero.
	ScopeStack []uint64

	// SnapSeq is the creation ordinal of the snapshot inside Txn, measured
	// on the same monotonic axis as Version.TxnSeq. Same-transaction
	// versions are visible only when their TxnSeq is not greater than it.
	SnapSeq uint64
}

// ReplicationSlot is a logical replication slot's retention horizon.
type ReplicationSlot struct {
	// RetainAtLeast retains every committed version whose CommitSeq is
	// greater than or equal to this bound. A zero bound pins the whole
	// committed history; a nil slot means no replication retention.
	RetainAtLeast CommitSeq
}

// Chain is the ordered input for one key.
type Chain struct {
	// Versions are ordered newest first, i.e. index 0 is the chain head and
	// each entry's predecessor is the next entry. Version IDs must be
	// unique.
	Versions []Version

	// Snapshots are the currently active read snapshots.
	Snapshots []Snapshot

	// Slot is the logical replication slot retention horizon. Nil disables
	// replication-based retention.
	Slot *ReplicationSlot
}

// Reclaim records the versions that may be garbage collected and the chain
// that remains after detaching them.
type Reclaim struct {
	// Retained is the surviving chain, newest first.
	Retained []Version

	// Reclaimed holds the IDs of versions that are fully invisible and may
	// be garbage collected, ordered the same way as the input chain.
	Reclaimed []VersionID

	// Predecessor maps each retained version ID to the ID of its new
	// predecessor. A missing entry or a zero VersionID means the version is
	// the new chain tail and has no predecessor.
	Predecessor map[VersionID]VersionID
}

// Prune computes the garbage collection decision for one version chain.
func Prune(chain Chain) Reclaim {
	retainedFlags := make([]bool, len(chain.Versions))

	// Rule 1: retain the exact newest version visible to every active
	// snapshot. Dead versions are retained here only when a still-active
	// read view lives inside their rolled-back branch.
	for snapIdx := range chain.Snapshots {
		snap := &chain.Snapshots[snapIdx]
		for verIdx := range chain.Versions {
			ver := &chain.Versions[verIdx]
			if Visible(ver, snap) {
				retainedFlags[verIdx] = true
				break
			}
		}
	}

	// Rule 2: retain in-progress live versions (outcome unknown) and
	// committed versions at or after the replication slot horizon. The
	// newest committed version is already retained whenever a snapshot can
	// see it or the slot covers it; once no older snapshot depends on its
	// upper bound, even a tombstone may be reclaimed and the chain clears.
	for verIdx := range chain.Versions {
		ver := &chain.Versions[verIdx]
		if ver.CommitSeq == 0 {
			if !ver.Aborted {
				retainedFlags[verIdx] = true
			}
			continue
		}
		if chain.Slot != nil && ver.CommitSeq >= chain.Slot.RetainAtLeast {
			retainedFlags[verIdx] = true
		}
	}

	result := Reclaim{Predecessor: make(map[VersionID]VersionID)}
	for verIdx := range chain.Versions {
		ver := chain.Versions[verIdx]
		if !retainedFlags[verIdx] {
			result.Reclaimed = append(result.Reclaimed, ver.ID)
			continue
		}
		result.Retained = append(result.Retained, ver)
	}
	// The input chain is newest first, so each retained version's new
	// predecessor is the next surviving entry (its older neighbor).
	for i := 0; i+1 < len(result.Retained); i++ {
		result.Predecessor[result.Retained[i].ID] = result.Retained[i+1].ID
	}
	return result
}

// Visible reports whether ver is visible to snap using the full per-version
// visibility rules. It is exported so callers can audit individual decisions.
func Visible(ver *Version, snap *Snapshot) bool {
	if snap.Txn != 0 && ver.Txn == snap.Txn {
		return visibleSameTxn(ver, snap)
	}

	// Foreign or committed contexts only see committed, non-aborted versions
	// inside their read horizon.
	if ver.Aborted || ver.CommitSeq == 0 {
		return false
	}
	return ver.CommitSeq <= snap.VisibilityUpperBound
}

// visibleSameTxn implements in-transaction read view semantics.
func visibleSameTxn(ver *Version, snap *Snapshot) bool {
	// The snapshot cannot see versions created after itself.
	if ver.TxnSeq > snap.SnapSeq {
		return false
	}

	if ver.Aborted {
		// A rolled-back version is visible only to a read view that still
		// lives strictly inside the rolled-back sub-branch. A read view at
		// the branch scope itself or at any ancestor scope is positioned
		// after the rollback and must not see it.
		return isStrictPrefix(ver.Scopes, snap.ScopeStack)
	}

	// Ordinary own versions are visible across sibling savepoint scopes:
	// either the version's scope is an ancestor of the view, or the view's
	// scope is an ancestor of the version's scope.
	return isPrefix(ver.Scopes, snap.ScopeStack) || isPrefix(snap.ScopeStack, ver.Scopes)
}

// isStrictPrefix reports whether prefix is a strict ancestor scope stack of
// scopeStack, i.e. scopeStack is nested at least one level deeper.
func isStrictPrefix(prefix, scopeStack []uint64) bool {
	return len(prefix) < len(scopeStack) && isPrefix(prefix, scopeStack)
}

// isPrefix reports whether prefix is an ancestor-or-self scope stack of
// scopeStack. Scope identity is compared position by position.
func isPrefix(prefix, scopeStack []uint64) bool {
	if len(prefix) > len(scopeStack) {
		return false
	}
	for i := range prefix {
		if prefix[i] != scopeStack[i] {
			return false
		}
	}
	return true
}

// SortReclaimed orders reclaimed IDs by their position in the input chain.
// Prune already emits them in chain order; this helper exists for callers
// that assemble reclaim sets independently.
func SortReclaimed(chain Chain, ids []VersionID) {
	position := make(map[VersionID]int, len(chain.Versions))
	for i := range chain.Versions {
		position[chain.Versions[i].ID] = i
	}
	sort.Slice(ids, func(i, j int) bool {
		return position[ids[i]] < position[ids[j]]
	})
}
