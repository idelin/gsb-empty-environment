package integrity

import (
	"bytes"
	"fmt"
	"math/rand"
	"sync"
	"testing"
)

// Concurrent updates and proof generation must never combine into a root
// that never existed: every proof a reader obtains must verify, and its
// root must belong to the actual update history recorded by the single
// writer. The final state is checked against a full tree rebuild.
func TestConcurrentUpdatesNeverYieldForeignRoot(t *testing.T) {
	const n = 32
	blocks := makeBlocks(n, "conc")
	tree := New(blocks)

	var mu sync.Mutex
	history := map[string]bool{string(tree.Root()): true}

	var wg sync.WaitGroup
	wg.Add(1)
	go func() { // single writer: sequential, real update history
		defer wg.Done()
		for k := 0; k < 400; k++ {
			idx := uint64(k % n)
			up, err := tree.Update(idx, HashBlock([]byte(fmt.Sprintf("w-%d", k))))
			if err != nil {
				t.Errorf("Update(%d): %v", idx, err)
				return
			}
			if !VerifyUpdateProof(up) {
				t.Errorf("update proof %d does not verify", k)
				return
			}
			mu.Lock()
			history[string(up.NewRoot)] = true
			mu.Unlock()
		}
	}()

	type observation struct {
		root  []byte
		valid bool
	}
	observed := make(chan observation, 4096)
	var rwg sync.WaitGroup
	for r := 0; r < 6; r++ {
		rwg.Add(1)
		go func(seed int64) {
			defer rwg.Done()
			rnd := rand.New(rand.NewSource(seed))
			for k := 0; k < 300; k++ {
				i, j := uint64(rnd.Intn(n)), uint64(rnd.Intn(n))
				if i == j {
					j = (j + 1) % n
				}
				if i > j {
					i, j = j, i
				}
				bp, root, err := tree.ProveBatch([]uint64{i, j})
				if err != nil {
					t.Errorf("ProveBatch: %v", err)
					return
				}
				observed <- observation{root, VerifyBatchProof(root, bp)}
				p, proot, err := tree.Prove(i)
				if err != nil {
					t.Errorf("Prove: %v", err)
					return
				}
				observed <- observation{proot, VerifyProof(proot, p)}
			}
		}(int64(r))
	}
	wg.Wait()
	rwg.Wait()
	close(observed)

	count := 0
	for obs := range observed {
		count++
		if !obs.valid {
			t.Fatal("proof from a concurrent snapshot does not verify")
		}
		mu.Lock()
		known := history[string(obs.root)]
		mu.Unlock()
		if !known {
			t.Fatalf("reader observed root %x that never existed", obs.root)
		}
	}
	if count == 0 {
		t.Fatal("no observations collected")
	}

	// The surviving state must equal a full rebuild from the live blocks.
	final := make([][]byte, n)
	for i := uint64(0); i < n; i++ {
		h, present := tree.BlockHash(i)
		if !present {
			t.Fatalf("block %d unexpectedly missing", i)
		}
		final[i] = h
	}
	if !bytes.Equal(tree.Root(), rebuildRoot(final)) {
		t.Fatal("final root after concurrent updates diverges from full rebuild")
	}
}

// A proof assembled from pieces of two different snapshots (as a racing
// reader might accidentally do without snapshot isolation) must not
// verify against any root that was never a real snapshot.
func TestMixedSnapshotPiecesDoNotVerify(t *testing.T) {
	blocks := makeBlocks(8, "mix")
	tree := New(blocks)
	root0 := tree.Root()
	if _, err := tree.Update(0, HashBlock([]byte("mix-0-new"))); err != nil {
		t.Fatal(err)
	}
	if _, err := tree.Update(1, HashBlock([]byte("mix-1-new"))); err != nil {
		t.Fatal(err)
	}
	root1 := tree.Root()

	// Single proof generated under root0, checked against root1.
	stale := New(blocks)
	p, _, err := stale.Prove(1)
	if err != nil {
		t.Fatal(err)
	}
	if VerifyProof(root1, p) {
		t.Fatal("stale proof verified against post-update root")
	}
	if !VerifyProof(root0, p) {
		t.Fatal("stale proof rejected by its own root")
	}
	// Batch proof mixing a block value from after the update with the
	// frontier nodes from before it: block 0 changed on the frontier, so
	// the spliced result matches neither snapshot's root.
	bp, _, err := stale.ProveBatch([]uint64{1, 2})
	if err != nil {
		t.Fatal(err)
	}
	bp.Blocks[0] = HashBlock([]byte("mix-1-new"))
	if VerifyBatchProof(root0, bp) || VerifyBatchProof(root1, bp) {
		t.Fatal("cross-snapshot mixed batch proof verified")
	}
}
