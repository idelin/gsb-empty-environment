package netsec

import (
	"fmt"
	"sort"
)

// Tree is a sparse Merkle tree backed by an explicit key/value table.
type Tree struct {
	height uint8
	values map[uint64][]byte
}

// SparseMerkleTree is an alias for Tree.
type SparseMerkleTree = Tree

// New returns an empty tree of the given height.
func New(height uint8) (*Tree, error) {
	if !validHeight(height) {
		return nil, ErrHeight
	}
	return &Tree{height: height, values: make(map[uint64][]byte)}, nil
}

// NewTree returns a tree containing a defensive copy of values.
func NewTree(height uint8, values map[uint64][]byte) (*Tree, error) {
	tree, err := New(height)
	if err != nil {
		return nil, err
	}
	for key, value := range values {
		if err := tree.Set(key, value); err != nil {
			return nil, err
		}
	}
	return tree, nil
}

// NewSparseMerkleTree is an alias for NewTree.
func NewSparseMerkleTree(height uint8, values map[uint64][]byte) (*Tree, error) {
	return NewTree(height, values)
}

// Height returns the tree height.
func (t *Tree) Height() uint8 {
	if t == nil {
		return 0
	}
	return t.height
}

// Len returns the number of present leaves.
func (t *Tree) Len() int {
	if t == nil {
		return 0
	}
	return len(t.values)
}

// Get returns a defensive copy of the value stored at key.
func (t *Tree) Get(key uint64) ([]byte, bool) {
	if t == nil || !keyInRange(key, t.height) {
		return nil, false
	}
	value, ok := t.values[key]
	if !ok {
		return nil, false
	}
	return cloneBytes(value), true
}

// Set inserts or replaces a present leaf. A nil value is a present empty value,
// not an absence.
func (t *Tree) Set(key uint64, value []byte) error {
	if t == nil || !validHeight(t.height) {
		return ErrHeight
	}
	if !keyInRange(key, t.height) {
		return fmt.Errorf("%w: key %d at height %d", ErrQueryKey, key, t.height)
	}
	if t.values == nil {
		t.values = make(map[uint64][]byte)
	}
	t.values[key] = cloneBytes(value)
	return nil
}

// Delete makes a key absent.
func (t *Tree) Delete(key uint64) error {
	if t == nil || !validHeight(t.height) {
		return ErrHeight
	}
	if !keyInRange(key, t.height) {
		return fmt.Errorf("%w: key %d at height %d", ErrQueryKey, key, t.height)
	}
	delete(t.values, key)
	return nil
}

// Root returns the tree root. An invalid zero-value Tree returns the zero digest.
func (t *Tree) Root() Digest {
	if t == nil || !validHeight(t.height) {
		return Digest{}
	}
	if len(t.values) == 0 {
		return emptyRoots[t.height]
	}
	return rootFromNodes(t.height, buildNodes(t.height, t.values))
}

// Root recomputes the root of a sparse key/value table.
func Root(height uint8, values map[uint64][]byte) (Digest, error) {
	tree, err := NewTree(height, values)
	if err != nil {
		return Digest{}, err
	}
	return tree.Root(), nil
}

// Prove generates a multi-proof for keys. Keys must be strictly increasing.
func (t *Tree) Prove(keys []uint64) (*MultiProof, error) {
	if t == nil || !validHeight(t.height) {
		return nil, ErrHeight
	}
	return GenerateProof(t.height, t.values, keys)
}

// ApplyUpdates applies a batch atomically. If any update fails validation, the
// tree is not modified and no new root is returned.
func (t *Tree) ApplyUpdates(updates []Update) (Digest, error) {
	if t == nil || !validHeight(t.height) {
		return Digest{}, ErrHeight
	}

	next := make(map[uint64][]byte, len(t.values))
	for key, value := range t.values {
		next[key] = cloneBytes(value)
	}

	seen := make(map[uint64]struct{}, len(updates))
	for _, update := range updates {
		if !keyInRange(update.Key, t.height) {
			return Digest{}, fmt.Errorf("%w: key %d", ErrQueryKey, update.Key)
		}
		if _, ok := seen[update.Key]; ok {
			return Digest{}, fmt.Errorf("%w: key %d", ErrDuplicateUpdate, update.Key)
		}
		seen[update.Key] = struct{}{}
		if update.Delete && len(update.Value) != 0 {
			return Digest{}, fmt.Errorf("%w: delete key carries a value", ErrInvalidUpdate)
		}
		if update.Delete {
			delete(next, update.Key)
		} else {
			next[update.Key] = cloneBytes(update.Value)
		}
	}

	var root Digest
	if len(next) == 0 {
		root = emptyRoots[t.height]
	} else {
		root = rootFromNodes(t.height, buildNodes(t.height, next))
	}
	t.values = next
	return root, nil
}

type nodeCoord struct {
	depth  uint8
	prefix uint64
}

