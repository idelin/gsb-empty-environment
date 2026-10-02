package erasureproof

import (
	"bytes"
	"fmt"
	"testing"
)

func newTestProver(t *testing.T, records map[string]string) (*Prover, map[string][]byte) {
	t.Helper()
	prover, err := NewProverFromSeed([]byte("test-seed-0001"))
	if err != nil {
		t.Fatalf("NewProverFromSeed: %v", err)
	}
	data := make(map[string][]byte, len(records))
	for id, payload := range records {
		buf := []byte(payload)
		data[id] = buf
		if err := prover.Add(id, buf); err != nil {
			t.Fatalf("Add(%s): %v", id, err)
		}
	}
	return prover, data
}

func proveAll(t *testing.T, p *Prover, root Digest, ids ...string) {
	t.Helper()
	for _, id := range ids {
		ip, err := p.Prove(id)
		if err != nil {
			t.Fatalf("Prove(%s): %v", id, err)
		}
		if !VerifyInclusion(root, ip) {
			t.Fatalf("inclusion proof for %s does not verify", id)
		}
	}
}

func TestInclusionProofsVerify(t *testing.T) {
	prover, _ := newTestProver(t, map[string]string{
		"cust-1": `{"name":"Ann","id_no":"A1"}`,
		"cust-2": `{"name":"Ben","id_no":"B2"}`,
		"cust-3": `{"name":"Cid","id_no":"C3"}`,
	})
	proveAll(t, prover, prover.Root(), "cust-1", "cust-2", "cust-3")
}

// Acceptance boundary: after a customer is erased, the root evolves and
// every remaining customer's proof still verifies against the new root.
func TestEraseKeepsOtherProofsValid(t *testing.T) {
	ids := []string{"cust-1", "cust-2", "cust-3", "cust-4", "cust-5"}
	records := map[string]string{}
	for i, id := range ids {
		records[id] = fmt.Sprintf(`{"name":"User%d","id_no":"ID-%d"}`, i, i)
	}
	prover, _ := newTestProver(t, records)
	oldRoot := prover.Root()

	tr, err := prover.Erase("cust-3")
	if err != nil {
		t.Fatalf("Erase: %v", err)
	}
	if tr.OldRoot != oldRoot {
		t.Fatal("transition old root mismatch")
	}
	newRoot := prover.Root()
	if tr.NewRoot != newRoot {
		t.Fatal("transition new root mismatch")
	}
	if newRoot == oldRoot {
		t.Fatal("root must evolve after erasure")
	}

	proveAll(t, prover, newRoot, "cust-1", "cust-2", "cust-4", "cust-5")
}

func TestErasureProofVerifies(t *testing.T) {
	prover, _ := newTestProver(t, map[string]string{
		"cust-1": `{"name":"Ann"}`,
		"cust-2": `{"name":"Ben"}`,
	})
	if _, err := prover.Erase("cust-1"); err != nil {
		t.Fatalf("Erase: %v", err)
	}
	ep, err := prover.ErasureProof("cust-1")
	if err != nil {
		t.Fatalf("ErasureProof: %v", err)
	}
	root := prover.Root()
	if !VerifyErasure(prover.PublicKey(), root, ep) {
		t.Fatal("valid erasure proof rejected")
	}

	// Wrong record id: marker hash no longer matches the path.
	bad := *ep
	bad.RecordID = "cust-2"
	if VerifyErasure(prover.PublicKey(), root, &bad) {
		t.Fatal("erasure proof verified under wrong record id")
	}

	// Wrong authority key: deletion signature must not verify.
	other, err := NewProverFromSeed([]byte("another-authority"))
	if err != nil {
		t.Fatal(err)
	}
	if VerifyErasure(other.PublicKey(), root, ep) {
		t.Fatal("erasure proof verified under wrong authority key")
	}

	// Stale root: marker is not in the old tree.
	if VerifyErasure(prover.PublicKey(), Digest{}, ep) {
		t.Fatal("erasure proof verified against wrong root")
	}
}

func TestPhysicalErasure(t *testing.T) {
	prover, data := newTestProver(t, map[string]string{
		"cust-1": `{"name":"Ann","id_no":"A1","phone":"138"}`,
		"cust-2": `{"name":"Ben"}`,
	})
	original := data["cust-1"]

	if _, err := prover.Erase("cust-1"); err != nil {
		t.Fatalf("Erase: %v", err)
	}

	// The prover's only copy was zeroed in place.
	if !bytes.Equal(original, make([]byte, len(original))) {
		t.Fatal("personal data was not physically zeroed")
	}
	// The record is no longer retrievable or provable.
	if _, err := prover.Record("cust-1"); err != ErrRecordErased {
		t.Fatalf("Record after erase: got %v, want ErrRecordErased", err)
	}
	if _, err := prover.Prove("cust-1"); err != ErrRecordErased {
		t.Fatalf("Prove after erase: got %v, want ErrRecordErased", err)
	}
	// The erased personal data no longer proves against the new root.
	stale := &InclusionProof{RecordID: "cust-1", Data: []byte(`{"name":"Ann","id_no":"A1","phone":"138"}`)}
	ep, err := prover.ErasureProof("cust-1")
	if err != nil {
		t.Fatal(err)
	}
	stale.Proof = ep.Proof
	if VerifyInclusion(prover.Root(), stale) {
		t.Fatal("erased personal data still verifies against the new root")
	}
}

