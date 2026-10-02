package erasable

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"testing"
)

func newVaultWithRecords(t *testing.T, n int) (*Vault, [][]byte) {
	t.Helper()
	v, err := NewVault()
	if err != nil {
		t.Fatal(err)
	}
	data := make([][]byte, n)
	for i := 0; i < n; i++ {
		data[i] = []byte{
			'A', 'l', 'i', 'c', 'e', byte('0' + i), // fake PII payload
			0xDE, 0xAD, byte(i), 0xBE, 0xEF,
		}
		id := string([]byte{'u', 's', 'e', 'r', '-', byte('0' + i)})
		if _, err := v.Insert(id, data[i]); err != nil {
			t.Fatal(err)
		}
	}
	return v, data
}

func TestEraseFlow(t *testing.T) {
	v, data := newVaultWithRecords(t, 5)
	rootBefore := v.Root()

	// Snapshot proofs of all records before erasure.
	type snapshot struct {
		proof *Proof
		leaf  []byte
	}
	snaps := make([]snapshot, 5)
	for i := 0; i < 5; i++ {
		p, leaf, err := v.ProveRecord(i)
		if err != nil {
			t.Fatal(err)
		}
		snaps[i] = snapshot{p, leaf}
		if !p.Verify(leaf, rootBefore) {
			t.Fatalf("record %d proof invalid before erasure", i)
		}
	}

	// Customer user-2 exercises the right to be forgotten.
	marker, err := v.EraseByID("user-2")
	if err != nil {
		t.Fatal(err)
	}
	rootAfter := v.Root()
	if bytes.Equal(rootBefore, rootAfter) {
		t.Fatal("root must evolve after erasure")
	}

	// 1. Plaintext is physically gone from the vault.
	if got := v.Get(2); got != nil {
		t.Fatal("erased record must not be retrievable")
	}
	// 2. The original backing array was overwritten with zeros.
	for i, b := range data[2] {
		if b != 0 {
			t.Fatalf("plaintext byte %d not zeroed: %#x", i, b)
		}
	}
	// 3. Untouched records are untouched.
	for _, i := range []int{0, 1, 3, 4} {
		if !bytes.Equal(v.Get(i), data[i]) {
			t.Fatalf("record %d corrupted by erasure of record 2", i)
		}
	}
	// 4. The marker is committed in the tree and verifiable.
	ep, em, err := v.ProveErasure(2)
	if err != nil {
		t.Fatal(err)
	}
	if em != marker {
		t.Fatal("marker mismatch")
	}
	if !VerifyErasure(ep, em, rootAfter, v.PublicKey()) {
		t.Fatal("erasure proof must verify against new root")
	}
	leaf, _ := v.Tree().Leaf(2)
	if !bytes.Equal(leaf, marker.Hash()) {
		t.Fatal("tree leaf must equal Erase_Marker hash")
	}
	// 5. All remaining customers' proofs still verify against the new root.
	for _, i := range []int{0, 1, 3, 4} {
		p, leaf, err := v.ProveRecord(i)
		if err != nil {
			t.Fatal(err)
		}
		if !p.Verify(leaf, rootAfter) {
			t.Fatalf("surviving record %d proof invalid after erasure", i)
		}
	}
	// 6. Double erasure is rejected.
	if _, err := v.Erase(2); err != ErrAlreadyErased {
		t.Fatalf("expected ErrAlreadyErased, got %v", err)
	}
}

func TestErasureProofRejectsForgery(t *testing.T) {
	v, _ := newVaultWithRecords(t, 4)
	if _, err := v.Erase(1); err != nil {
		t.Fatal(err)
	}
	root := v.Root()
	ep, em, err := v.ProveErasure(1)
	if err != nil {
		t.Fatal(err)
	}

	// Wrong public key.
	priv2, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	pub2 := &priv2.PublicKey
	if VerifyErasure(ep, em, root, pub2) {
		t.Fatal("erasure proof must fail under a foreign key")
	}
	// Forged signature.
	forged := &EraseMarker{Index: em.Index, OriginalLeafHash: em.OriginalLeafHash, Signature: append([]byte(nil), em.Signature...)}
	forged.Signature[0] ^= 0xFF
	if VerifyErasure(ep, forged, root, v.PublicKey()) {
		t.Fatal("forged signature must not verify")
	}
	// Marker claiming a different index.
	wrongIdx := &EraseMarker{Index: 2, OriginalLeafHash: em.OriginalLeafHash, Signature: em.Signature}
	if VerifyErasure(ep, wrongIdx, root, v.PublicKey()) {
		t.Fatal("marker at wrong index must not verify")
	}
	// Stale root (before erasure) must not validate the marker proof.
	v2, _ := newVaultWithRecords(t, 4)
	if VerifyErasure(ep, em, v2.Root(), v.PublicKey()) {
		t.Fatal("marker proof must not verify against a tree without the marker")
	}
}

func TestMultipleErasuresKeepTreeConsistent(t *testing.T) {
	v, _ := newVaultWithRecords(t, 8)
	for _, i := range []int{0, 3, 7} {
		if _, err := v.Erase(i); err != nil {
			t.Fatal(err)
		}
	}
	root := v.Root()
	for i := 0; i < 8; i++ {
		switch i {
		case 0, 3, 7:
			p, m, err := v.ProveErasure(i)
			if err != nil {
				t.Fatal(err)
			}
			if !VerifyErasure(p, m, root, v.PublicKey()) {
				t.Fatalf("erasure proof %d invalid", i)
			}
		default:
			p, leaf, err := v.ProveRecord(i)
			if err != nil {
				t.Fatal(err)
			}
			if !p.Verify(leaf, root) {
				t.Fatalf("live record %d proof invalid", i)
			}
		}
	}
}

func TestEraseUnknownAndDuplicate(t *testing.T) {
	v, _ := newVaultWithRecords(t, 1)
	if _, err := v.EraseByID("ghost"); err != ErrNotFound {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
	if _, err := v.Insert("user-0", []byte("dup")); err == nil {
		t.Fatal("duplicate id must be rejected")
	}
}
