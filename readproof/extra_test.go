package readproof

import (
	"sync"
	"testing"
)

func TestStaleSubtreeAfterTruncation(t *testing.T) {
	_, keys, data, manifests := publishFourVersions(t)

	// A v2 proof that only carries blocks beyond the v3 truncation cannot
	// supply surviving v3 material; requesting the re-extended region after
	// v4 without changed leaves must be rejected.
	oldProof := mustProof(t, BuildTree(data[2]), []Range{{Start: 0, End: 5}})
	// Block indices 5 and 6 do not exist in the v3 (oldProof) tree at all.
	_, err := UpdateProof(
		manifests[2], manifests[3], keys.verify, oldProof,
		nil,
		[]Range{{Start: 5, End: 7}},
	)
	if err != ErrMissingLeafData && err != ErrStaleSubtree {
		t.Fatalf("got %v, want ErrMissingLeafData or ErrStaleSubtree", err)
	}
}

func TestHandleGenerationMismatch(t *testing.T) {
	archive, keys, _, manifests := publishFourVersions(t)
	if _, err := archive.Open(manifests[3], keys.verify, NewMemoryNodeCache()); err != nil {
		t.Fatalf("valid open failed: %v", err)
	}

	unknown := *manifests[3]
	unknown.Version = 99
	if _, err := archive.Open(&unknown, keys.verify, NewMemoryNodeCache()); err != ErrHandleMismatch {
		t.Fatalf("unknown generation: got %v, want ErrHandleMismatch", err)
	}

	foreign := *manifests[3]
	foreign.Root[0] ^= 0xff
	if _, err := archive.Open(&foreign, keys.verify, NewMemoryNodeCache()); err != ErrProofMismatch {
		t.Fatalf("got %v, want ErrProofMismatch", err)
	}
}

func TestMemoryNodeCacheConcurrent(t *testing.T) {
	cache := NewMemoryNodeCache()
	var root [32]byte
	root[0] = 7
	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			node := CachedNode{Start: uint64(i), End: uint64(i) + 1, Version: 1}
			cache.Put(root, node)
			_, _ = cache.Get(root, uint64(i), uint64(i)+1)
		}(i)
	}
	wg.Wait()
	for i := 0; i < 32; i++ {
		if _, ok := cache.Get(root, uint64(i), uint64(i)+1); !ok {
			t.Fatalf("missing cached node %d", i)
		}
	}
}

func TestPublishRejectsBrokenChain(t *testing.T) {
	keys := testKeys(t)
	archive := NewArchive("chain", keys.sign)
	data := fileData(2, 0)
	if _, err := archive.Publish(data, 1, [32]byte{}); err != ErrManifestChain {
		t.Fatalf("first version with non-empty previous root: got %v", err)
	}
	m1, err := archive.Publish(data, 1, EmptyRoot)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := archive.Publish(data, 3, m1.Root); err != ErrManifestChain {
		t.Fatalf("skipped version: got %v", err)
	}
}

func TestDuplicateChangedLeafRejected(t *testing.T) {
	_, keys, data, manifests := publishFourVersions(t)
	oldProof := mustProof(t, BuildTree(data[0]), []Range{{Start: 0, End: 7}})
	changed := append(changedLeavesFor(data[1], 2), changedLeavesFor(data[1], 2)...)
	if _, err := UpdateProof(
		manifests[0], manifests[1], keys.verify, oldProof,
		changed, []Range{{Start: 2, End: 3}},
	); err != ErrBadChangedLeaf {
		t.Fatalf("got %v, want ErrBadChangedLeaf", err)
	}
}
