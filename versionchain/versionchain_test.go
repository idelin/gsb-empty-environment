package versionchain

import "testing"

// scope is a small helper for building subtransaction stacks.
func scope(ids ...uint64) []uint64 { return ids }

// idsOf returns the IDs of a retained version slice in slice order.
func idsOf(versions []Version) []VersionID {
	ids := make([]VersionID, 0, len(versions))
	for _, ver := range versions {
		ids = append(ids, ver.ID)
	}
	return ids
}

func assertIDs(t *testing.T, label string, got, want []VersionID) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s: got %v, want %v", label, got, want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Fatalf("%s: got %v, want %v", label, got, want)
		}
	}
}

// TestNestedSavepointMiddleRollback covers a three-level nested savepoint
// structure where only the middle level is rolled back.
func TestNestedSavepointMiddleRollback(t *testing.T) {
	const txn TxnID = 100
	chain := Chain{
		Versions: []Version{
			// Outer scope version committed after the rollback.
			{ID: 4, Txn: txn, TxnSeq: 4, Scopes: scope(100), CommitSeq: 10},
			// Deepest savepoint version: the whole branch disappears when
			// the middle savepoint is rolled back.
			{ID: 3, Txn: txn, TxnSeq: 3, Scopes: scope(100, 2, 3), Aborted: true},
			// Middle savepoint version: rolled back through savepoint 2.
			{ID: 2, Txn: txn, TxnSeq: 2, Scopes: scope(100, 2), Aborted: true},
			// Initial outer-scope version.
			{ID: 1, Txn: txn, TxnSeq: 1, Scopes: scope(100), CommitSeq: 9},
		},
		Snapshots: []Snapshot{
			// A foreign snapshot whose horizon includes both committed
			// outer-scope versions: its newest visible version is 4.
			{ID: 1, VisibilityUpperBound: 10},
			// An older foreign snapshot that still reads version 1.
			{ID: 4, VisibilityUpperBound: 9},
			// A same-transaction read view at the outer scope, taken after
			// the rollback. The aborted branches must be invisible to it.
			{ID: 2, Txn: txn, ScopeStack: scope(100), SnapSeq: 4},
		},
	}

	reclaim := Prune(chain)
	assertIDs(t, "retained", idsOf(reclaim.Retained), []VersionID{4, 1})
	assertIDs(t, "reclaimed", reclaim.Reclaimed, []VersionID{3, 2})
	if pred, ok := reclaim.Predecessor[4]; !ok || pred != 1 {
		t.Fatalf("version 4 predecessor = %d (present=%v), want 1", pred, ok)
	}
	if _, ok := reclaim.Predecessor[1]; ok {
		t.Fatal("chain tail 1 must not carry a predecessor entry")
	}

	// The snapshots' newest visible versions must both survive.
	if !Visible(&chain.Versions[0], &chain.Snapshots[0]) {
		t.Fatal("foreign snapshot should see committed version 4")
	}
	if !Visible(&chain.Versions[0], &chain.Snapshots[2]) {
		t.Fatal("outer-scope read view should see own version 4")
	}
	for idx := 1; idx <= 2; idx++ {
		if Visible(&chain.Versions[idx], &chain.Snapshots[2]) {
			t.Fatalf("aborted version %d must be invisible to outer read view", chain.Versions[idx].ID)
		}
	}

	// A read view still positioned strictly inside the rolled-back middle
	// branch keeps seeing its dead versions until that view exits. A view
	// sitting exactly at the rolled-back scope is already past the rollback.
	insideView := Snapshot{ID: 3, Txn: txn, ScopeStack: scope(100, 2, 3), SnapSeq: 3}
	if !Visible(&chain.Versions[2], &insideView) {
		t.Fatal("read view nested inside rolled-back branch must still see version 2")
	}
	atBranchView := Snapshot{ID: 4, Txn: txn, ScopeStack: scope(100, 2), SnapSeq: 3}
	if Visible(&chain.Versions[2], &atBranchView) {
		t.Fatal("read view at the rolled-back scope must not see version 2")
	}
	withInsideView := chain
	withInsideView.Snapshots = append(append([]Snapshot{}, chain.Snapshots...), insideView)
	reclaim = Prune(withInsideView)
	assertIDs(t, "retained with inside view", idsOf(reclaim.Retained), []VersionID{4, 2, 1})
	assertIDs(t, "reclaimed with inside view", reclaim.Reclaimed, []VersionID{3})
	if pred := reclaim.Predecessor[4]; pred != 2 {
		t.Fatalf("version 4 predecessor = %d, want 2", pred)
	}
	if pred := reclaim.Predecessor[2]; pred != 1 {
		t.Fatalf("version 2 predecessor = %d, want 1", pred)
	}
}

