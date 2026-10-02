package merkle

import "errors"

// ErrShardOutOfRange is returned for an invalid shard index.
var ErrShardOutOfRange = errors.New("merkle: shard index out of range")

// MetaTree is the top-level tree maintained by the main aggregator.
// Its leaves are the local roots of the shard trees, so a single
// meta root commits to the full cross-shard state.
type MetaTree struct {
	shardRoots []Digest
	levels     [][]Digest
}

// NewMetaTree creates a meta tree over numShards shard roots,
// initially all empty-shard roots.
func NewMetaTree(numShards int) *MetaTree {
	roots := make([]Digest, numShards)
	for i := range roots {
		roots[i] = emptyRoot
	}
	m := &MetaTree{shardRoots: roots}
	m.rebuild()
	return m
}

// NumShards returns how many shard roots the meta tree commits to.
func (m *MetaTree) NumShards() int { return len(m.shardRoots) }

func (m *MetaTree) rebuild() {
	m.levels = buildLevels(append([]Digest(nil), m.shardRoots...))
}

// SetShardRoot installs the latest local root of a shard and
// recomputes the meta root.
func (m *MetaTree) SetShardRoot(shard int, root Digest) error {
	if shard < 0 || shard >= len(m.shardRoots) {
		return ErrShardOutOfRange
	}
	m.shardRoots[shard] = root
	m.rebuild()
	return nil
}

// ShardRoot returns the committed local root of a shard.
func (m *MetaTree) ShardRoot(shard int) (Digest, error) {
	if shard < 0 || shard >= len(m.shardRoots) {
		return Digest{}, ErrShardOutOfRange
	}
	return m.shardRoots[shard], nil
}

// Root returns the current meta root.
func (m *MetaTree) Root() Digest {
	return m.levels[len(m.levels)-1][0]
}

// ProveShard generates a proof that the committed root of shard sits
// at position shard in the meta tree.
func (m *MetaTree) ProveShard(shard int) ([]ProofStep, error) {
	if shard < 0 || shard >= len(m.shardRoots) {
		return nil, ErrShardOutOfRange
	}
	return pathFromLevels(m.levels, shard), nil
}

// VerifyMetaProof checks that shardRoot sits at the meta-tree position
// proven by path, under the given meta root.
func VerifyMetaProof(metaRoot, shardRoot Digest, path []ProofStep) bool {
	return fold(shardRoot, path) == metaRoot
}
