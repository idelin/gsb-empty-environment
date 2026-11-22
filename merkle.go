package disclosure

import (
	"fmt"
	"math/bits"
)

func merkleRoot(hashes []Hash) Hash {
	if len(hashes) == 0 {
		return Hash{}
	}
	return merkleRootRange(hashes, 0, len(hashes))
}

func merkleRootRange(hashes []Hash, lo, hi int) Hash {
	n := hi - lo
	if n == 1 {
		return treeLeafHash(hashes[lo])
	}
	split := lo + largestPowerOfTwoLessThan(n)
	return nodeHash(
		merkleRootRange(hashes, lo, split),
		merkleRootRange(hashes, split, hi),
	)
}

func largestPowerOfTwoLessThan(n int) int {
	if n <= 1 {
		return 1
	}
	return 1 << (bits.Len(uint(n-1)) - 1)
}

func largestPowerOfTwoLessThan64(n uint64) uint64 {
	if n <= 1 {
		return 1
	}
	return uint64(1) << (bits.Len64(n-1) - 1)
}

func merkleProof(hashes []Hash, pos int) (Hash, []Hash) {
	return merkleProofRange(hashes, pos, 0, len(hashes))
}

func merkleProofRange(hashes []Hash, pos, lo, hi int) (Hash, []Hash) {
	n := hi - lo
	if n == 1 {
		return treeLeafHash(hashes[lo]), nil
	}
	split := lo + largestPowerOfTwoLessThan(n)
	if pos < split {
		root, path := merkleProofRange(hashes, pos, lo, split)
		sibling := merkleRootRange(hashes, split, hi)
		return nodeHash(root, sibling), append(path, sibling)
	}
	root, path := merkleProofRange(hashes, pos, split, hi)
	sibling := merkleRootRange(hashes, lo, split)
	return nodeHash(sibling, root), append(path, sibling)
}

func merkleRootFromProof(pos, size uint64, leafHash Hash, path []Hash) (Hash, error) {
	if size == 0 || pos >= size {
		return Hash{}, fmt.Errorf("%w: position %d outside tree of size %d", ErrInvalidProof, pos, size)
	}
	next := 0
	root, err := reconstructRoot(pos, 0, size, treeLeafHash(leafHash), path, &next)
	if err != nil {
		return Hash{}, err
	}
	if next != len(path) {
		return Hash{}, fmt.Errorf("%w: unused audit path elements", ErrInvalidProof)
	}
	return root, nil
}

func reconstructRoot(pos, lo, hi uint64, node Hash, path []Hash, next *int) (Hash, error) {
	if hi-lo == 1 {
		if pos != lo {
			return Hash{}, fmt.Errorf("%w: leaf position mismatch", ErrInvalidProof)
		}
		return node, nil
	}
	split := lo + largestPowerOfTwoLessThan64(hi-lo)
	if pos < split {
		left, err := reconstructRoot(pos, lo, split, node, path, next)
		if err != nil {
			return Hash{}, err
		}
		if *next >= len(path) {
			return Hash{}, fmt.Errorf("%w: audit path too short", ErrInvalidProof)
		}
		right := path[*next]
		(*next)++
		return nodeHash(left, right), nil
	}
	right, err := reconstructRoot(pos, split, hi, node, path, next)
	if err != nil {
		return Hash{}, err
	}
	if *next >= len(path) {
		return Hash{}, fmt.Errorf("%w: audit path too short", ErrInvalidProof)
	}
	left := path[*next]
	(*next)++
	return nodeHash(left, right), nil
}
