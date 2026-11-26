package kt

import (
	"crypto/ed25519"
	"testing"
)

// world holds the evolving directory state across epochs.
type world struct {
	t       *testing.T
	keys    testKeys
	verif   *Verifier
	trees   map[uint32]*Tree
	logs    map[uint32]*MigrationLog
	roots   map[uint32]*SignedRoot
	counter uint64
	serial  uint64
}

func newWorld(t *testing.T) *world {
	keys := mustKeys(t)
	verif, err := NewVerifier(keys.dirPub, []ed25519.PublicKey{keys.issPub}, testDepth)
	if err != nil {
		t.Fatal(err)
	}
	return &world{
		t:     t,
		keys:  keys,
		verif: verif,
		trees: map[uint32]*Tree{},
		logs:  map[uint32]*MigrationLog{},
		roots: map[uint32]*SignedRoot{},
	}
}

func (w *world) nextSerial() uint64 {
	w.serial++
	return w.serial
}

// ingest builds a signed root for an epoch and feeds it to the verifier.
func (w *world) ingest(epoch uint32, tr *Tree, log *MigrationLog) {
	w.t.Helper()
	var prev Hash
	if epoch > FirstEpoch {
		prev = w.roots[epoch-1].Body.Hash()
	}
	sr := signRoot(w.t, w.keys, epoch, tr.RootHash(), w.counter, log.Hash(), prev)
	var old *Tree
	if epoch > FirstEpoch {
		old = w.trees[epoch-1]
	}
	if err := w.verif.IngestEpoch(sr, log, old, tr); err != nil {
		w.t.Fatalf("ingest epoch %d: %v", epoch, err)
	}
	w.trees[epoch] = tr
	w.logs[epoch] = log
	w.roots[epoch] = sr
}

func (w *world) revokeMigration(epoch uint32, prefix BitPrefix, oldTree *Tree) Migration {
	oldHash := mustSubtree(w.t, oldTree, prefix)
	auth := signAuth(w.t, w.keys, epoch, prefix, OpRevokeOut, Hash{},
		nonce(byte(epoch), byte(prefix.Len())), w.nextSerial())
	return Migration{
		Epoch:   epoch,
		Prefix:  prefix,
		Op:      OpRevokeOut,
		OldHash: oldHash,
		Auth:    auth,
	}
}

func (w *world) splitMigration(epoch uint32, prefix BitPrefix, oldTree, newTree *Tree) Migration {
	// When the previous epoch left this region empty, the split's OldHash is
	// the empty subtree commitment in the new epoch (hashes are epoch-bound).
	oldHash := mustSubtree(w.t, oldTree, prefix)
	if oldHash == recomputeEmpty(oldTree.Epoch(), oldTree.LabelDepth(), prefix) {
		oldHash = recomputeEmpty(epoch, oldTree.LabelDepth(), prefix)
	}
	newHash := mustSubtree(w.t, newTree, prefix)
	auth := signAuth(w.t, w.keys, epoch, prefix, OpSplit, newHash,
		nonce(byte(epoch), byte(prefix.Len()), 0x02), w.nextSerial())
	return Migration{
		Epoch:   epoch,
		Prefix:  prefix,
		Op:      OpSplit,
		OldHash: oldHash,
		NewHash: newHash,
		Auth:    auth,
	}
}

func (w *world) mergeMigration(epoch uint32, prefix BitPrefix, oldTree, newTree *Tree) Migration {
	// Merge semantics: prefix is the parent subtree; OldHash is its previous
	// hash and SiblingHash the hash of its 1-child inside the old tree.
	child1, _ := prefix.AppendBit(1)
	oldHash := mustSubtree(w.t, oldTree, prefix)
	newHash := mustSubtree(w.t, newTree, prefix)
	sibHash := mustSubtree(w.t, oldTree, child1)
	auth := signAuth(w.t, w.keys, epoch, prefix, OpMerge, newHash,
		nonce(byte(epoch), byte(prefix.Len()), 0x03), w.nextSerial())
	return Migration{
		Epoch:         epoch,
		Prefix:        prefix,
		Op:            OpMerge,
		OldHash:       oldHash,
		NewHash:       newHash,
		Auth:          auth,
		SiblingPrefix: child1,
		SiblingHash:   sibHash,
	}
}

func (w *world) logsBetween(from, to uint32) []*MigrationLog {
	out := make([]*MigrationLog, 0, to-from)
	for e := from + 1; e <= to; e++ {
		out = append(out, w.logs[e])
	}
	return out
}

// TestEndToEndSplitMergeRevoke exercises the full honest lifecycle over four
// epochs of the 6-bit fully expanded reference tree.
func TestEndToEndSplitMergeRevoke(t *testing.T) {
	w := newWorld(t)

	// Epoch 1: two honest leaves under prefix 010 and one under 11.
	lA := mustPrefix(t, "010001")
	lB := mustPrefix(t, "010010")
	lC := mustPrefix(t, "110001")
	t1 := referenceTree(t, 1,
		Entry{lA, leaf("pk-A", false, 0)},
		Entry{lB, leaf("pk-B", false, 0)},
		Entry{lC, leaf("pk-C", false, 0)},
	)
	w.ingest(1, t1, &MigrationLog{Epoch: 1})

	// Epoch 2: revoke the whole 010 subtree (migrate out), counter +1.
	p010 := mustPrefix(t, "010")
	w.counter = 1
	rev := w.revokeMigration(2, p010, t1)
	t2 := referenceTree(t, 2,
		Entry{lC, leaf("pk-C", false, 0)},
	)
	w.ingest(2, t2, &MigrationLog{Epoch: 2, Entries: []Migration{rev}})

	// Epoch 3: split at 010 with a brand new subtree and fresh authorization.
	lD := mustPrefix(t, "010001")
	lE := mustPrefix(t, "010111")
	t3 := referenceTree(t, 3,
		Entry{lC, leaf("pk-C", false, 0)},
		Entry{lD, leaf("pk-D-new", false, 1)},
		Entry{lE, leaf("pk-E-new", false, 1)},
	)
	split := w.splitMigration(3, p010, t2, t3)
	w.ingest(3, t3, &MigrationLog{Epoch: 3, Entries: []Migration{split}})

	// Epoch 4: merge 110 with its sibling 111 at prefix 11.
	p110 := mustPrefix(t, "110")
	t4 := referenceTree(t, 4,
		Entry{lD, leaf("pk-D-new", false, 1)},
		Entry{lE, leaf("pk-E-new", false, 1)},
		Entry{lC, leaf("pk-C", false, 1)},
	)
	merge := w.mergeMigration(4, p110, t3, t4)
	w.ingest(4, t4, &MigrationLog{Epoch: 4, Entries: []Migration{merge}})

	if w.verif.Locked() {
		t.Fatalf("honest chain locked: %v", w.verif.LockReason())
	}

	// Adjacent and non-adjacent append-only consistency.
	for _, pair := range [][2]uint32{{1, 2}, {1, 4}, {2, 3}, {3, 4}} {
		proof, err := BuildConsistencyProof(w.trees[pair[0]], w.trees[pair[1]], w.logsBetween(pair[0], pair[1]))
		if err != nil {
			t.Fatalf("build proof %v: %v", pair, err)
		}
		if err := w.verif.VerifyConsistency(proof); err != nil {
			t.Fatalf("verify proof %v: %v", pair, err)
		}
	}

	chain := []*SignedRoot{w.roots[1], w.roots[2], w.roots[3], w.roots[4]}
	if err := VerifyRootChain(chain, w.keys.dirPub); err != nil {
		t.Fatalf("root chain: %v", err)
	}
}
