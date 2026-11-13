package netsec

import (
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"math/rand"
	"slices"
	"sort"
	"testing"
)

var (
	refValueA = []byte{0x11, 0x22}
	refValueB = []byte{0x33, 0x44}
)

// referenceRoot is an independent full-tree implementation used only by tests.
func referenceRoot(height uint8, values map[uint64][]byte) Digest {
	var nodeAt func(depth uint8, prefix uint64) Digest
	nodeAt = func(depth uint8, prefix uint64) Digest {
		if depth == height {
			if value, ok := values[prefix]; ok {
				return LeafHash(prefix, value)
			}
			return AbsentLeafHash()
		}
		left := nodeAt(depth+1, prefix<<1)
		right := nodeAt(depth+1, prefix<<1|1)
		return InternalHash(left, right)
	}
	return nodeAt(0, 0)
}

func referenceValue(key uint64) []byte {
	if key&1 == 0 {
		return refValueA
	}
	return refValueB
}

func keysForMask(limit uint64, mask uint64) []uint64 {
	keys := make([]uint64, 0, limit)
	for key := uint64(0); key < limit; key++ {
		if mask&(uint64(1)<<key) != 0 {
			keys = append(keys, key)
		}
	}
	return keys
}

func TestEmptyRootsMatchReference(t *testing.T) {
	for height := uint8(1); height <= 6; height++ {
		root, err := EmptyRoot(height)
		if err != nil {
			t.Fatalf("EmptyRoot(%d): %v", height, err)
		}
		if want := referenceRoot(height, nil); root != want {
			t.Fatalf("height %d: empty root %s, reference %s", height, root, want)
		}
	}
}

func TestReferenceRootsExhaustiveSmallHeights(t *testing.T) {
	for height := uint8(1); height <= 4; height++ {
		limit := uint64(1) << height
		total := uint64(1) << limit
		for mask := uint64(0); mask < total; mask++ {
			values := make(map[uint64][]byte)
			for _, key := range keysForMask(limit, mask) {
				values[key] = referenceValue(key)
			}
			want := referenceRoot(height, values)
			tree, err := NewTree(height, values)
			if err != nil {
				t.Fatalf("height %d mask %d: NewTree: %v", height, mask, err)
			}
			if got := tree.Root(); got != want {
				t.Fatalf("height %d mask %d: root %s, want %s", height, mask, got, want)
			}
		}
	}

	// h=5 and h=6 cannot be enumerated in full; use deterministic and random
	// presence patterns against the same reference implementation.
	rng := rand.New(rand.NewSource(42))
	for height := uint8(5); height <= 6; height++ {
		limit := uint64(1) << height
		for iteration := 0; iteration < 300; iteration++ {
			values := make(map[uint64][]byte)
			for key := uint64(0); key < limit; key++ {
				if iteration == 0 || rng.Intn(3) != 0 {
					values[key] = referenceValue(key)
				}
			}
			want := referenceRoot(height, values)
			tree, err := NewTree(height, values)
			if err != nil {
				t.Fatalf("height %d iteration %d: NewTree: %v", height, iteration, err)
			}
			if got := tree.Root(); got != want {
				t.Fatalf("height %d iteration %d: root %s, want %s", height, iteration, got, want)
			}
		}
	}
}

