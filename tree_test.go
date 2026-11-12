package netsec

import (
	"bytes"
	"errors"
	"fmt"
	"sync"
	"testing"
)

func leafBytes(size int, seed byte) []byte {
	b := make([]byte, size)
	for i := range b {
		b[i] = seed + byte(i)
	}
	return b
}

func mustOpen(t *testing.T, cfg Config) *Tree {
	t.Helper()
	tr, err := Open(cfg)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	return tr
}

func mustCommit(t *testing.T, tx *Tx) Digest {
	t.Helper()
	d, err := tx.Commit()
	if err != nil {
		t.Fatalf("Commit: %v", err)
	}
	return d
}

// flakyStore fails the Put that arrives after failAfter successful puts.
type flakyStore struct {
	inner     *MemoryStore
	failAfter int
	puts      int
}

func (f *flakyStore) Put(d Digest, blob []byte) error {
	f.puts++
	if f.failAfter >= 0 && f.puts > f.failAfter {
		return errors.New("injected write failure")
	}
	return f.inner.Put(d, blob)
}

// TestCommitWriteFailuresKeepOldSnapshot drives successive write failures:
// the commit is retried while the store fails on its 1st, 2nd, ... write.
// Every failed attempt must leave the root and every old snapshot untouched.
func TestCommitWriteFailuresKeepOldSnapshot(t *testing.T) {
	inner := NewMemoryStore()
	store := &flakyStore{inner: inner, failAfter: -1}
	tr := mustOpen(t, Config{Fanout: 3, LeafSize: 8, NumLeaves: 9, Store: store})
	snap := tr.Snapshot()
	oldRoot := snap.Root()

	stage := func() *Tx {
		tx := tr.Begin()
		for _, idx := range []int{0, 5, 8} {
			if err := tx.Set(idx, leafBytes(8, 0xA0+byte(idx)*0x10)); err != nil {
				t.Fatalf("Set: %v", err)
			}
		}
		return tx
	}

	// Fail on the 1st, 2nd, ... write in turn. The commit must abort each
	// time without moving the root.
	succeeded := false
	for failAfter := 0; !succeeded; failAfter++ {
		if failAfter > 64 {
			t.Fatal("commit never succeeded; failure injection stuck")
		}
		store.failAfter = failAfter
		store.puts = 0
		_, err := stage().Commit()
		if err == nil {
			succeeded = true
			continue
		}
		if got := tr.Root(); got != oldRoot {
			t.Fatalf("failAfter=%d: root moved on failed commit", failAfter)
		}
		for i := 0; i < snap.NumLeaves(); i++ {
			got, err := snap.Read(i)
			if err != nil {
				t.Fatalf("failAfter=%d: old snapshot read %d: %v", failAfter, i, err)
			}
			if !bytes.Equal(got, make([]byte, 8)) {
				t.Fatalf("failAfter=%d: old snapshot leaf %d changed", failAfter, i)
			}
		}
	}

	// The successful commit exposes the new version under a new root...
	if got := tr.Root(); got == oldRoot {
		t.Fatal("root did not change after successful commit")
	}
	for _, idx := range []int{0, 5, 8} {
		got, err := tr.Read(idx)
		if err != nil {
			t.Fatalf("read %d: %v", idx, err)
		}
		if !bytes.Equal(got, leafBytes(8, 0xA0+byte(idx)*0x10)) {
			t.Fatalf("leaf %d: unexpected data %x", idx, got)
		}
	}
	// ...while the old snapshot still authenticates the all-zero version.
	if snap.Root() != oldRoot {
		t.Fatal("snapshot root moved")
	}
	got, err := snap.Read(0)
	if err != nil {
		t.Fatalf("old snapshot read: %v", err)
	}
	if !bytes.Equal(got, make([]byte, 8)) {
		t.Fatal("old snapshot leaf changed after commit")
	}
}

