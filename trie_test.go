package mpt

import (
	"bytes"
	"testing"
)

func buildSampleTrie(t *testing.T) *Trie {
	t.Helper()
	tr := NewTrie()
	entries := map[string]string{
		"account.balance":      "1000",
		"account.nonce":        "7",
		"account.storage.code": "0x60806040",
		"account.storage.root": "0x56e81f17",
		"block.number":         "19283746",
		"block.timestamp":      "1759420800",
	}
	for k, v := range entries {
		tr.Put(k, []byte(v))
	}
	return tr
}

func TestInsertAndGet(t *testing.T) {
	tr := buildSampleTrie(t)
	cases := map[string]string{
		"account.balance":      "1000",
		"account.nonce":        "7",
		"account.storage.code": "0x60806040",
		"account.storage.root": "0x56e81f17",
		"block.number":         "19283746",
		"block.timestamp":      "1759420800",
	}
	for k, want := range cases {
		got, ok := tr.Get(k)
		if !ok {
			t.Fatalf("Get(%q): key not found", k)
		}
		if string(got) != want {
			t.Fatalf("Get(%q) = %q, want %q", k, got, want)
		}
	}
	for _, missing := range []string{"", "account", "account.balancf", "account.balance.extra", "block", "zzz"} {
		if _, ok := tr.Get(missing); ok {
			t.Fatalf("Get(%q) should not exist", missing)
		}
	}
}

// Shared prefixes must collapse into extension nodes, and the path from the
// root to any leaf must contain no unary branch chains.
func TestPathCompression(t *testing.T) {
	tr := NewTrie()
	tr.Put("account.balance", []byte("1000"))
	tr.Put("account.nonce", []byte("7"))

	// Root must be an extension covering the whole shared prefix
	// "account." (16 nibbles) — not a chain of branches.
	ext, ok := tr.root.(*ExtensionNode)
	if !ok {
		t.Fatalf("root is %T, want *ExtensionNode (shared prefix not compressed)", tr.root)
	}
	if got := nibblesToKey(ext.Path); got != "account." {
		t.Fatalf("extension path decodes to %q, want %q", got, "account.")
	}
	if _, ok := ext.Child.(*BranchNode); !ok {
		t.Fatalf("extension child is %T, want *BranchNode", ext.Child)
	}
}

// Inserting a key that diverges mid-extension must split the extension.
func TestExtensionSplit(t *testing.T) {
	tr := NewTrie()
	tr.Put("account.balance", []byte("1000"))
	tr.Put("account.nonce", []byte("7"))
	tr.Put("block.number", []byte("1")) // diverges inside the root extension

	// 'a'(0x61) and 'b'(0x62) share only the high nibble 0x6, so the root
	// extension must shrink to that single nibble above a branch.
	ext, ok := tr.root.(*ExtensionNode)
	if !ok {
		t.Fatalf("root is %T, want *ExtensionNode", tr.root)
	}
	if len(ext.Path) != 1 || ext.Path[0] != 0x6 {
		t.Fatalf("root extension path = %x, want [6]", ext.Path)
	}
	if _, ok := ext.Child.(*BranchNode); !ok {
		t.Fatalf("root extension child is %T, want *BranchNode", ext.Child)
	}
	for k, want := range map[string]string{
		"account.balance": "1000",
		"account.nonce":   "7",
		"block.number":    "1",
	} {
		got, ok := tr.Get(k)
		if !ok || string(got) != want {
			t.Fatalf("Get(%q) = %q,%v want %q", k, got, ok, want)
		}
	}
}

func TestPrefixSearch(t *testing.T) {
	tr := buildSampleTrie(t)
	got := tr.GetByPrefix("account.storage.")
	want := map[string]string{
		"account.storage.code": "0x60806040",
		"account.storage.root": "0x56e81f17",
	}
	if len(got) != len(want) {
		t.Fatalf("prefix search returned %d entries, want %d: %v", len(got), len(want), got)
	}
	for k, v := range want {
		if string(got[k]) != v {
			t.Fatalf("prefix search[%q] = %q, want %q", k, got[k], v)
		}
	}
	if all := tr.GetByPrefix(""); len(all) != 6 {
		t.Fatalf("empty prefix should return all 6 keys, got %d", len(all))
	}
	if none := tr.GetByPrefix("transaction."); len(none) != 0 {
		t.Fatalf("non-matching prefix returned %v", none)
	}
}