func TestReferenceProofsExhaustiveQueries(t *testing.T) {
	for height := uint8(1); height <= 4; height++ {
		limit := uint64(1) << height
		values := make(map[uint64][]byte, limit)
		for key := uint64(0); key < limit; key++ {
			values[key] = referenceValue(key)
		}
		root := referenceRoot(height, values)

		querySets := uint64(1) << limit
		for mask := uint64(1); mask < querySets; mask++ {
			keys := keysForMask(limit, mask)
			proof, err := GenerateProof(height, values, keys)
			if err != nil {
				t.Fatalf("height %d mask %d: GenerateProof: %v", height, mask, err)
			}
			if err := proof.Verify(root); err != nil {
				t.Fatalf("height %d mask %d: Verify: %v", height, mask, err)
			}
		}
	}

	// Larger heights: every singleton, every adjacent pair, all keys, and
	// deterministic mixed subsets (including absent keys).
	rng := rand.New(rand.NewSource(99))
	for height := uint8(5); height <= 6; height++ {
		limit := uint64(1) << height
		values := make(map[uint64][]byte, limit/2)
		for key := uint64(0); key < limit; key++ {
			if key%3 != 1 {
				values[key] = referenceValue(key)
			}
		}
		root := referenceRoot(height, values)

		var querySets [][]uint64
		for key := uint64(0); key < limit; key++ {
			querySets = append(querySets, []uint64{key})
			if key+1 < limit {
				querySets = append(querySets, []uint64{key, key + 1})
			}
		}
		all := make([]uint64, 0, limit)
		for key := uint64(0); key < limit; key++ {
			all = append(all, key)
		}
		querySets = append(querySets, all)

		for iteration := 0; iteration < 200; iteration++ {
			keys := make([]uint64, 0, MaxQueries)
			for key := uint64(0); key < limit; key++ {
				if rng.Intn(4) == 0 {
					keys = append(keys, key)
				}
			}
			if len(keys) > 0 {
				querySets = append(querySets, keys)
			}
		}

		for index, keys := range querySets {
			proof, err := Prove(height, values, keys)
			if err != nil {
				t.Fatalf("height %d set %d: Prove: %v", height, index, err)
			}
			if err := VerifyProof(root, proof); err != nil {
				t.Fatalf("height %d set %d: Verify: %v", height, index, err)
			}
		}
	}
}

func TestServiceHeightAndBoundaryKeys(t *testing.T) {
	values := map[uint64][]byte{
		0:                  refValueA,
		math.MaxUint64:     refValueB,
		math.MaxUint64 - 1: refValueA,
		uint64(1) << 63:    refValueB,
		12345678901234567:  refValueA,
	}
	tree, err := NewTree(ServiceHeight, values)
	if err != nil {
		t.Fatal(err)
	}
	root := tree.Root()

	keys := []uint64{0, 1, 12345678901234567, uint64(1) << 63, math.MaxUint64 - 1, math.MaxUint64}
	proof, err := tree.Prove(keys)
	if err != nil {
		t.Fatal(err)
	}
	if err := proof.Verify(root); err != nil {
		t.Fatal(err)
	}

	updates := []Update{
		SetUpdate(1, refValueB),
		DeleteUpdate(uint64(1) << 63),
		SetUpdate(math.MaxUint64, nil),
	}
	newRoot, err := proof.Update(root, updates)
	if err != nil {
		t.Fatal(err)
	}

	expected := make(map[uint64][]byte)
	for key, value := range values {
		expected[key] = cloneBytes(value)
	}
	expected[1] = refValueB
	delete(expected, uint64(1)<<63)
	expected[math.MaxUint64] = nil
	expectedTree, err := NewTree(ServiceHeight, expected)
	if err != nil {
		t.Fatal(err)
	}
	expectedRoot := expectedTree.Root()
	if newRoot != expectedRoot {
		t.Fatalf("new root %s, want %s", newRoot, expectedRoot)
	}
}

func TestProofEncodingRoundTrip(t *testing.T) {
	values := map[uint64][]byte{2: refValueA, 9: refValueB, 21: nil}
	root := referenceRoot(6, values)
	proof, err := GenerateProof(6, values, []uint64{1, 2, 9, 13, 21})
	if err != nil {
		t.Fatal(err)
	}

	encoded, err := proof.MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	var decoded MultiProof
	if err := decoded.UnmarshalBinary(encoded); err != nil {
		t.Fatal(err)
	}
	if err := decoded.Verify(root); err != nil {
		t.Fatal(err)
	}

	if err := decoded.UnmarshalBinary(append(encoded, 0x00)); !errors.Is(err, ErrTrailingBytes) {
		t.Fatalf("trailing byte: got %v", err)
	}
	if err := decoded.UnmarshalBinary(encoded[:len(encoded)-1]); !errors.Is(err, ErrMalformedProof) {
		t.Fatalf("truncation: got %v", err)
	}
}

func cloneProof(proof *MultiProof) *MultiProof {
	out := &MultiProof{
		Height:  proof.Height,
		Queries: make([]ProofQuery, len(proof.Queries)),
		Sides:   make([]ProofSide, len(proof.Sides)),
	}
	for index, query := range proof.Queries {
		out.Queries[index] = ProofQuery{
			Key:     query.Key,
			Present: query.Present,
			Value:   cloneBytes(query.Value),
		}
	}
	copy(out.Sides, proof.Sides)
	return out
}