func buildNodes(height uint8, values map[uint64][]byte) map[nodeCoord]Digest {
	nodes := make(map[nodeCoord]Digest)
	current := make(map[uint64]Digest, len(values))
	for key, value := range values {
		digest := LeafHash(key, value)
		current[key] = digest
		nodes[nodeCoord{depth: height, prefix: key}] = digest
	}

	type childPair struct {
		hasLeft  bool
		hasRight bool
		left     Digest
		right    Digest
	}

	for depth := int(height); depth >= 1; depth-- {
		parents := make(map[uint64]*childPair, len(current))
		for prefix, digest := range current {
			parent := prefix >> 1
			children := parents[parent]
			if children == nil {
				children = &childPair{}
				parents[parent] = children
			}
			if prefix&1 == 0 {
				children.hasLeft = true
				children.left = digest
			} else {
				children.hasRight = true
				children.right = digest
			}
		}

		emptyChild := emptyRoots[height-uint8(depth)]
		next := make(map[uint64]Digest, len(parents))
		for parent, children := range parents {
			left, right := emptyChild, emptyChild
			if children.hasLeft {
				left = children.left
			}
			if children.hasRight {
				right = children.right
			}
			digest := InternalHash(left, right)
			next[parent] = digest
			nodes[nodeCoord{depth: uint8(depth - 1), prefix: parent}] = digest
		}
		current = next
	}

	return nodes
}

func rootFromNodes(height uint8, nodes map[nodeCoord]Digest) Digest {
	if root, ok := nodes[nodeCoord{depth: 0, prefix: 0}]; ok {
		return root
	}
	return emptyRoots[height]
}

func validateQueries(height uint8, keys []uint64) error {
	if len(keys) == 0 || len(keys) > MaxQueries {
		return fmt.Errorf("%w: got %d", ErrQueryCount, len(keys))
	}
	for index, key := range keys {
		if !keyInRange(key, height) {
			return fmt.Errorf("%w: key %d at height %d", ErrQueryKey, key, height)
		}
		if index > 0 && key <= keys[index-1] {
			return fmt.Errorf("%w: key %d follows %d", ErrQueryOrder, key, keys[index-1])
		}
	}
	return nil
}

// GenerateProof builds a multi-proof from the full sparse table.
func GenerateProof(height uint8, values map[uint64][]byte, keys []uint64) (*MultiProof, error) {
	if !validHeight(height) {
		return nil, ErrHeight
	}
	if err := validateQueries(height, keys); err != nil {
		return nil, err
	}
	for key := range values {
		if !keyInRange(key, height) {
			return nil, fmt.Errorf("%w: table key %d at height %d", ErrQueryKey, key, height)
		}
	}

	nodes := buildNodes(height, values)
	proof := &MultiProof{
		Height:  height,
		Queries: make([]ProofQuery, len(keys)),
	}

	queryPrefixes := make([]map[uint64]struct{}, int(height)+1)
	for depth := uint8(1); depth <= height; depth++ {
		queryPrefixes[depth] = make(map[uint64]struct{}, len(keys))
		for _, key := range keys {
			prefix := key >> (height - depth)
			queryPrefixes[depth][prefix] = struct{}{}
		}
	}

	for index, key := range keys {
		query := ProofQuery{Key: key}
		if value, ok := values[key]; ok {
			query.Present = true
			query.Value = cloneBytes(value)
		}
		proof.Queries[index] = query
	}

	sideDigests := make(map[nodeCoord]Digest)
	for _, query := range proof.Queries {
		for depth := uint8(1); depth <= height; depth++ {
			prefix := query.Key >> (height - depth)
			sibling := prefix ^ 1
			coord := nodeCoord{depth: depth, prefix: sibling}

			// A sibling subtree containing a query is reconstructed from that
			// query's own path and must not be supplied as a side branch.
			if _, covered := queryPrefixes[depth][sibling]; covered {
				continue
			}
			digest, ok := nodes[coord]
			if !ok {
				// An unprovided side branch means the empty subtree.
				continue
			}
			sideDigests[coord] = digest
		}
	}

	proof.Sides = make([]ProofSide, 0, len(sideDigests))
	for coord, digest := range sideDigests {
		proof.Sides = append(proof.Sides, ProofSide{
			Depth:  coord.depth,
			Prefix: coord.prefix,
			Digest: digest,
		})
	}
	sort.Slice(proof.Sides, func(i, j int) bool {
		if proof.Sides[i].Depth != proof.Sides[j].Depth {
			return proof.Sides[i].Depth < proof.Sides[j].Depth
		}
		return proof.Sides[i].Prefix < proof.Sides[j].Prefix
	})
	return proof, nil
}

// Prove is an alias for GenerateProof.
func Prove(height uint8, values map[uint64][]byte, keys []uint64) (*MultiProof, error) {
	return GenerateProof(height, values, keys)
}
