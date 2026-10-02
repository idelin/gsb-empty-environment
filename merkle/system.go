package merkle

// DefaultShardCount is the number of independent shard trees.
const DefaultShardCount = 4

// System is the sharded state verifier: DefaultShardCount independent
// shard trees plus a main aggregator (meta tree) that folds the shard
// roots into one meta root.
type System struct {
	shards []*ShardTree
	meta   *MetaTree
}

// NewSystem creates a system with numShards empty shard trees.
func NewSystem(numShards int) *System {
	s := &System{meta: NewMetaTree(numShards)}
	for i := 0; i < numShards; i++ {
		s.shards = append(s.shards, NewShardTree(i))
	}
	return s
}

// Shard returns the shard tree with the given id.
func (s *System) Shard(id int) (*ShardTree, error) {
	if id < 0 || id >= len(s.shards) {
		return nil, ErrShardOutOfRange
	}
	return s.shards[id], nil
}

// Update applies a key/value mutation inside one shard and pushes the
// shard's new local root into the meta tree, so the meta root always
// reflects the latest cross-shard state.
func (s *System) Update(shard int, key, value []byte) error {
	t, err := s.Shard(shard)
	if err != nil {
		return err
	}
	t.Put(key, value)
	return s.meta.SetShardRoot(shard, t.Root())
}

// Delete removes a key inside one shard and refreshes the meta tree.
func (s *System) Delete(shard int, key []byte) error {
	t, err := s.Shard(shard)
	if err != nil {
		return err
	}
	if err := t.Delete(key); err != nil {
		return err
	}
	return s.meta.SetShardRoot(shard, t.Root())
}

// MetaRoot returns the current top-level meta root.
func (s *System) MetaRoot() Digest { return s.meta.Root() }

// ShardRoot returns the local root currently committed for a shard.
func (s *System) ShardRoot(shard int) (Digest, error) {
	return s.meta.ShardRoot(shard)
}

// CrossShardProof is the end-to-end aggregate proof: an inclusion
// proof of the leaf inside its shard, plus the shard's position proof
// inside the meta tree. Any external verifier can check it against
// the published meta root without trusting any component.
type CrossShardProof struct {
	ShardIndex int
	Key        []byte
	Value      []byte
	ShardPath  []ProofStep // leaf -> shard local root
	MetaPath   []ProofStep // shard local root -> meta root
}

// ProveCrossShard builds the aggregate proof for key in shard:
// the shard-local inclusion proof merged with the shard's position
// proof in the meta tree.
func (s *System) ProveCrossShard(shard int, key []byte) (*CrossShardProof, error) {
	t, err := s.Shard(shard)
	if err != nil {
		return nil, err
	}
	value, ok := t.Get(key)
	if !ok {
		return nil, ErrKeyNotFound
	}
	shardPath, err := t.Prove(key)
	if err != nil {
		return nil, err
	}
	metaPath, err := s.meta.ProveShard(shard)
	if err != nil {
		return nil, err
	}
	return &CrossShardProof{
		ShardIndex: shard,
		Key:        append([]byte(nil), key...),
		Value:      append([]byte(nil), value...),
		ShardPath:  shardPath,
		MetaPath:   metaPath,
	}, nil
}

// VerifyCrossShardProof independently verifies an aggregate proof
// against a published meta root. It replays the leaf hash up the
// shard path to recover the shard root, then continues up the meta
// path; the result must equal the meta root. No access to any shard
// or aggregator state is required.
func VerifyCrossShardProof(metaRoot Digest, p *CrossShardProof) bool {
	if p == nil {
		return false
	}
	shardRoot := fold(LeafHash(p.Key, p.Value), p.ShardPath)
	return fold(shardRoot, p.MetaPath) == metaRoot
}