// TestDeleteThenInsertKeepsTombstone covers a same-transaction delete
// followed by a re-insert: an older snapshot still depends on the tombstone.
func TestDeleteThenInsertKeepsTombstone(t *testing.T) {
	const txn TxnID = 200
	chain := Chain{
		Versions: []Version{
			// Re-insert after the delete.
			{ID: 30, Txn: txn, TxnSeq: 2, Scopes: scope(200), CommitSeq: 30},
			// Deletion tombstone created earlier inside the same transaction.
			{ID: 20, Txn: txn, TxnSeq: 1, Scopes: scope(200), CommitSeq: 29, Delete: true},
		},
		Snapshots: []Snapshot{
			{ID: 1, VisibilityUpperBound: 29}, // sees the tombstone
			{ID: 2, VisibilityUpperBound: 30}, // sees the re-insert
		},
	}

	reclaim := Prune(chain)
	assertIDs(t, "retained", idsOf(reclaim.Retained), []VersionID{30, 20})
	if len(reclaim.Reclaimed) != 0 {
		t.Fatalf("nothing should be reclaimed, got %v", reclaim.Reclaimed)
	}
	if pred := reclaim.Predecessor[30]; pred != 20 {
		t.Fatalf("version 30 predecessor = %d, want 20", pred)
	}

	// Once the older snapshot advances past the tombstone, the tombstone is
	// an invisible intermediate version and only the re-insert survives.
	chain.Snapshots = []Snapshot{{ID: 2, VisibilityUpperBound: 30}}
	reclaim = Prune(chain)
	assertIDs(t, "retained after snapshot advance", idsOf(reclaim.Retained), []VersionID{30})
	assertIDs(t, "reclaimed after snapshot advance", reclaim.Reclaimed, []VersionID{20})
	if _, ok := reclaim.Predecessor[30]; ok {
		t.Fatalf("sole surviving version 30 must not carry a predecessor: %v", reclaim.Predecessor)
	}
}

// TestReplicationSlotPinsOlderThanAllSnapshots covers a replication slot
// pinning history older than every active transaction snapshot.
func TestReplicationSlotPinsOlderThanAllSnapshots(t *testing.T) {
	chain := Chain{
		Versions: []Version{
			{ID: 5, Txn: 50, TxnSeq: 1, Scopes: scope(50), CommitSeq: 50},
			{ID: 4, Txn: 40, TxnSeq: 1, Scopes: scope(40), CommitSeq: 40},
			{ID: 3, Txn: 30, TxnSeq: 1, Scopes: scope(30), CommitSeq: 30},
			{ID: 2, Txn: 20, TxnSeq: 1, Scopes: scope(20), CommitSeq: 20},
			{ID: 1, Txn: 10, TxnSeq: 1, Scopes: scope(10), CommitSeq: 10},
		},
		Snapshots: []Snapshot{
			{ID: 1, VisibilityUpperBound: 45}, // newest visible is version 4
		},
		Slot: &ReplicationSlot{RetainAtLeast: 30},
	}

	reclaim := Prune(chain)
	// Snapshot pins 4, the slot horizon pins 5/4/3. Versions 2 and 1 are
	// below every retention horizon; without any pin, even the chain head
	// (here version 5 is covered by the slot) would be reclaimable.
	assertIDs(t, "retained", idsOf(reclaim.Retained), []VersionID{5, 4, 3})
	assertIDs(t, "reclaimed", reclaim.Reclaimed, []VersionID{2, 1})
	assertChainPointers(t, reclaim)

	// Without the slot, the head version 5 has no snapshot depending on it
	// and is reclaimed together with versions 3/2/1; only snapshot-pinned 4
	// survives.
	chain.Slot = nil
	reclaim = Prune(chain)
	assertIDs(t, "retained without slot", idsOf(reclaim.Retained), []VersionID{4})
	assertIDs(t, "reclaimed without slot", reclaim.Reclaimed, []VersionID{5, 3, 2, 1})
	assertChainPointers(t, reclaim)
}

