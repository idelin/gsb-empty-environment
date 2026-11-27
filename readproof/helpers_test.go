package readproof

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"testing"
)

type keyPair struct {
	sign   Ed25519Signer
	verify Ed25519Verifier
}

func testKeys(t *testing.T) keyPair {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	return keyPair{sign: Ed25519Signer(priv), verify: Ed25519Verifier(pub)}
}

// blockData fills block index with deterministic, version-independent
// content of exactly BlockSize bytes.
func blockData(index uint64) []byte {
	data := make([]byte, BlockSize)
	for i := range data {
		data[i] = byte((uint64(i) + index*7919) % 251)
	}
	return data
}

// fileData assembles full blocks 0..full-1 plus an optional partial tail.
func fileData(fullBlocks int, tailLen int) []byte {
	total := fullBlocks*BlockSize + tailLen
	data := make([]byte, 0, total)
	for i := 0; i < fullBlocks; i++ {
		data = append(data, blockData(uint64(i))...)
	}
	if tailLen > 0 {
		tail := make([]byte, tailLen)
		for i := range tail {
			tail[i] = byte(100 + i%100)
		}
		data = append(data, tail...)
	}
	return data
}

// changedBlock returns an altered full block for index, distinct from the
// reference block content.
func changedBlock(index uint64) []byte {
	data := blockData(index)
	for i := range data {
		data[i] ^= 0x5a
	}
	return data
}

func mustProof(t *testing.T, tree *Tree, ranges []Range) *Proof {
	t.Helper()
	proof, err := tree.BuildProof(ranges)
	if err != nil {
		t.Fatalf("BuildProof: %v", err)
	}
	return proof
}

func assertSameProof(t *testing.T, got, want *Proof) {
	t.Helper()
	if len(got.Leaves) != len(want.Leaves) || len(got.Nodes) != len(want.Nodes) || len(got.Ranges) != len(want.Ranges) {
		t.Fatalf("proof shape mismatch: got %d/%d/%d want %d/%d/%d",
			len(got.Ranges), len(got.Leaves), len(got.Nodes),
			len(want.Ranges), len(want.Leaves), len(want.Nodes))
	}
	for i := range want.Leaves {
		if !sameNode(got.Leaves[i], want.Leaves[i]) {
			t.Fatalf("leaf %d mismatch", i)
		}
	}
	for i := range want.Nodes {
		if !sameNode(got.Nodes[i], want.Nodes[i]) {
			t.Fatalf("node %d mismatch", i)
		}
	}
	for i := range want.Ranges {
		if got.Ranges[i] != want.Ranges[i] {
			t.Fatalf("range %d mismatch", i)
		}
	}
}

func sameNode(a, b Node) bool {
	return a.Start == b.Start && a.End == b.End && a.Hash == b.Hash && bytes.Equal(a.Data, b.Data)
}