func TestTamperingCannotForgeMembership(t *testing.T) {
	values := map[uint64][]byte{
		3:  refValueA,
		8:  refValueB,
		19: refValueA,
		42: nil,
		50: refValueB,
	}
	const height = uint8(6)
	root := referenceRoot(height, values)
	queries := []uint64{1, 3, 8, 12, 19, 30, 42, 63}
	proof, err := GenerateProof(height, values, queries)
	if err != nil {
		t.Fatal(err)
	}
	if err := proof.Verify(root); err != nil {
		t.Fatalf("baseline proof: %v", err)
	}

	find := func(key uint64) int {
		for index, query := range proof.Queries {
			if query.Key == key {
				return index
			}
		}
		t.Fatalf("query %d not found", key)
		return -1
	}

	tests := []struct {
		name    string
		mutate  func(*MultiProof)
		wantErr error
	}{
		{
			name: "present value replaced",
			mutate: func(p *MultiProof) {
				p.Queries[find(3)].Value = cloneBytes(refValueB)
			},
			wantErr: ErrRootMismatch,
		},
		{
			name: "present marker cleared",
			mutate: func(p *MultiProof) {
				index := find(8)
				p.Queries[index].Present = false
				p.Queries[index].Value = nil
			},
			wantErr: ErrRootMismatch,
		},
		{
			name: "absent marker forged",
			mutate: func(p *MultiProof) {
				index := find(1)
				p.Queries[index].Present = true
				p.Queries[index].Value = cloneBytes(refValueA)
			},
			wantErr: ErrRootMismatch,
		},
		{
			name: "empty present leaf forged as absent",
			mutate: func(p *MultiProof) {
				index := find(42)
				p.Queries[index].Present = false
				p.Queries[index].Value = nil
			},
			wantErr: ErrRootMismatch,
		},
		{
			name: "query order reversed",
			mutate: func(p *MultiProof) {
				p.Queries[0], p.Queries[1] = p.Queries[1], p.Queries[0]
			},
			wantErr: ErrQueryOrder,
		},
		{
			name: "query duplicated",
			mutate: func(p *MultiProof) {
				p.Queries[1] = p.Queries[0]
			},
			wantErr: ErrQueryOrder,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			tampered := cloneProof(proof)
			test.mutate(tampered)
			err := tampered.Verify(root)
			if !errors.Is(err, test.wantErr) {
				t.Fatalf("got %v, want %v", err, test.wantErr)
			}
		})
	}

	if len(proof.Sides) == 0 {
		t.Fatal("test proof unexpectedly has no side branches")
	}
	t.Run("side prefix changed", func(t *testing.T) {
		tampered := cloneProof(proof)
		tampered.Sides[0].Prefix ^= 1
		if err := tampered.Verify(root); err == nil {
			t.Fatal("tampered side prefix was accepted")
		}
	})
	t.Run("side depth changed", func(t *testing.T) {
		tampered := cloneProof(proof)
		if tampered.Sides[0].Depth == 1 {
			tampered.Sides[0].Depth = 2
		} else {
			tampered.Sides[0].Depth--
		}
		if err := tampered.Verify(root); err == nil {
			t.Fatal("tampered side depth was accepted")
		}
	})
	t.Run("side digest changed", func(t *testing.T) {
		tampered := cloneProof(proof)
		tampered.Sides[0].Digest[0] ^= 0xff
		if err := tampered.Verify(root); !errors.Is(err, ErrRootMismatch) {
			t.Fatalf("got %v, want root mismatch", err)
		}
	})
}