// TestOnlyTombstoneNoOlderSnapshotEmptiesChain covers the full-clear case:
// the sole surviving version is a tombstone and no older snapshot depends on
// its upper bound.
func TestOnlyTombstoneNoOlderSnapshotEmptiesChain(t *testing.T) {
	chain := Chain{
		Versions: []Version{
			{ID: 7, Txn: 70, TxnSeq: 1, Scopes: scope(70), CommitSeq: 70, Delete: true},
			{ID: 6, Txn: 60, TxnSeq: 1, Scopes: scope(60), CommitSeq: 60},
		},
		Snapshots: []Snapshot{
			{ID: 1, VisibilityUpperBound: 70},
		},
	}

	reclaim := Prune(chain)
	// The live snapshot still sees the tombstone as its newest version.
	assertIDs(t, "retained while snapshot needs tombstone", idsOf(reclaim.Retained), []VersionID{7})
	assertIDs(t, "reclaimed while snapshot needs tombstone", reclaim.Reclaimed, []VersionID{6})

	// Once every snapshot has moved beyond the tombstone (or gone away),
	// nothing visible remains and the whole chain is reclaimed.
	chain.Snapshots = nil
	reclaim = Prune(chain)
	if len(reclaim.Retained) != 0 {
		t.Fatalf("chain should be empty, retained %v", idsOf(reclaim.Retained))
	}
	assertIDs(t, "fully reclaimed", reclaim.Reclaimed, []VersionID{7, 6})
	if len(reclaim.Predecessor) != 0 {
		t.Fatalf("empty chain must not carry predecessor pointers: %v", reclaim.Predecessor)
	}

	// A later snapshot with a higher horizon still sees the tombstone as
	// the newest committed version, so it must not be collected.
	chain.Snapshots = []Snapshot{{ID: 2, VisibilityUpperBound: 99}}
	reclaim = Prune(chain)
	assertIDs(t, "retained with advanced snapshot", idsOf(reclaim.Retained), []VersionID{7})
}

// TestInProgressAndAbortedVersions covers not-yet-committed live versions and
// top-level aborted transactions.
func TestInProgressAndAbortedVersions(t *testing.T) {
	chain := Chain{
		Versions: []Version{
			// In-progress version: outcome unknown, must not be collected.
			{ID: 4, Txn: 99, TxnSeq: 2, Scopes: scope(99)},
			// Dead version of an aborted transaction.
			{ID: 3, Txn: 98, TxnSeq: 1, Scopes: scope(98), Aborted: true},
			// Newest committed version of the chain.
			{ID: 2, Txn: 97, TxnSeq: 1, Scopes: scope(97), CommitSeq: 80},
			// Old committed version nobody needs any more.
			{ID: 1, Txn: 96, TxnSeq: 1, Scopes: scope(96), CommitSeq: 70},
		},
		Snapshots: []Snapshot{{ID: 1, VisibilityUpperBound: 80}},
	}

	reclaim := Prune(chain)
	assertIDs(t, "retained", idsOf(reclaim.Retained), []VersionID{4, 2})
	assertIDs(t, "reclaimed", reclaim.Reclaimed, []VersionID{3, 1})
	assertChainPointers(t, reclaim)

	// An aborted top-level version is invisible to a foreign snapshot and
	// even to the owning transaction's read view after the branch is gone.
	dead := chain.Versions[1]
	foreign := Snapshot{ID: 2, VisibilityUpperBound: 100}
	if Visible(&dead, &foreign) {
		t.Fatal("aborted version must be invisible to foreign snapshots")
	}
	topView := Snapshot{ID: 3, Txn: 98, ScopeStack: scope(98), SnapSeq: 5}
	if Visible(&dead, &topView) {
		t.Fatal("aborted version must be invisible once the branch is gone")
	}
}

// assertChainPointers verifies that predecessor pointers only reference
// retained versions and reproduce the input order of the surviving chain.
func assertChainPointers(t *testing.T, reclaim Reclaim) {
	t.Helper()
	retained := make(map[VersionID]bool, len(reclaim.Retained))
	for _, ver := range reclaim.Retained {
		retained[ver.ID] = true
	}
	for id, pred := range reclaim.Predecessor {
		if !retained[id] || !retained[pred] {
			t.Fatalf("predecessor %d -> %d references a non-retained version", id, pred)
		}
	}
	for i := 0; i+1 < len(reclaim.Retained); i++ {
		pred, ok := reclaim.Predecessor[reclaim.Retained[i].ID]
		if !ok || pred != reclaim.Retained[i+1].ID {
			t.Fatalf("retained chain order broken at %d", reclaim.Retained[i].ID)
		}
	}
}
