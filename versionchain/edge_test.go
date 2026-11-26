package versionchain

import "testing"

func TestEmptyChain(t *testing.T) {
	reclaim := Prune(Chain{})
	if len(reclaim.Retained) != 0 || len(reclaim.Reclaimed) != 0 || len(reclaim.Predecessor) != 0 {
		t.Fatalf("empty chain must produce an empty result, got %+v", reclaim)
	}
}

func TestSortReclaimed(t *testing.T) {
	chain := Chain{
		Versions: []Version{
			{ID: 3},
			{ID: 2},
			{ID: 1},
		},
	}
	ids := []VersionID{1, 3, 2}
	SortReclaimed(chain, ids)
	assertIDs(t, "sorted ids", ids, []VersionID{3, 2, 1})
}

func TestScopePrefixHelpers(t *testing.T) {
	if !isPrefix([]uint64{1}, []uint64{1, 2, 3}) {
		t.Fatal("ancestor scope must be a prefix")
	}
	if !isPrefix([]uint64{1, 2}, []uint64{1, 2}) {
		t.Fatal("equal scope stacks must be a prefix")
	}
	if isPrefix([]uint64{1, 9}, []uint64{1, 2}) {
		t.Fatal("diverging scope stacks must not be a prefix")
	}
	if isStrictPrefix([]uint64{1, 2}, []uint64{1, 2}) {
		t.Fatal("equal scope stacks must not be a strict prefix")
	}
	if !isStrictPrefix([]uint64{1}, []uint64{1, 2}) {
		t.Fatal("nested scope stack must be a strict prefix")
	}
}

func TestSlotZeroHorizonPinsAllCommitted(t *testing.T) {
	chain := Chain{
		Versions: []Version{
			{ID: 2, Txn: 2, TxnSeq: 1, Scopes: scope(2), CommitSeq: 2},
			{ID: 1, Txn: 1, TxnSeq: 1, Scopes: scope(1), Aborted: true},
		},
		Slot: &ReplicationSlot{RetainAtLeast: 0},
	}
	reclaim := Prune(chain)
	assertIDs(t, "retained", idsOf(reclaim.Retained), []VersionID{2})
	assertIDs(t, "reclaimed", reclaim.Reclaimed, []VersionID{1})
}

func TestSnapshotWithoutVisibleVersion(t *testing.T) {
	chain := Chain{
		Versions: []Version{
			{ID: 1, Txn: 1, TxnSeq: 1, Scopes: scope(1), CommitSeq: 10},
		},
		Snapshots: []Snapshot{
			{ID: 1, VisibilityUpperBound: 5},
		},
	}
	// Nothing pins the sole committed version once no snapshot can see it.
	reclaim := Prune(chain)
	if len(reclaim.Retained) != 0 {
		t.Fatalf("expected empty chain, retained %v", idsOf(reclaim.Retained))
	}
	assertIDs(t, "reclaimed", reclaim.Reclaimed, []VersionID{1})
}
