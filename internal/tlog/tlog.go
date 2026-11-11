// Package tlog implements the Merkle tree math used by a transparent
// append-only log, following RFC 6962 and the conventions of
// golang.org/x/mod/sumdb/tlog:
//
//   - leaf records are hashed as SHA-256(0x00 || record),
//   - interior nodes are hashed as SHA-256(0x01 || left || right),
//   - consistency proofs bind a later tree head to an earlier one.
//
// The consistency proof generator and verifier are direct ports of the
// recursive routines in the sumdb tlog package, re-expressed over an
// in-memory leaf-hash slice. The package depends only on the standard library
// so it can be verified and used fully offline.
package tlog

import (
	"crypto/sha256"
	"errors"
)

// Hash is the 32-byte label of a tree node or leaf record.
type Hash [sha256.Size]byte

var (
	leafHashPrefix = []byte{0x00}
	nodeHashPrefix = []byte{0x01}
)

// RecordHash returns the label of a leaf containing record.
func RecordHash(record []byte) Hash {
	h := sha256.New()
	h.Write(leafHashPrefix)
	h.Write(record)
	var out Hash
	h.Sum(out[:0])
	return out
}

// NodeHash returns the label of an interior node with the given children.
func NodeHash(left, right Hash) Hash {
	h := sha256.New()
	h.Write(nodeHashPrefix)
	h.Write(left[:])
	h.Write(right[:])
	var out Hash
	h.Sum(out[:0])
	return out
}

// maxPow2 returns the largest power of two strictly smaller than n. For n<=1
// it returns 0. This mirrors tlog.maxpow2 (note the strict inequality).
func maxPow2(n int64) int64 {
	k := int64(1)
	for k*2 < n {
		k *= 2
	}
	if k >= n {
		return 0
	}
	return k
}

// subtreeHash returns the root of the perfect subtree spanning leaves
// [start, start+size), where size must be a power of two.
func subtreeHash(leaves []Hash, start, size int64) Hash {
	if size == 1 {
		return leaves[start]
	}
	half := size / 2
	return NodeHash(
		subtreeHash(leaves, start, half),
		subtreeHash(leaves, start+half, half),
	)
}

// subTreeHashLeaf computes the root of leaves[lo:hi] by repeatedly splitting
// the range into the largest aligned perfect subtree that fits, then folding
// the subtree roots left-to-right. It is a port of tlog.subTreeHash.
func subTreeHashLeaf(leaves []Hash, lo, hi int64) Hash {
	var roots []Hash
	for lo < hi {
		k := maxPow2(hi - lo + 1)
		roots = append(roots, subtreeHash(leaves, lo, k))
		lo += k
	}
	h := roots[len(roots)-1]
	for i := len(roots) - 2; i >= 0; i-- {
		h = NodeHash(roots[i], h)
	}
	return h
}

// TreeHash computes the Merkle root of the given leaf hashes using a single
// popcount pass. An empty leaf slice yields the zero hash.
func TreeHash(leaves []Hash) Hash {
	if len(leaves) == 0 {
		return Hash{}
	}
	return subTreeHashLeaf(leaves, 0, int64(len(leaves)))
}

// ConsistencyProof returns a proof that the tree of size newSize extends the
// tree of size oldSize. leaves must contain every leaf hash from index 0 up to
// newSize-1. An empty proof is returned when oldSize == newSize.
func ConsistencyProof(leaves []Hash, oldSize, newSize int64) ([]Hash, error) {
	if oldSize < 1 || newSize < 1 || oldSize > newSize {
		return nil, errors.New("tlog: invalid inputs in ConsistencyProof")
	}
	if int64(len(leaves)) < newSize {
		return nil, errors.New("tlog: not enough leaf hashes")
	}
	if oldSize == newSize {
		return nil, nil
	}
	return treeProofLeaf(leaves, 0, newSize, oldSize), nil
}

// treeProofLeaf is a port of tlog.treeProof over an in-memory leaf slice.
func treeProofLeaf(leaves []Hash, lo, hi, oldSize int64) []Hash {
	if oldSize == hi {
		if lo == 0 {
			// This subtree is exactly the old tree; the verifier already holds
			// its root, so the hash need not be included.
			return nil
		}
		return []Hash{subTreeHashLeaf(leaves, lo, hi)}
	}

	k := maxPow2(hi - lo)
	var p []Hash
	var th Hash
	if oldSize <= lo+k {
		// The old/new boundary lies in the left subtree.
		p = treeProofLeaf(leaves, lo, lo+k, oldSize)
		th = subTreeHashLeaf(leaves, lo+k, hi)
	} else {
		// The boundary lies in the right subtree.
		th = subTreeHashLeaf(leaves, lo, lo+k)
		p = treeProofLeaf(leaves, lo+k, hi, oldSize)
	}
	return append(p, th)
}

// CheckConsistency verifies that proof demonstrates the tree with root newRoot
// and size newSize is an append-only extension of the tree with root oldRoot
// and size oldSize. It is a port of tlog.CheckTree/runTreeProof.
func CheckConsistency(oldSize, newSize int64, oldRoot, newRoot Hash, proof []Hash) error {
	if oldSize < 1 || newSize < 1 || oldSize > newSize {
		return errors.New("tlog: invalid inputs in CheckConsistency")
	}
	if oldSize == newSize {
		if oldRoot != newRoot {
			return errors.New("tlog: identical sizes but different roots")
		}
		return nil
	}

	oldGot, newGot, err := runTreeProofLeaf(proof, 0, newSize, oldSize, oldRoot)
	if err != nil {
		return err
	}
	if oldGot != oldRoot || newGot != newRoot {
		return errors.New("tlog: proof failed")
	}
	return nil
}

// runTreeProofLeaf is a port of tlog.runTreeProof. It reconstructs both the
// old and new tree roots from the proof and the trusted old root.
func runTreeProofLeaf(p []Hash, lo, hi, oldSize int64, old Hash) (oldRoot, newRoot Hash, err error) {
	if oldSize == hi {
		if lo == 0 {
			if len(p) != 0 {
				return Hash{}, Hash{}, errors.New("tlog: proof failed")
			}
			return old, old, nil
		}
		if len(p) != 1 {
			return Hash{}, Hash{}, errors.New("tlog: proof failed")
		}
		return p[0], p[0], nil
	}

	if len(p) == 0 {
		return Hash{}, Hash{}, errors.New("tlog: proof failed")
	}

	k := maxPow2(hi - lo)
	last := p[len(p)-1]
	if oldSize <= lo+k {
		oh, th, rerr := runTreeProofLeaf(p[:len(p)-1], lo, lo+k, oldSize, old)
		if rerr != nil {
			return Hash{}, Hash{}, rerr
		}
		return oh, NodeHash(th, last), nil
	}
	oh, th, rerr := runTreeProofLeaf(p[:len(p)-1], lo+k, hi, oldSize, old)
	if rerr != nil {
		return Hash{}, Hash{}, rerr
	}
	return NodeHash(last, oh), NodeHash(last, th), nil
}
