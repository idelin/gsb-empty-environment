package sigverify

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"testing"
)

// refMTH is an independent, textbook RFC 6962 Merkle tree hash used only as a
// test oracle. It recurses over raw leaf data, unlike the production verifier
// which only consumes an already-computed leaf hash and a supplied path.
func refMTH(leaves [][]byte) []byte {
	switch len(leaves) {
	case 0:
		h := sha256.New()
		return h.Sum(nil)
	case 1:
		return LeafHash(leaves[0])
	default:
		n := len(leaves)
		var k int
		if n&(n-1) == 0 {
			k = n / 2
		} else {
			k = 1 << (bitLenUint(uint64(n)) - 1)
		}
		left := refMTH(leaves[:k])
		right := refMTH(leaves[k:])
		combined, err := nodeHash(left, right)
		if err != nil {
			panic(err)
		}
		return combined
	}
}

// subtreeHash returns the RFC 6962 hash of the contiguous subtree
// leaves[start:start+width], where width is a power of two.
func subtreeHash(leaves [][]byte, start, width int) []byte {
	if width == 1 {
		return LeafHash(leaves[start])
	}
	half := width / 2
	h, err := nodeHash(subtreeHash(leaves, start, half), subtreeHash(leaves, start+half, half))
	if err != nil {
		panic(err)
	}
	return h
}

// refInclusionPath independently derives the inclusion path for leaf m. It
// walks the RFC 6962 balanced-tree structure directly, and is deliberately
// written differently from the production side schedule so the two
// implementations cross-check each other.
func refInclusionPath(leaves [][]byte, m int) [][]byte {
	// Independent test oracle built as an explicit level-by-level fold,
	// matching RFC 6962's actual pairing (the odd last node is promoted rather
	// than merged). At each level we locate the block containing m and record
	// its neighbor block; the neighbor's precomputed subtree hash is emitted.
	var path [][]byte

	// Precompute every level's block hashes; block b at level L covers
	// 2^L leaves. The last block may be a promoted (unpaired) node.
	levelHashes := [][][]byte{{}}
	for _, leaf := range leaves {
		levelHashes[0] = append(levelHashes[0], LeafHash(leaf))
	}
	for {
		current := levelHashes[len(levelHashes)-1]
		if len(current) == 1 {
			break
		}
		var next [][]byte
		for i := 0; i < len(current); i += 2 {
			if i+1 == len(current) {
				next = append(next, current[i]) // promote odd node
				continue
			}
			combined, err := nodeHash(current[i], current[i+1])
			if err != nil {
				panic(err)
			}
			next = append(next, combined)
		}
		levelHashes = append(levelHashes, next)
	}

	index := m
	for level := 0; level < len(levelHashes)-1; level++ {
		if index%2 == 0 {
			neighbor := index + 1
			if neighbor < len(levelHashes[level]) {
				path = append(path, levelHashes[level][neighbor]) // right sibling
			}
		} else {
			path = append(path, levelHashes[level][index-1]) // left sibling
		}
		index /= 2
	}
	return path
}

func bitLenUint(n uint64) uint {
	var length uint
	for n > 0 {
		length++
		n >>= 1
	}
	return length
}

func TestInclusionProofAcrossAllTreeShapes(t *testing.T) {
	for n := 1; n <= 64; n++ {
		leaves := make([][]byte, n)
		for i := range leaves {
			leaves[i] = []byte(fmt.Sprintf("canonical-entry-%d", i))
		}
		root := refMTH(leaves)

		for m := 0; m < n; m++ {
			path := refInclusionPath(leaves, m)
			leaf := LeafHash(leaves[m])
			if err := VerifyInclusion(uint64(m), uint64(n), leaf, root, path); err != nil {
				t.Fatalf("n=%d m=%d: valid proof rejected: %v", n, m, err)
			}

			// A proof for a different leaf must never validate.
			other := (m + 1) % n
			if other != m {
				wrongLeaf := LeafHash(leaves[other])
				if err := VerifyInclusion(uint64(m), uint64(n), wrongLeaf, root, path); err == nil {
					t.Fatalf("n=%d m=%d: foreign leaf accepted", n, m)
				}
			}
		}
	}
}

func TestInclusionProofRejectsTampering(t *testing.T) {
	const n, m = 13, 7
	leaves := make([][]byte, n)
	for i := range leaves {
		leaves[i] = []byte(fmt.Sprintf("entry-%d", i))
	}
	root := refMTH(leaves)
	path := refInclusionPath(leaves, m)
	leaf := LeafHash(leaves[m])

	t.Run("index out of range", func(t *testing.T) {
		if !errors.Is(VerifyInclusion(uint64(n), uint64(n), leaf, root, path), ErrProofIndex) {
			t.Fatal("expected ErrProofIndex")
		}
		if !errors.Is(VerifyInclusion(0, 0, leaf, root, path), ErrProofIndex) {
			t.Fatal("expected ErrProofIndex for zero tree")
		}
	})

	t.Run("missing node", func(t *testing.T) {
		err := VerifyInclusion(m, n, leaf, root, path[:len(path)-1])
		if !errors.Is(err, ErrProofLength) {
			t.Fatalf("expected ErrProofLength, got %v", err)
		}
	})

	t.Run("extra node", func(t *testing.T) {
		extra := append(append([][]byte{}, path...), make([]byte, sha256.Size))
		err := VerifyInclusion(m, n, leaf, root, extra)
		if !errors.Is(err, ErrProofLength) {
			t.Fatalf("expected ErrProofLength, got %v", err)
		}
	})

	t.Run("forged path hash", func(t *testing.T) {
		forged := append([][]byte{}, path...)
		forged[0] = flipByte(append([]byte(nil), forged[0]...))
		err := VerifyInclusion(m, n, leaf, root, forged)
		if !errors.Is(err, ErrRootMismatch) {
			t.Fatalf("expected ErrRootMismatch, got %v", err)
		}
	})

	t.Run("wrong claimed root", func(t *testing.T) {
		wrongRoot := flipByte(append([]byte(nil), root...))
		err := VerifyInclusion(m, n, leaf, wrongRoot, path)
		if !errors.Is(err, ErrRootMismatch) {
			t.Fatalf("expected ErrRootMismatch, got %v", err)
		}
	})

	t.Run("malformed hash length", func(t *testing.T) {
		bad := append([][]byte{}, path...)
		bad[0] = bad[0][:31]
		if !errors.Is(VerifyInclusion(m, n, leaf, root, bad), ErrProofHash) {
			t.Fatal("expected ErrProofHash")
		}
	})
}

func flipByte(b []byte) []byte {
	b[0] ^= 0xFF
	return b
}
