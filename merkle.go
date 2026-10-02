package antientropy

import (
	"fmt"
	"hash/fnv"
	"sort"
)

const (
	// BucketCount is the number of leaf buckets partitioning the key space.
	BucketCount = 16
	// Fanout is the branching factor of internal tree levels.
	Fanout = 4
)

// MerkleNode is one node of the hash tree. Leaves map 1:1 to buckets.
type MerkleNode struct {
	Hash     uint64
	Bucket   int // -1 for internal nodes
	Children []*MerkleNode
}

func hashParts(parts ...string) uint64 {
	h := fnv.New64a()
	for _, p := range parts {
		h.Write([]byte(p))
		h.Write([]byte{0})
	}
	return h.Sum64()
}

func bucketOf(key string) int {
	return int(hashParts("bucket", key) % BucketCount)
}

// BuildTree builds a Merkle tree over the given states:
// keys are hashed into BucketCount leaf buckets, internal levels
// combine Fanout child hashes up to a single root.
func BuildTree(states []State) *MerkleNode {
	buckets := make([][]State, BucketCount)
	for _, st := range states {
		b := bucketOf(st.NodeID)
		buckets[b] = append(buckets[b], st)
	}
	leaves := make([]*MerkleNode, BucketCount)
	for i, sts := range buckets {
		sort.Slice(sts, func(a, b int) bool { return sts[a].NodeID < sts[b].NodeID })
		parts := make([]string, 0, len(sts))
		for _, st := range sts {
			parts = append(parts, st.String())
		}
		leaves[i] = &MerkleNode{Hash: hashParts(parts...), Bucket: i}
	}
	level := leaves
	for len(level) > 1 {
		var next []*MerkleNode
		for i := 0; i < len(level); i += Fanout {
			node := &MerkleNode{Bucket: -1}
			parts := make([]string, 0, Fanout)
			for j := i; j < i+Fanout && j < len(level); j++ {
				node.Children = append(node.Children, level[j])
				parts = append(parts, fmt.Sprintf("%x", level[j].Hash))
			}
			node.Hash = hashParts(parts...)
			next = append(next, node)
		}
		level = next
	}
	return level[0]
}

// DiffResult describes the outcome of a depth-by-depth tree comparison.
type DiffResult struct {
	// Buckets are the leaf buckets whose content differs.
	Buckets []int
	// LevelStats[d] is the number of mismatched nodes discovered at depth d.
	LevelStats []int
	// Compared is the total number of node pairs compared.
	Compared int
}

// Diff compares two Merkle trees level by level (breadth-first by depth)
// and returns exactly the leaf buckets that diverge.
func Diff(local, remote *MerkleNode) *DiffResult {
	res := &DiffResult{}
	if local.Hash == remote.Hash {
		return res
	}
	type pair struct{ l, r *MerkleNode }
	queue := []pair{{local, remote}}
	for len(queue) > 0 {
		res.LevelStats = append(res.LevelStats, len(queue))
		var next []pair
		for _, p := range queue {
			res.Compared++
			if len(p.l.Children) == 0 {
				res.Buckets = append(res.Buckets, p.l.Bucket)
				continue
			}
			for i := range p.l.Children {
				lc, rc := p.l.Children[i], p.r.Children[i]
				if lc.Hash != rc.Hash {
					next = append(next, pair{lc, rc})
				}
			}
		}
		queue = next
	}
	sort.Ints(res.Buckets)
	return res
}
