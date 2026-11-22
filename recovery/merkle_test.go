package recovery

import "testing"

func TestLogInclusionProofs(t *testing.T) {
	for n := 1; n <= 257; n++ {
		leaves := make([]Hash, n)
		for i := range leaves {
			leaves[i] = domainHash("test-leaf", u64Bytes(uint64(i)))
		}
		root := treeRoot("mth-leaf/directory/v1", leaves)
		for idx := 0; idx < n; idx++ {
			p := InclusionProof{
				Index:    uint64(idx),
				Size:     uint64(n),
				RootHash: root,
				Hashes:   inclusionPath(leaves, idx),
			}
			if err := verifyInclusion(leaves[idx], p, root); err != nil {
				t.Fatalf("n=%d idx=%d: %v", n, idx, err)
			}
			// 篡改任一兄弟节点都必须失败。
			if len(p.Hashes) > 0 {
				bad := append(Hash(nil), p.Hashes[0]...)
				bad[0] ^= 0xFF
				p.Hashes[0] = bad
				if err := verifyInclusion(leaves[idx], p, root); err == nil {
					t.Fatalf("n=%d idx=%d: tampered proof accepted", n, idx)
				}
			}
		}
	}
}

func TestPrekeyInclusionProofs(t *testing.T) {
	device := DeviceID("dev-1")
	for n := 1; n <= 70; n++ {
		commitments := make([]Hash, n)
		for i := range commitments {
			commitments[i] = domainHash("commitment", u64Bytes(uint64(i)))
		}
		pt := buildPrekeyTree(device, 3, commitments)
		root := pt.root()
		for idx := 0; idx < n; idx++ {
			pp := pt.proof(uint64(idx))
			if err := verifyPrekeyInclusion(pp, root); err != nil {
				t.Fatalf("n=%d idx=%d: %v", n, idx, err)
			}
		}
		// 错误的树根必须拒绝。
		wrongRoot := domainHash("wrong-root")
		if err := verifyPrekeyInclusion(pt.proof(0), wrongRoot); err == nil {
			t.Fatalf("n=%d: wrong prekey root accepted", n)
		}
	}
}

func TestSparseProofs(t *testing.T) {
	entries := make(map[string]Hash)
	keys := []Hash{
		domainHash("k1"), domainHash("k2"), domainHash("k3"),
	}
	for i, k := range keys {
		entries[string(k)] = domainHash("v", u64Bytes(uint64(i)))
	}
	root := smtRoot(entries)

	for _, k := range keys {
		p := smtProof(entries, k)
		got, err := verifySparseProof(p)
		if err != nil || !equalHash(got, root) {
			t.Fatalf("inclusion root mismatch: %v", err)
		}
		if !p.Exists {
			t.Fatalf("expected inclusion proof")
		}
	}

	missing := domainHash("missing")
	np := smtProof(entries, missing)
	np.Value = smtEmptyHash
	np.Exists = false
	got, err := verifySparseProof(np)
	if err != nil || !equalHash(got, root) {
		t.Fatalf("non-inclusion root mismatch: %v", err)
	}

	// 非包含证明却声称存在，必然重算出不同的根。
	np.Exists = true
	if got, err = verifySparseProof(np); err != nil || equalHash(got, root) {
		t.Fatalf("forged sparse proof accepted")
	}
}