func TestMalformedProofNodesAreRejected(t *testing.T) {
	values := map[uint64][]byte{3: refValueA, 10: refValueB, 19: refValueA}
	const height = uint8(5)
	root := referenceRoot(height, values)
	proof, err := GenerateProof(height, values, []uint64{1, 3, 19})
	if err != nil {
		t.Fatal(err)
	}

	firstQueryPrefix := proof.Queries[0].Key >> (height - 1)
	firstQuerySibling := firstQueryPrefix ^ 1

	tests := []struct {
		name    string
		mutate  func(*MultiProof)
		wantErr error
	}{
		{
			name: "explicit empty side",
			mutate: func(p *MultiProof) {
				filtered := p.Sides[:0]
				for _, side := range p.Sides {
					if side.Depth == 1 && side.Prefix == firstQuerySibling {
						continue
					}
					filtered = append(filtered, side)
				}
				p.Sides = filtered
				p.Sides = append(p.Sides, ProofSide{
					Depth:  1,
					Prefix: firstQuerySibling,
					Digest: emptyRoots[height-1],
				})
			},
			wantErr: ErrExplicitEmptySide,
		},
		{
			name: "non-zero extra prefix bits",
			mutate: func(p *MultiProof) {
				p.Sides = append(p.Sides, ProofSide{
					Depth:  1,
					Prefix: 2,
					Digest: testSideDigest(),
				})
			},
			wantErr: ErrNonZeroPrefixBits,
		},
		{
			name: "side covering query",
			mutate: func(p *MultiProof) {
				p.Sides = append(p.Sides, ProofSide{
					Depth:  1,
					Prefix: firstQueryPrefix,
					Digest: testSideDigest(),
				})
			},
			wantErr: ErrSideCoversQuery,
		},
		{
			name: "side not on sibling boundary",
			mutate: func(p *MultiProof) {
				p.Sides = append(p.Sides, ProofSide{
					Depth:  4,
					Prefix: 2,
					Digest: testSideDigest(),
				})
			},
			wantErr: ErrSideNotSibling,
		},
		{
			name: "duplicate side coordinate",
			mutate: func(p *MultiProof) {
				p.Sides = append(p.Sides, p.Sides[0])
			},
			wantErr: ErrDuplicateCoordinate,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			tampered := cloneProof(proof)
			test.mutate(tampered)
			err := tampered.Verify(root)
			if !errors.Is(err, test.wantErr) {
				t.Fatalf("got %v, want %v", err, test.wantErr)
			}
		})
	}

	// Exercise the final unconsumed-node check directly with a structurally
	// plausible side that no active frontier node ever reads.
	extra := cloneProof(proof)
	extra.Sides = append(extra.Sides, ProofSide{
		Depth:  1,
		Prefix: firstQueryPrefix,
		Digest: testSideDigest(),
	})
	_, err = extra.computeRoot(map[uint64]Digest{
		proof.Queries[0].Key: AbsentLeafHash(),
	})
	if !errors.Is(err, ErrUnconsumedSide) {
		t.Fatalf("unconsumed side: got %v", err)
	}
}

func TestBatchUpdateMatchesReference(t *testing.T) {
	const height = uint8(6)
	values := map[uint64][]byte{
		5:  refValueA,
		17: refValueB,
		33: refValueA,
		40: refValueB,
	}
	root := referenceRoot(height, values)
	queries := []uint64{1, 5, 17, 33, 40, 63}
	proof, err := GenerateProof(height, values, queries)
	if err != nil {
		t.Fatal(err)
	}

	updates := []Update{
		SetUpdate(5, []byte{0x55, 0x66}),
		SetUpdate(1, []byte{0x77}),
		DeleteUpdate(17),
		SetUpdate(40, []byte{}),
		DeleteUpdate(63),
	}
	newRoot, err := proof.Update(root, updates)
	if err != nil {
		t.Fatal(err)
	}

	expected := make(map[uint64][]byte)
	for key, value := range values {
		expected[key] = cloneBytes(value)
	}
	expected[5] = []byte{0x55, 0x66}
	expected[1] = []byte{0x77}
	delete(expected, 17)
	expected[40] = []byte{}
	delete(expected, 63)
	expectedRoot := referenceRoot(height, expected)
	if newRoot != expectedRoot {
		t.Fatalf("new root %s, want %s", newRoot, expectedRoot)
	}
	if err := proof.Verify(root); err != nil {
		t.Fatalf("proof mutated by Update: %v", err)
	}

	noChange, err := proof.Update(root, nil)
	if err != nil {
		t.Fatal(err)
	}
	if noChange != root {
		t.Fatalf("empty update changed root: %s != %s", noChange, root)
	}
}

