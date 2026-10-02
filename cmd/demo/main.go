package main

import (
	"bytes"
	"fmt"
	"os"

	"mpt"
)

func main() {
	tr := mpt.NewTrie()

	// Keys sharing long common prefixes — the trie compresses them into
	// extension nodes automatically.
	entries := []struct{ k, v string }{
		{"account.balance", "1000"},
		{"account.nonce", "7"},
		{"account.storage.code", "0x60806040"},
		{"account.storage.root", "0x56e81f17"},
		{"block.number", "19283746"},
		{"block.timestamp", "1759420800"},
	}
	for _, e := range entries {
		tr.Put(e.k, []byte(e.v))
	}

	fmt.Println("=== Trie structure (paths shown as hex nibbles) ===")
	tr.Dump(os.Stdout)
	fmt.Printf("\nRoot hash: %x\n\n", tr.RootHash())

	// --- prefix search -------------------------------------------------------
	fmt.Println("=== Prefix search: \"account.storage.\" ===")
	for k, v := range tr.GetByPrefix("account.storage.") {
		fmt.Printf("  %s = %s\n", k, v)
	}
	fmt.Println()

	// --- membership proof ----------------------------------------------------
	const target = "account.balance"
	proof, ok := tr.Prove(target)
	fmt.Printf("=== Merkle proof for %q (exists=%v, %d nodes root->leaf) ===\n", target, ok, len(proof))
	for i, s := range proof {
		fmt.Printf("  [%d] %-13s hash=%x\n", i, mpt.KindOf(s.Node), s.Hash[:16])
	}
	val, err := mpt.VerifyProof(tr.RootHash(), target, proof)
	if err != nil {
		fmt.Println("  verify FAILED:", err)
		os.Exit(1)
	}
	fmt.Printf("  verify OK -> %q = %s\n\n", target, val)

	// --- tamper detection ----------------------------------------------------
	fmt.Println("=== Tamper test: corrupt a parent node's hash in the proof ===")
	tampered := make([]mpt.ProofStep, len(proof))
	copy(tampered, proof)
	fake := bytes.Clone(proof[1].Hash)
	fake[0] ^= 0xff // flip bits in a parent hash
	tampered[1].Hash = fake
	if _, err := mpt.VerifyProof(tr.RootHash(), target, tampered); err != nil {
		fmt.Println("  detected:", err)
	} else {
		fmt.Println("  ERROR: tampering went undetected!")
		os.Exit(1)
	}

	fmt.Println("\n=== Root hash cascade: update one value ===")
	fmt.Printf("  before: %x\n", tr.RootHash()[:16])
	tr.Put("account.balance", []byte("999"))
	fmt.Printf("  after : %x\n", tr.RootHash()[:16])
}
