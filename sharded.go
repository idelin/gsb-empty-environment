// Package sharded implements a dynamically sharded Merkle state system:
// NumShards independent shard trees, each maintaining its own local root,
// plus a master aggregator that builds a top-level Meta-Tree over the
// shard roots and produces end-to-end cross-shard aggregate proofs.
package sharded

import (
	"encoding/binary"
	"fmt"
	"sort"

	"shardedmerkle/merkle"
)

// NumShards is the fixed number of independent shard trees.
const NumShards = 4

// accountLeafData encodes an account entry deterministically as a leaf
// payload: 0x01 || uint16(len(key)) || key || value.
func accountLeafData(account string, value []byte) []byte {
	buf := make([]byte, 0, 3+len(account)+len(value))
	buf = append(buf, 0x01)
	var lb [2]byte
	binary.BigEndian.PutUint16(lb[:], uint16(len(account)))
	buf = append(buf, lb[:]...)
	buf = append(buf, account...)
	buf = append(buf, value...)
	return buf
}

// Shard is one independent shard tree holding account -> balance state.
type Shard struct {
	ID       int
	balances map[string]uint64
	keys     []string // sorted account keys, mirrors tree leaf order
	tree     *merkle.Tree
}

// NewShard creates an empty shard with the given ID.
func NewShard(id int) *Shard {
	return &Shard{ID: id, balances: make(map[string]uint64)}
}

// rebuild re-sorts the keys and reconstructs the shard's Merkle tree.
func (s *Shard) rebuild() {
	s.keys = s.keys[:0]
	for k := range s.balances {
		s.keys = append(s.keys, k)
	}
	sort.Strings(s.keys)
	leaves := make([][]byte, len(s.keys))
	for i, k := range s.keys {
		var vb [8]byte
		binary.BigEndian.PutUint64(vb[:], s.balances[k])
		leaves[i] = accountLeafData(k, vb[:])
	}
	s.tree = merkle.NewTree(leaves)
}

// Set creates or updates an account balance and refreshes the local root.
func (s *Shard) Set(account string, balance uint64) {
	s.balances[account] = balance
	s.rebuild()
}

// Balance returns the balance of an account and whether it exists.
func (s *Shard) Balance(account string) (uint64, bool) {
	b, ok := s.balances[account]
	return b, ok
}

// Transfer moves amount between two accounts inside this shard and
// refreshes the local root atomically with the state change.
func (s *Shard) Transfer(from, to string, amount uint64) error {
	fb, ok := s.balances[from]
	if !ok {
		return fmt.Errorf("shard %d: sender %q not found", s.ID, from)
	}
	if fb < amount {
		return fmt.Errorf("shard %d: insufficient balance of %q: have %d, need %d", s.ID, from, fb, amount)
	}
	s.balances[from] = fb - amount
	s.balances[to] = s.balances[to] + amount
	s.rebuild()
	return nil
}

// Root returns the shard's current local Merkle root (nil if empty).
func (s *Shard) Root() []byte {
	if s.tree == nil {
		return nil
	}
	return s.tree.Root()
}

// Prove returns an inclusion proof for the account inside this shard,
// together with the account's current balance.
func (s *Shard) Prove(account string) (*merkle.Proof, uint64, error) {
	bal, ok := s.balances[account]
	if !ok {
		return nil, 0, fmt.Errorf("shard %d: account %q not found", s.ID, account)
	}
	idx := sort.SearchStrings(s.keys, account)
	p, err := s.tree.Prove(idx)
	if err != nil {
		return nil, 0, err
	}
	return p, bal, nil
}

// Aggregator is the master combiner: it folds the NumShards local roots
// into a single top-level Meta-Tree.
type Aggregator struct {
	shards [NumShards]*Shard
	meta   *merkle.Tree
}

// NewAggregator creates shard trees 0..NumShards-1 and the meta-tree.
func NewAggregator() *Aggregator {
	a := &Aggregator{}
	for i := 0; i < NumShards; i++ {
		a.shards[i] = NewShard(i)
	}
	a.Refresh()
	return a
}

// Shard returns the shard tree with the given ID.
func (a *Aggregator) Shard(id int) *Shard { return a.shards[id] }

// Refresh rebuilds the Meta-Tree from the current local shard roots.
// Empty shards contribute a zero root so the meta-tree always has
// exactly NumShards leaves at fixed positions.
func (a *Aggregator) Refresh() {
	leaves := make([][]byte, NumShards)
	for i, s := range a.shards {
		root := s.Root()
		if root == nil {
			root = make([]byte, 32) // zero root for empty shard
		}
		leaves[i] = root
	}
	a.meta = merkle.NewTree(leaves)
}

// MetaRoot returns the current top-level Meta-Tree root.
func (a *Aggregator) MetaRoot() []byte { return a.meta.Root() }

// CombinedProof is an end-to-end cross-shard aggregate proof:
//  1. inclusion of the account inside its shard tree, and
//  2. inclusion of that shard's root inside the Meta-Tree.
type CombinedProof struct {
	ShardID    int
	Account    string
	Balance    uint64
	ShardProof *merkle.Proof // account -> shard root
	MetaProof  *merkle.Proof // shard root -> meta root
}

// ProveCrossShard builds the combined proof for an account living in
// shard shardID: the shard-local inclusion proof plus the shard's
// position proof inside the Meta-Tree.
func (a *Aggregator) ProveCrossShard(shardID int, account string) (*CombinedProof, error) {
	if shardID < 0 || shardID >= NumShards {
		return nil, fmt.Errorf("invalid shard id %d", shardID)
	}
	shardProof, bal, err := a.shards[shardID].Prove(account)
	if err != nil {
		return nil, err
	}
	metaProof, err := a.meta.Prove(shardID)
	if err != nil {
		return nil, err
	}
	return &CombinedProof{
		ShardID:    shardID,
		Account:    account,
		Balance:    bal,
		ShardProof: shardProof,
		MetaProof:  metaProof,
	}, nil
}

// VerifyCombinedProof lets any external verifier independently check a
// CombinedProof against a known metaRoot, with no access to any shard
// state. It recomputes the account leaf, folds it to the shard root,
// then folds the shard root through the meta proof to the meta root.
func VerifyCombinedProof(metaRoot []byte, cp *CombinedProof) bool {
	if cp == nil || cp.ShardID < 0 || cp.ShardID >= NumShards {
		return false
	}
	// Stage 1: account -> shard root. The shard root is not known to the
	// verifier a priori; it is recovered here and authenticated by stage 2.
	var vb [8]byte
	binary.BigEndian.PutUint64(vb[:], cp.Balance)
	leafHash := merkle.LeafHash(accountLeafData(cp.Account, vb[:]))
	shardRoot := merkle.ComputeRoot(leafHash, cp.ShardProof)

	// Stage 2: shard root -> meta root, at the claimed shard position.
	if cp.MetaProof == nil || cp.MetaProof.Index != uint64(cp.ShardID) ||
		cp.MetaProof.Size != uint64(NumShards) {
		return false
	}
	metaLeaf := merkle.LeafHash(shardRoot)
	return merkle.VerifyProof(metaRoot, metaLeaf, cp.MetaProof)
}
