package btree_test

import (
	"fmt"
	"testing"

	"btree"
)

// TestRootSplitOldSnapshots verifies that snapshots taken before root
// splits remain fully defined: the file is append-only, so an old
// (rootID, node map) pair still describes a complete, consistent tree no
// matter how many commits (and root splits) happen afterwards.
func TestRootSplitOldSnapshots(t *testing.T) {
	dir := tempDir(t)
	db, err := btree.Open(dir, &btree.Options{PageSize: 128})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	type mark struct {
		snap *btree.Snapshot
		want []kv
	}
	var marks []mark
	md := newModel()
	for i := 0; i < 400; i++ {
		k := fmt.Sprintf("s-%04d", i)
		v := fmt.Sprintf("v-%d", i)
		if err := db.Insert([]byte(k), []byte(v)); err != nil {
			t.Fatal(err)
		}
		md.put(k, v)
		if i%10 == 0 {
			marks = append(marks, mark{mustSnapshot(t, db), md.sorted()})
		}
	}

	// Sanity: root splits really happened.
	h, err := mustSnapshot(t, db).Height()
	if err != nil {
		t.Fatal(err)
	}
	if h < 3 {
		t.Fatalf("expected height >= 3 (multiple root splits), got %d", h)
	}
	if marks[0].snap.Gen() >= db.Gen() {
		t.Fatal("generations did not advance")
	}

	// Every historical snapshot still scans exactly its own state and
	// passes structural checks.
	for j, mk := range marks {
		if err := mk.snap.Check(); err != nil {
			t.Fatalf("snapshot %d (gen %d) check: %v", j, mk.snap.Gen(), err)
		}
		got := scanAll(t, mk.snap)
		if !equalKVs(got, mk.want) {
			t.Fatalf("snapshot %d (gen %d): got %d entries, want %d — old snapshot corrupted by later commits",
				j, mk.snap.Gen(), len(got), len(mk.want))
		}
	}
	t.Logf("verified %d historical snapshots across root splits (final height %d)", len(marks), h)
}