func TestRootTransitionChain(t *testing.T) {
	prover, _ := newTestProver(t, map[string]string{
		"cust-1": `{"name":"Ann"}`,
		"cust-2": `{"name":"Ben"}`,
		"cust-3": `{"name":"Cid"}`,
		"cust-4": `{"name":"Dee"}`,
	})
	for _, id := range []string{"cust-2", "cust-4"} {
		if _, err := prover.Erase(id); err != nil {
			t.Fatalf("Erase(%s): %v", id, err)
		}
	}
	history := prover.History()
	if len(history) != 2 {
		t.Fatalf("history length = %d, want 2", len(history))
	}
	for i := range history {
		if !VerifyTransition(prover.PublicKey(), &history[i]) {
			t.Fatalf("transition %d does not verify", i)
		}
		if i+1 < len(history) && history[i].NewRoot != history[i+1].OldRoot {
			t.Fatal("root chain is broken between transitions")
		}
	}
	if history[len(history)-1].NewRoot != prover.Root() {
		t.Fatal("last transition does not lead to the current root")
	}

	// A tampered transition (wrong leaf position) must be rejected.
	tampered := history[0]
	tampered.MarkerProof.Index++
	if VerifyTransition(prover.PublicKey(), &tampered) {
		t.Fatal("tampered transition verified")
	}

	// Remaining customers still fully provable.
	proveAll(t, prover, prover.Root(), "cust-1", "cust-3")
}

func TestTamperedInclusionProofFails(t *testing.T) {
	prover, _ := newTestProver(t, map[string]string{
		"cust-1": `{"name":"Ann"}`,
		"cust-2": `{"name":"Ben"}`,
	})
	ip, err := prover.Prove("cust-1")
	if err != nil {
		t.Fatal(err)
	}
	root := prover.Root()

	corrupt := *ip
	corrupt.Data = []byte(`{"name":"Mallory"}`)
	if VerifyInclusion(root, &corrupt) {
		t.Fatal("proof verified for tampered data")
	}

	if len(ip.Proof.Siblings) > 0 {
		corrupt = *ip
		corrupt.Proof.Siblings = append([]Digest{}, ip.Proof.Siblings...)
		corrupt.Proof.Siblings[0][0] ^= 0xff
		if VerifyInclusion(root, &corrupt) {
			t.Fatal("proof verified with corrupted sibling")
		}
	}

	corrupt = *ip
	corrupt.Proof.Index++
	if VerifyInclusion(root, &corrupt) {
		t.Fatal("proof verified at wrong index")
	}
}

func TestErrorPaths(t *testing.T) {
	prover, _ := newTestProver(t, map[string]string{"cust-1": `{"name":"Ann"}`})

	if err := prover.Add("cust-1", []byte("dup")); err != ErrDuplicateRecord {
		t.Fatalf("duplicate Add: got %v", err)
	}
	if _, err := prover.Erase("ghost"); err != ErrRecordNotFound {
		t.Fatalf("Erase unknown: got %v", err)
	}
	if _, err := prover.Prove("ghost"); err != ErrRecordNotFound {
		t.Fatalf("Prove unknown: got %v", err)
	}
	if _, err := prover.Erase("cust-1"); err != nil {
		t.Fatal(err)
	}
	if _, err := prover.Erase("cust-1"); err != ErrRecordErased {
		t.Fatalf("double erase: got %v", err)
	}
	if _, err := prover.ErasureProof("ghost"); err != ErrRecordNotFound {
		t.Fatalf("ErasureProof unknown: got %v", err)
	}
}

func TestMerkleTreePaddingAndProofBoundaries(t *testing.T) {
	// Non-power-of-two leaf counts exercise deterministic padding.
	for n := 1; n <= 9; n++ {
		leaves := make([]Digest, n)
		for i := range leaves {
			leaves[i] = LeafHash([]byte(fmt.Sprintf("leaf-%d", i)))
		}
		tree := NewTree(leaves)
		root := tree.Root()
		for i := range leaves {
			proof, err := tree.Prove(i)
			if err != nil {
				t.Fatalf("n=%d Prove(%d): %v", n, i, err)
			}
			if !proof.Verify(leaves[i], root) {
				t.Fatalf("n=%d proof %d invalid", n, i)
			}
		}
		if _, err := tree.Prove(tree.NumLeaves()); err != ErrIndexOutOfRange {
			t.Fatalf("n=%d out-of-range prove: got %v", n, err)
		}
	}
}