// Any mutation must cascade to the root hash; identical content must give
// an identical root hash regardless of insertion order.
func TestRootHashCascadeAndDeterminism(t *testing.T) {
	tr := buildSampleTrie(t)
	h1 := tr.RootHash()

	tr.Put("account.balance", []byte("999"))
	h2 := tr.RootHash()
	if bytes.Equal(h1, h2) {
		t.Fatal("root hash did not change after updating a value")
	}

	tr.Put("account.balance", []byte("1000"))
	if !bytes.Equal(tr.RootHash(), h1) {
		t.Fatal("root hash did not return to original after reverting value")
	}

	// determinism under different insertion order
	a, b := NewTrie(), NewTrie()
	a.Put("alpha", []byte("1"))
	a.Put("alphabet", []byte("2"))
	a.Put("beta", []byte("3"))
	b.Put("beta", []byte("3"))
	b.Put("alphabet", []byte("2"))
	b.Put("alpha", []byte("1"))
	if !bytes.Equal(a.RootHash(), b.RootHash()) {
		t.Fatal("root hash depends on insertion order")
	}
}

func TestProofForAccountBalance(t *testing.T) {
	tr := buildSampleTrie(t)
	proof, ok := tr.Prove("account.balance")
	if !ok {
		t.Fatal("Prove: key not found")
	}
	if len(proof) < 3 {
		t.Fatalf("proof has %d steps, expected root->...->leaf chain", len(proof))
	}
	// first step is the root, last step is a leaf
	if proof[0].Hash == nil || !bytes.Equal(proof[0].Hash, tr.RootHash()) {
		t.Fatal("proof does not start at the trie root")
	}
	if _, ok := proof[len(proof)-1].Node.(*LeafNode); !ok {
		t.Fatalf("proof should end at a leaf, got %T", proof[len(proof)-1].Node)
	}
	val, err := VerifyProof(tr.RootHash(), "account.balance", proof)
	if err != nil {
		t.Fatalf("VerifyProof: %v", err)
	}
	if string(val) != "1000" {
		t.Fatalf("verified value = %q, want %q", val, "1000")
	}
}

// Tampering with any parent hash in the proof must be detected.
func TestProofTamperedParentHashDetected(t *testing.T) {
	tr := buildSampleTrie(t)
	proof, ok := tr.Prove("account.balance")
	if !ok {
		t.Fatal("Prove: key not found")
	}
	for i := 0; i < len(proof)-1; i++ { // every parent step
		tampered := make([]ProofStep, len(proof))
		copy(tampered, proof)
		fake := bytes.Clone(proof[i].Hash)
		fake[0] ^= 0xff
		tampered[i].Hash = fake
		if _, err := VerifyProof(tr.RootHash(), "account.balance", tampered); err == nil {
			t.Fatalf("tampering parent hash at step %d was not detected", i)
		}
	}
}

// Tampering with the leaf value must be detected (hash mismatch cascades).
func TestProofTamperedValueDetected(t *testing.T) {
	tr := buildSampleTrie(t)
	proof, _ := tr.Prove("account.balance")
	tampered := make([]ProofStep, len(proof))
	copy(tampered, proof)
	leaf := proof[len(proof)-1].Node.(*LeafNode)
	tampered[len(proof)-1].Node = &LeafNode{Path: leaf.Path, Value: []byte("999999")}
	if _, err := VerifyProof(tr.RootHash(), "account.balance", tampered); err == nil {
		t.Fatal("tampered leaf value was not detected")
	}
}

// A proof verified against a foreign root hash must fail (forged dictionary).
func TestProofRejectsForeignRoot(t *testing.T) {
	good := buildSampleTrie(t)
	evil := buildSampleTrie(t)
	evil.Put("account.balance", []byte("0")) // attacker rewrites the balance

	proof, _ := evil.Prove("account.balance")
	if _, err := VerifyProof(good.RootHash(), "account.balance", proof); err == nil {
		t.Fatal("proof from forged trie verified against the honest root hash")
	}
}

func TestNonMembershipProofFails(t *testing.T) {
	tr := buildSampleTrie(t)
	proof, ok := tr.Prove("account.missing")
	if ok {
		t.Fatal("Prove reported existence for a missing key")
	}
	if _, err := VerifyProof(tr.RootHash(), "account.missing", proof); err == nil {
		t.Fatal("VerifyProof succeeded for a missing key")
	}
}