func TestBatchUpdateFailureProducesNoRoot(t *testing.T) {
	const height = uint8(6)
	values := map[uint64][]byte{5: refValueA, 17: refValueB}
	root := referenceRoot(height, values)
	proof, err := GenerateProof(height, values, []uint64{1, 5, 17, 30})
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name    string
		mutate  func(*MultiProof)
		updates []Update
		wantErr error
	}{
		{
			name: "stale old value",
			mutate: func(p *MultiProof) {
				p.Queries[1].Value = cloneBytes(refValueB)
			},
			updates: []Update{SetUpdate(1, refValueB)},
			wantErr: ErrRootMismatch,
		},
		{
			name:    "duplicate update key",
			updates: []Update{SetUpdate(1, refValueB), DeleteUpdate(1)},
			wantErr: ErrDuplicateUpdate,
		},
		{
			name:    "update key not proven",
			updates: []Update{SetUpdate(2, refValueB)},
			wantErr: ErrUpdateKeyNotProven,
		},
		{
			name:    "delete carries value",
			updates: []Update{{Key: 17, Value: refValueA, Delete: true}},
			wantErr: ErrInvalidUpdate,
		},
		{
			name:    "valid update plus invalid update",
			updates: []Update{SetUpdate(5, refValueB), SetUpdate(2, refValueA)},
			wantErr: ErrUpdateKeyNotProven,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			current := proof
			if test.mutate != nil {
				current = cloneProof(proof)
				test.mutate(current)
			}
			newRoot, err := current.Update(root, test.updates)
			if !errors.Is(err, test.wantErr) {
				t.Fatalf("got %v, want %v", err, test.wantErr)
			}
			if newRoot != (Digest{}) {
				t.Fatalf("failed batch produced non-zero root %s", newRoot)
			}
		})
	}
}

func TestGeneratedProofSideSetIsMinimalAndSorted(t *testing.T) {
	values := map[uint64][]byte{
		0: refValueA,
		1: refValueB,
		2: refValueA,
		5: refValueB,
	}
	proof, err := GenerateProof(3, values, []uint64{0, 1, 2, 5})
	if err != nil {
		t.Fatal(err)
	}
	for index := 1; index < len(proof.Sides); index++ {
		previous := proof.Sides[index-1]
		current := proof.Sides[index]
		if previous.Depth > current.Depth ||
			(previous.Depth == current.Depth && previous.Prefix >= current.Prefix) {
			t.Fatalf("sides are not sorted at index %d", index)
		}
	}
	for _, side := range proof.Sides {
		if side.Digest == emptyRoots[proof.Height-side.Depth] {
			t.Fatalf("generator emitted explicit empty side %d/%d", side.Depth, side.Prefix)
		}
	}
}

func testSideDigest() Digest {
	return LeafHash(42, refValueA)
}

func referenceHash(parts ...[]byte) Digest {
	h := sha256.New()
	for _, part := range parts {
		_, _ = h.Write(part)
	}
	var digest Digest
	copy(digest[:], h.Sum(nil))
	return digest
}

func TestHashConstruction(t *testing.T) {
	values := [][]byte{nil, {}, {0x00}, refValueA, make([]byte, 257)}
	values[4][0] = 0xfe
	for _, key := range []uint64{0, 1, 42, math.MaxUint64} {
		for _, value := range values {
			var header [16]byte
			binary.BigEndian.PutUint64(header[:8], key)
			binary.BigEndian.PutUint64(header[8:], uint64(len(value)))
			want := referenceHash([]byte{0x00}, header[:], value)
			if got := LeafHash(key, value); got != want {
				t.Fatalf("leaf key %d value length %d: got %s, want %s", key, len(value), got, want)
			}
		}
	}

	if got, want := AbsentLeafHash(), referenceHash([]byte{0x02}); got != want {
		t.Fatalf("absent leaf: got %s, want %s", got, want)
	}

	left := LeafHash(1, refValueA)
	right := LeafHash(2, refValueB)
	if got, want := InternalHash(left, right), referenceHash([]byte{0x01}, left[:], right[:]); got != want {
		t.Fatalf("internal: got %s, want %s", got, want)
	}
}

