package versionchain

import (
	"math/rand"
	"testing"
)

// naiveVisible is an independent, deliberately straightforward transcription
// of the per-version visibility rules used to cross-check Prune.
func naiveVisible(ver *Version, snap *Snapshot) bool {
	sameTxn := snap.Txn != 0 && ver.Txn == snap.Txn
	if sameTxn {
		if ver.TxnSeq > snap.SnapSeq {
			return false
		}
		if ver.Aborted {
			return isStrictPrefix(ver.Scopes, snap.ScopeStack)
		}
		return isPrefix(ver.Scopes, snap.ScopeStack) || isPrefix(snap.ScopeStack, ver.Scopes)
	}
	if ver.Aborted || ver.CommitSeq == 0 {
		return false
	}
	return ver.CommitSeq <= snap.VisibilityUpperBound
}

// naivePrune recomputes the retention decision by walking every
// snapshot/version pair explicitly, without reusing Prune's bookkeeping.
func naivePrune(chain Chain) map[VersionID]bool {
	keep := make(map[VersionID]bool)

	for snapIdx := range chain.Snapshots {
		snap := &chain.Snapshots[snapIdx]
		for verIdx := range chain.Versions {
			ver := &chain.Versions[verIdx]
			if naiveVisible(ver, snap) {
				keep[ver.ID] = true
				break
			}
		}
	}

	for verIdx := range chain.Versions {
		ver := &chain.Versions[verIdx]
		switch {
		case ver.CommitSeq == 0 && !ver.Aborted:
			keep[ver.ID] = true
		case ver.CommitSeq != 0:
			if chain.Slot != nil && ver.CommitSeq >= chain.Slot.RetainAtLeast {
				keep[ver.ID] = true
			}
		}
	}
	return keep
}

// TestPruneAgainstNaiveSimulation builds randomized chains, including nested
// savepoint rollbacks and in-transaction read views, and compares Prune with
// the naive per-version simulation.
func TestPruneAgainstNaiveSimulation(t *testing.T) {
	for seed := int64(0); seed < 500; seed++ {
		rng := rand.New(rand.NewSource(seed))
		chain := generateRandomChain(rng)

		reclaim := Prune(chain)
		wantKeep := naivePrune(chain)

		gotKeep := make(map[VersionID]bool, len(reclaim.Retained))
		for _, ver := range reclaim.Retained {
			gotKeep[ver.ID] = true
		}
		for id := range gotKeep {
			if !wantKeep[id] {
				t.Fatalf("seed %d: version %d retained but naive simulation reclaims it", seed, id)
			}
		}
		for id := range wantKeep {
			if !gotKeep[id] {
				t.Fatalf("seed %d: version %d reclaimed but naive simulation retains it", seed, id)
			}
		}

		// Reclaimed IDs must be exactly the input versions not kept, in
		// input chain order.
		var wantReclaimed []VersionID
		for _, ver := range chain.Versions {
			if !wantKeep[ver.ID] {
				wantReclaimed = append(wantReclaimed, ver.ID)
			}
		}
		assertIDs(t, "reclaimed set", reclaim.Reclaimed, wantReclaimed)

		// The retained slice and predecessor map must describe one
		// contiguous surviving chain.
		assertChainPointers(t, reclaim)
		if len(reclaim.Retained) != len(wantKeep) {
			t.Fatalf("seed %d: retained count mismatch", seed)
		}

		// Every snapshot's newest visible version, if any, must survive.
		for snapIdx := range chain.Snapshots {
			snap := &chain.Snapshots[snapIdx]
			for verIdx := range chain.Versions {
				ver := &chain.Versions[verIdx]
				if Visible(ver, snap) {
					if !gotKeep[ver.ID] {
						t.Fatalf("seed %d: snapshot %d lost its newest visible version %d", seed, snap.ID, ver.ID)
					}
					break
				}
			}
		}
	}
}

// generateRandomChain produces a plausible single-key chain with up to three
// nested savepoint levels, mixed aborted/committed/in-progress versions,
// foreign and same-transaction snapshots, and an optional replication slot.
func generateRandomChain(rng *rand.Rand) Chain {
	txnCount := 1 + rng.Intn(5)
	type txnInfo struct {
		id       TxnID
		scopes   [][]uint64
		aborted  bool
		commitAt CommitSeq
	}
	txns := make([]txnInfo, txnCount)
	for i := range txns {
		txnID := TxnID(i + 1)
		depth := 1 + rng.Intn(3)
		scopes := [][]uint64{{uint64(txnID) * 10}}
		for level := 1; level < depth; level++ {
			parent := scopes[len(scopes)-1]
			child := append(append([]uint64{}, parent...), parent[len(parent)-1]+uint64(level))
			scopes = append(scopes, child)
		}
		txns[i] = txnInfo{
			id:      txnID,
			scopes:  scopes,
			aborted: rng.Intn(4) == 0,
		}
	}

	var versions []Version
	var nextID VersionID
	var globalSeq CommitSeq
	for i := range txns {
		info := &txns[i]
		versionCount := 1 + rng.Intn(4)
		if info.aborted {
			info.commitAt = 0
		} else if rng.Intn(5) == 0 {
			info.commitAt = 0 // still in progress
		} else {
			globalSeq += CommitSeq(1 + rng.Intn(3))
			info.commitAt = globalSeq
		}

		for v := 0; v < versionCount; v++ {
			nextID++
			ver := Version{
				ID:      nextID,
				Txn:     info.id,
				TxnSeq:  uint64(v + 1),
				Scopes:  append([]uint64{}, info.scopes[rng.Intn(len(info.scopes))]...),
				Delete:  rng.Intn(3) == 0,
				Aborted: info.aborted,
			}
			if !info.aborted {
				ver.CommitSeq = info.commitAt
			}
			versions = append(versions, ver)
		}
	}

	// Newest first: reverse the transaction-ordered construction.
	for left, right := 0, len(versions)-1; left < right; left, right = left+1, right-1 {
		versions[left], versions[right] = versions[right], versions[left]
	}

	var snapshots []Snapshot
	var nextSnap SnapshotID
	for _, info := range txns {
		snapshotCount := rng.Intn(3)
		for s := 0; s < snapshotCount; s++ {
			nextSnap++
			snap := Snapshot{ID: nextSnap}
			if rng.Intn(2) == 0 {
				// Foreign/committed-context snapshot with a random horizon.
				snap.VisibilityUpperBound = CommitSeq(rng.Intn(int(globalSeq) + 2))
			} else {
				// In-transaction read view at one of the txn's scopes.
				snap.Txn = info.id
				snap.ScopeStack = append([]uint64{}, info.scopes[rng.Intn(len(info.scopes))]...)
				snap.SnapSeq = uint64(1 + rng.Intn(5))
				snap.VisibilityUpperBound = globalSeq
			}
			snapshots = append(snapshots, snapshots...)
		}
	}

	chain := Chain{Versions: versions, Snapshots: snapshots}
	if rng.Intn(2) == 0 && globalSeq > 0 {
		chain.Slot = &ReplicationSlot{RetainAtLeast: CommitSeq(rng.Intn(int(globalSeq) + 2))}
	}
	return chain
}