// TestOddLeafCounts builds trees whose levels end in partial nodes and
// verifies proofs for every leaf, before and after updates.
func TestOddLeafCounts(t *testing.T) {
	for _, n := range []int{1, 3, 5, 7, 11, 13} {
		t.Run(fmt.Sprintf("leaves=%d", n), func(t *testing.T) {
			tr := mustOpen(t, Config{Fanout: 3, LeafSize: 4, NumLeaves: n})
			tx := tr.Begin()
			for i := 0; i < n; i++ {
				if err := tx.Set(i, leafBytes(4, byte(i))); err != nil {
					t.Fatalf("Set %d: %v", i, err)
				}
			}
			root := mustCommit(t, tx)
			if root != tr.Root() {
				t.Fatal("committed root != tree root")
			}
			for i := 0; i < n; i++ {
				p, err := tr.Prove(i)
				if err != nil {
					t.Fatalf("Prove %d: %v", i, err)
				}
				want := leafBytes(4, byte(i))
				if !VerifyProof(root, want, p) {
					t.Fatalf("proof for leaf %d does not verify", i)
				}
				got, err := tr.Read(i)
				if err != nil {
					t.Fatalf("Read %d: %v", i, err)
				}
				if !bytes.Equal(got, want) {
					t.Fatalf("leaf %d: got %x want %x", i, got, want)
				}
			}
			// Update the lone leaf on the odd right edge.
			tx = tr.Begin()
			if err := tx.Set(n-1, leafBytes(4, 0xFF)); err != nil {
				t.Fatalf("Set last: %v", err)
			}
			root = mustCommit(t, tx)
			p, err := tr.Prove(n - 1)
			if err != nil {
				t.Fatalf("Prove last: %v", err)
			}
			if !VerifyProof(root, leafBytes(4, 0xFF), p) {
				t.Fatal("proof for updated last leaf does not verify")
			}
		})
	}
}

// TestSharedAncestors updates non-adjacent leaves that share ancestors and
// checks that untouched subtrees are shared between the old and new roots
// while the old snapshot stays intact.
func TestSharedAncestors(t *testing.T) {
	tr := mustOpen(t, Config{Fanout: 4, LeafSize: 4, NumLeaves: 16})
	before := tr.Snapshot()

	tx := tr.Begin()
	// Leaves 1 and 2 share their parent; leaf 14 only shares the root.
	for _, i := range []int{1, 2, 14} {
		if err := tx.Set(i, leafBytes(4, byte(0x10+i))); err != nil {
			t.Fatalf("Set %d: %v", i, err)
		}
	}
	mustCommit(t, tx)
	after := tr.Snapshot()

	if before.Root() == after.Root() {
		t.Fatal("root did not change")
	}
	// Untouched subtrees (leaves 4..7 and 8..11) must be shared by pointer.
	oldKids := before.root.children
	newKids := after.root.children
	if oldKids[1] != newKids[1] || oldKids[2] != newKids[2] {
		t.Fatal("untouched subtrees not shared between roots")
	}
	if oldKids[0] == newKids[0] || oldKids[3] == newKids[3] {
		t.Fatal("touched subtrees were not rebuilt")
	}
	// Within the rebuilt parent of leaves 0..3, untouched leaves are shared.
	if oldKids[0].children[0] != newKids[0].children[0] ||
		oldKids[0].children[3] != newKids[0].children[3] {
		t.Fatal("untouched leaves under a rebuilt parent not shared")
	}

	// Old snapshot still serves the old bytes and verifies against old root.
	for i := 0; i < 16; i++ {
		got, err := before.Read(i)
		if err != nil {
			t.Fatalf("old snapshot read %d: %v", i, err)
		}
		if !bytes.Equal(got, make([]byte, 4)) {
			t.Fatalf("old snapshot leaf %d changed", i)
		}
	}
	// New snapshot serves the staged writes.
	for _, i := range []int{1, 2, 14} {
		got, err := after.Read(i)
		if err != nil {
			t.Fatalf("new snapshot read %d: %v", i, err)
		}
		if !bytes.Equal(got, leafBytes(4, byte(0x10+i))) {
			t.Fatalf("new snapshot leaf %d: got %x", i, got)
		}
	}
}

// TestPathProofs checks that valid proofs verify and that tampering with the
// leaf, a sibling, or the root is detected.
func TestPathProofs(t *testing.T) {
	tr := mustOpen(t, Config{Fanout: 2, LeafSize: 16, NumLeaves: 8})
	tx := tr.Begin()
	for i := 0; i < 8; i++ {
		if err := tx.Set(i, leafBytes(16, byte(i*7))); err != nil {
			t.Fatalf("Set %d: %v", i, err)
		}
	}
	root := mustCommit(t, tx)

	for i := 0; i < 8; i++ {
		p, err := tr.Prove(i)
		if err != nil {
			t.Fatalf("Prove %d: %v", i, err)
		}
		good := leafBytes(16, byte(i*7))
		if !VerifyProof(root, good, p) {
			t.Fatalf("valid proof for leaf %d rejected", i)
		}
		if VerifyProof(root, leafBytes(16, 0xEE), p) {
			t.Fatalf("leaf %d: proof accepted wrong data", i)
		}
		bad := Proof{Siblings: make([][]Digest, len(p.Siblings)), Position: p.Position}
		for l := range p.Siblings {
			bad.Siblings[l] = append([]Digest(nil), p.Siblings[l]...)
		}
		bad.Siblings[0][0][0] ^= 0xFF
		if VerifyProof(root, good, bad) {
			t.Fatalf("leaf %d: proof accepted tampered sibling", i)
		}
		var other Digest
		other[0] = 0xFF
		if VerifyProof(other, good, p) {
			t.Fatalf("leaf %d: proof verified against wrong root", i)
		}
	}
}