func TestPathUsesMostSignificantBitFirst(t *testing.T) {
	key := uint64(0b101010)
	want := []uint64{0, 1, 2, 5, 10, 21, 42}
	for depth := uint8(0); depth <= 6; depth++ {
		got, err := PrefixAtDepth(key, 6, depth)
		if err != nil {
			t.Fatalf("depth %d: %v", depth, err)
		}
		if got != want[depth] {
			t.Fatalf("depth %d: prefix %d, want %d", depth, got, want[depth])
		}
	}
	if _, err := PrefixAtDepth(key, 6, 7); !errors.Is(err, ErrDepth) {
		t.Fatalf("depth above height: %v", err)
	}

	const height = uint8(64)
	for depth := uint8(1); depth <= height; depth++ {
		got, err := PrefixAtDepth(math.MaxUint64, height, depth)
		if err != nil {
			t.Fatalf("height 64 depth %d: %v", depth, err)
		}
		if got != uint64(1)<<depth-1 {
			t.Fatalf("height 64 depth %d: prefix %d", depth, got)
		}
	}
	if prefix, err := PrefixAtDepth(uint64(1)<<63, height, 1); err != nil || prefix != 1 {
		t.Fatalf("top bit: prefix %d, err %v", prefix, err)
	}
}

func TestQueryValidationBoundaries(t *testing.T) {
	values := map[uint64][]byte{uint64(1) << 6: refValueA}
	if _, err := GenerateProof(6, values, nil); !errors.Is(err, ErrQueryCount) {
		t.Fatalf("empty query set: %v", err)
	}
	if _, err := GenerateProof(6, nil, []uint64{2, 1}); !errors.Is(err, ErrQueryOrder) {
		t.Fatalf("unordered query set: %v", err)
	}
	if _, err := GenerateProof(6, nil, []uint64{1, 1}); !errors.Is(err, ErrQueryOrder) {
		t.Fatalf("duplicate query set: %v", err)
	}
	if _, err := GenerateProof(6, nil, []uint64{64}); !errors.Is(err, ErrQueryKey) {
		t.Fatalf("out-of-range key: %v", err)
	}
	if _, err := GenerateProof(0, nil, []uint64{0}); !errors.Is(err, ErrHeight) {
		t.Fatalf("height zero: %v", err)
	}

	tooMany := make([]uint64, MaxQueries+1)
	for index := range tooMany {
		tooMany[index] = uint64(index)
	}
	if _, err := GenerateProof(64, nil, tooMany); !errors.Is(err, ErrQueryCount) {
		t.Fatalf("too many queries: %v", err)
	}
}

func TestProofSideBranchesForAbsence(t *testing.T) {
	// This table deliberately makes both leaves under prefix 00 present and
	// both absent elsewhere, so absence proofs need real sibling digests.
	values := map[uint64][]byte{
		0: refValueA,
		1: refValueB,
		6: refValueA,
	}
	const height = uint8(4)
	root := referenceRoot(height, values)
	queries := []uint64{2, 3, 4, 5, 7, 8, 9, 10, 11, 12, 13, 14, 15}
	proof, err := GenerateProof(height, values, queries)
	if err != nil {
		t.Fatal(err)
	}
	if err := proof.Verify(root); err != nil {
		t.Fatal(err)
	}
	for _, query := range proof.Queries {
		if query.Present {
			t.Fatalf("query %d unexpectedly present", query.Key)
		}
	}

	// Reordering absence queries must not change a valid proof into a forgery.
	for left := 0; left < len(queries)-1; left++ {
		tampered := cloneProof(proof)
		tampered.Queries[left], tampered.Queries[left+1] =
			tampered.Queries[left+1], tampered.Queries[left]
		if err := tampered.Verify(root); !errors.Is(err, ErrQueryOrder) {
			t.Fatalf("swap %d/%d: got %v", left, left+1, err)
		}
	}
}

func TestRootTableAndTreeAgree(t *testing.T) {
	values := map[uint64][]byte{2: refValueA, 7: nil, 15: refValueB}
	functionRoot, err := Root(4, values)
	if err != nil {
		t.Fatal(err)
	}
	tree, err := NewTree(4, values)
	if err != nil {
		t.Fatal(err)
	}
	if functionRoot != tree.Root() {
		t.Fatal("Root and Tree.Root disagree")
	}
	if functionRoot != referenceRoot(4, values) {
		t.Fatal("Root disagrees with reference")
	}
}

