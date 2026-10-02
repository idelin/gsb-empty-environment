package merkle

import (
	"errors"
	"sort"
)

// ErrKeyNotFound is returned when proving or deleting a missing key.
var ErrKeyNotFound = errors.New("merkle: key not found in shard")

// ShardTree is a dynamic Merkle tree over the account state of one
// shard. Leaves are keyed (e.g. account address) and may be inserted,
// updated, or deleted at any time; the local root is recomputed
// lazily after each mutation.
type ShardTree struct {
	id     int
	leaves map[string][]byte // key -> value
	levels [][]Digest        // cached levels, nil when dirty
	dirty  bool
}

// NewShardTree creates an empty shard tree with the given shard id.
func NewShardTree(id int) *ShardTree {
	return &ShardTree{id: id, leaves: make(map[string][]byte), dirty: true}
}

// ID returns the shard identifier.
func (t *ShardTree) ID() int { return t.id }

// Put inserts or updates the value stored under key.
func (t *ShardTree) Put(key, value []byte) {
	t.leaves[string(key)] = append([]byte(nil), value...)
	t.dirty = true
}

// Delete removes key from the shard.
func (t *ShardTree) Delete(key []byte) error {
	if _, ok := t.leaves[string(key)]; !ok {
		return ErrKeyNotFound
	}
	delete(t.leaves, string(key))
	t.dirty = true
	return nil
}

// Get returns the value stored under key.
func (t *ShardTree) Get(key []byte) ([]byte, bool) {
	v, ok := t.leaves[string(key)]
	return v, ok
}

// Len returns the number of leaves in the shard.
func (t *ShardTree) Len() int { return len(t.leaves) }

// sortedKeys returns leaf keys in canonical (lexicographic) order so
// the tree shape is deterministic regardless of insertion order.
func (t *ShardTree) sortedKeys() []string {
	keys := make([]string, 0, len(t.leaves))
	for k := range t.leaves {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func (t *ShardTree) rebuild() {
	if !t.dirty {
		return
	}
	keys := t.sortedKeys()
	leaves := make([]Digest, len(keys))
	for i, k := range keys {
		leaves[i] = LeafHash([]byte(k), t.leaves[k])
	}
	t.levels = buildLevels(leaves)
	t.dirty = false
}

// Root returns the current local root of the shard.
func (t *ShardTree) Root() Digest {
	t.rebuild()
	return t.levels[len(t.levels)-1][0]
}

// Prove generates an inclusion proof for key inside this shard.
func (t *ShardTree) Prove(key []byte) ([]ProofStep, error) {
	t.rebuild()
	keys := t.sortedKeys()
	idx := sort.SearchStrings(keys, string(key))
	if idx >= len(keys) || keys[idx] != string(key) {
		return nil, ErrKeyNotFound
	}
	return pathFromLevels(t.levels, idx), nil
}

// VerifyShardProof checks that key/value is included under shardRoot.
// It is a standalone building block usable without a ShardTree instance.
func VerifyShardProof(shardRoot Digest, key, value []byte, path []ProofStep) bool {
	return fold(LeafHash(key, value), path) == shardRoot
}