// TestOldSnapshotInvarianceAcrossCommits commits several versions and reads
// every historical snapshot afterwards.
func TestOldSnapshotInvarianceAcrossCommits(t *testing.T) {
	tr := mustOpen(t, Config{Fanout: 3, LeafSize: 4, NumLeaves: 10})
	snaps := []Snapshot{tr.Snapshot()}
	want := make([][]byte, 10)
	for i := range want {
		want[i] = make([]byte, 4)
	}
	history := [][][]byte{append([][]byte(nil), want...)}

	for v := 1; v <= 4; v++ {
		tx := tr.Begin()
		for _, i := range []int{v, 9 - v} {
			data := leafBytes(4, byte(v*16+i))
			if err := tx.Set(i, data); err != nil {
				t.Fatalf("Set %d: %v", i, err)
			}
			want[i] = data
		}
		mustCommit(t, tx)
		snaps = append(snaps, tr.Snapshot())
		history = append(history, append([][]byte(nil), want...))
	}

	for v, snap := range snaps {
		for i := 0; i < 10; i++ {
			got, err := snap.Read(i)
			if err != nil {
				t.Fatalf("version %d leaf %d: %v", v, i, err)
			}
			if !bytes.Equal(got, history[v][i]) {
				t.Fatalf("version %d leaf %d: got %x want %x", v, i, got, history[v][i])
			}
		}
	}
}

// TestValidation rejects bad shapes and bad writes.
func TestValidation(t *testing.T) {
	if _, err := Open(Config{Fanout: 1, LeafSize: 4, NumLeaves: 4}); err == nil {
		t.Fatal("fanout 1 accepted")
	}
	if _, err := Open(Config{Fanout: 2, LeafSize: 0, NumLeaves: 4}); err == nil {
		t.Fatal("leaf size 0 accepted")
	}
	if _, err := Open(Config{Fanout: 2, LeafSize: 4, NumLeaves: 0}); err == nil {
		t.Fatal("zero leaves accepted")
	}
	tr := mustOpen(t, Config{Fanout: 2, LeafSize: 4, NumLeaves: 4})
	tx := tr.Begin()
	if err := tx.Set(4, leafBytes(4, 1)); !errors.Is(err, ErrIndexOutOfRange) {
		t.Fatalf("out-of-range Set: %v", err)
	}
	if err := tx.Set(0, []byte{1, 2}); !errors.Is(err, ErrLeafSize) {
		t.Fatalf("short Set: %v", err)
	}
	if _, err := tr.Read(-1); !errors.Is(err, ErrIndexOutOfRange) {
		t.Fatalf("negative Read: %v", err)
	}
	mustCommit(t, tx)
	if err := tx.Set(0, leafBytes(4, 1)); !errors.Is(err, ErrTxClosed) {
		t.Fatalf("Set after Commit: %v", err)
	}
}

// TestConcurrentReadersAndWriter runs readers on pinned snapshots while a
// writer commits; run with -race.
func TestConcurrentReadersAndWriter(t *testing.T) {
	tr := mustOpen(t, Config{Fanout: 4, LeafSize: 8, NumLeaves: 32})
	var wg sync.WaitGroup
	stop := make(chan struct{})
	for r := 0; r < 4; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				snap := tr.Snapshot()
				for i := 0; i < snap.NumLeaves(); i++ {
					if _, err := snap.Read(i); err != nil {
						t.Errorf("read %d: %v", i, err)
						return
					}
				}
			}
		}()
	}
	for v := 0; v < 50; v++ {
		tx := tr.Begin()
		if err := tx.Set(v%32, leafBytes(8, byte(v))); err != nil {
			t.Fatalf("Set: %v", err)
		}
		mustCommit(t, tx)
	}
	close(stop)
	wg.Wait()
}