func TestRandomSparseProofsAndUpdates(t *testing.T) {
	rng := rand.New(rand.NewSource(20261113))
	for iteration := 0; iteration < 600; iteration++ {
		height := uint8(rng.Intn(int(MaxHeight)) + 1)
		var limit uint64
		if height == 64 {
			limit = math.MaxUint64
		} else {
			limit = uint64(1) << height
		}

		values := make(map[uint64][]byte)
		entryCount := rng.Intn(24)
		for index := 0; index < entryCount; index++ {
			key := rng.Uint64()
			if height != 64 {
				key %= limit
			}
			if rng.Intn(2) == 0 {
				values[key] = cloneBytes(referenceValue(key))
			} else {
				values[key] = nil
			}
		}

		tree, err := NewTree(height, values)
		if err != nil {
			t.Fatal(err)
		}
		root := tree.Root()
		if height <= 6 && root != referenceRoot(height, values) {
			t.Fatalf("iteration %d height %d: root disagrees with reference", iteration, height)
		}

		allKeys := make([]uint64, 0, MaxQueries)
		for key := range values {
			allKeys = append(allKeys, key)
		}
		for index := 0; index < rng.Intn(20); index++ {
			key := rng.Uint64()
			if height != 64 {
				key %= limit
			}
			allKeys = append(allKeys, key)
		}
		sort.Slice(allKeys, func(i, j int) bool { return allKeys[i] < allKeys[j] })
		keys := allKeys[:0]
		for _, key := range allKeys {
			if len(keys) == 0 || key != keys[len(keys)-1] {
				keys = append(keys, key)
			}
		}
		if len(keys) > MaxQueries {
			keys = keys[:MaxQueries]
		}
		if len(keys) == 0 {
			keys = append(keys, 0)
		}

		proof, err := tree.Prove(keys)
		if err != nil {
			t.Fatalf("iteration %d: Prove: %v", iteration, err)
		}
		if err := proof.Verify(root); err != nil {
			t.Fatalf("iteration %d height %d keys %v: Verify: %v", iteration, height, keys, err)
		}

		updateCount := rng.Intn(len(keys) + 1)
		updates := make([]Update, 0, updateCount)
		order := rng.Perm(len(keys))
		for index := 0; index < updateCount; index++ {
			key := keys[order[index]]
			if rng.Intn(2) == 0 {
				updates = append(updates, DeleteUpdate(key))
			} else {
				updates = append(updates, SetUpdate(key, referenceValue(key+uint64(index)+1)))
			}
		}

		newRoot, err := proof.Update(root, updates)
		if err != nil {
			t.Fatalf("iteration %d: Update: %v", iteration, err)
		}
		expectedTree, err := NewTree(height, values)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := expectedTree.ApplyUpdates(updates); err != nil {
			t.Fatalf("iteration %d: expected ApplyUpdates: %v", iteration, err)
		}
		if want := expectedTree.Root(); newRoot != want {
			t.Fatalf("iteration %d height %d: new root %s, want %s", iteration, height, newRoot, want)
		}
	}
}

func TestTableKeysStaySortedInTestHelper(t *testing.T) {
	// Guard against accidentally testing unsorted generated key slices.
	keys := keysForMask(16, 0xffff)
	if !sort.SliceIsSorted(keys, func(i, j int) bool { return keys[i] < keys[j] }) {
		t.Fatal("keysForMask produced unordered keys")
	}
	if len(keys) != 16 {
		t.Fatalf("got %d keys", len(keys))
	}
	if !slices.Equal(keysForMask(16, 3), []uint64{0, 1}) {
		t.Fatalf("unexpected mask expansion: %v", keysForMask(16, 3))
	}
}

func ExampleGenerateProof() {
	values := map[uint64][]byte{
		1: []byte("alice"),
		4: []byte("carol"),
	}
	tree, _ := NewTree(3, values)
	proof, _ := tree.Prove([]uint64{1, 2})
	fmt.Println(proof.Height, len(proof.Queries), len(proof.Sides))
	// Output: 3 2 1
}
