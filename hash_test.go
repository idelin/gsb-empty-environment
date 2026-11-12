package netsec_test

import (
	"crypto/sha256"
	"fmt"
	"testing"

	netsec "example.org/netsec/q0500"
)

func TestRFC9162HashRules(t *testing.T) {
	data := []byte("hello")

	prefixLeaf := append([]byte{0x00}, data...)
	wantLeaf := netsec.Hash(sha256.Sum256(prefixLeaf))
	if got := netsec.LeafHash(data); got != wantLeaf {
		t.Fatalf("LeafHash = %s, want %s", got, wantLeaf)
	}

	left := netsec.LeafHash([]byte("left"))
	right := netsec.LeafHash([]byte("right"))
	prefixNode := []byte{0x01}
	prefixNode = append(prefixNode, left[:]...)
	prefixNode = append(prefixNode, right[:]...)
	wantNode := netsec.Hash(sha256.Sum256(prefixNode))
	if got := netsec.NodeHash(left, right); got != wantNode {
		t.Fatalf("NodeHash = %s, want %s", got, wantNode)
	}

	if got, want := netsec.EmptyRoot, netsec.Hash(sha256.Sum256(nil)); got != want {
		t.Fatalf("EmptyRoot = %s, want %s", got, want)
	}

	leaf0 := netsec.LeafHash([]byte("a"))
	leaf1 := netsec.LeafHash([]byte("b"))
	leaf2 := netsec.LeafHash([]byte("c"))
	cases := []struct {
		name   string
		leaves [][]byte
		want   netsec.Hash
	}{
		{name: "empty", leaves: nil, want: netsec.EmptyRoot},
		{name: "one", leaves: [][]byte{[]byte("a")}, want: leaf0},
		{name: "two", leaves: [][]byte{[]byte("a"), []byte("b")}, want: netsec.NodeHash(leaf0, leaf1)},
		{
			name:   "three",
			leaves: [][]byte{[]byte("a"), []byte("b"), []byte("c")},
			want:   netsec.NodeHash(netsec.NodeHash(leaf0, leaf1), leaf2),
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := netsec.MerkleRoot(tc.leaves); got != tc.want {
				t.Fatalf("MerkleRoot = %s, want %s", got, tc.want)
			}
		})
	}
}

func TestInclusionProofsFoldToRootForManyTreeShapes(t *testing.T) {
	for n := 1; n <= 40; n++ {
		blocks := leaves("shape", n)
		root := netsec.MerkleRoot(blocks)

		for index := 0; index < n; index++ {
			proof, err := netsec.ProveInclusion(blocks, uint64(index))
			if err != nil {
				t.Fatalf("n=%d index=%d prove: %v", n, index, err)
			}
			if !netsec.VerifyInclusion(root, uint64(n), uint64(index), blocks[index], proof) {
				t.Fatalf("n=%d index=%d valid proof rejected", n, index)
			}

			if !netsec.VerifyInclusion(root, uint64(n), uint64(index), blocks[index], append([]netsec.Hash(nil), proof...)) {
				t.Fatalf("n=%d index=%d proof failed after defensive copy", n, index)
			}

			// Wrong body, wrong index, extra proof element, truncated proof,
			// and a tampered sibling must all fail.
			if netsec.VerifyInclusion(root, uint64(n), uint64(index), []byte("wrong"), proof) {
				t.Fatalf("n=%d index=%d accepted wrong body", n, index)
			}
			if n > 1 && netsec.VerifyInclusion(root, uint64(n), uint64((index+1)%n), blocks[index], proof) {
				t.Fatalf("n=%d index=%d accepted proof at another index", n, index)
			}
			if netsec.VerifyInclusion(root, uint64(n), uint64(index), blocks[index], append(proof, netsec.Hash{0xaa})) {
				t.Fatalf("n=%d index=%d accepted extra proof element", n, index)
			}
			if len(proof) > 0 {
				truncated := append([]netsec.Hash(nil), proof[:len(proof)-1]...)
				if netsec.VerifyInclusion(root, uint64(n), uint64(index), blocks[index], truncated) {
					t.Fatalf("n=%d index=%d accepted truncated proof", n, index)
				}
				tampered := append([]netsec.Hash(nil), proof...)
				tampered[len(tampered)-1][0] ^= 0x80
				if netsec.VerifyInclusion(root, uint64(n), uint64(index), blocks[index], tampered) {
					t.Fatalf("n=%d index=%d accepted tampered proof", n, index)
				}
			}
		}
	}
}

func TestParseAndStringHash(t *testing.T) {
	blocks := leaves("parse", 1)
	h := netsec.MerkleRoot(blocks)
	parsed, err := netsec.ParseHash(h.String())
	if err != nil {
		t.Fatalf("ParseHash: %v", err)
	}
	if parsed != h {
		t.Fatalf("round trip = %s, want %s", parsed, h)
	}
	if _, err := netsec.ParseHash("xyz"); err == nil {
		t.Fatal("ParseHash accepted an invalid digest")
	}
	_ = fmt.Sprintf("%s", h)
}
