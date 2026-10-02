// Package merkle 实现可验证日志快照：将记录列表组织为一棵
// Fanout 叉默克尔树，支持根哈希比对、包含证明（inclusion proof）
// 以及两棵树的差异下钻定位。
package merkle

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
)

// Fanout 为树的扇出（每个内部节点的最大子节点数）。
// 16 叉树在 100 万条记录时深度仅为 5，差异下钻展开的节点数极少。
const Fanout = 16

// Hash 是一个 SHA-256 哈希值。
type Hash [sha256.Size]byte

// Zero 是“缺失节点”的哨兵值（全零）。
// 真实节点由带域分离前缀的 SHA-256 产生，实践中不会等于 Zero。
var Zero Hash

// ErrIndexOutOfRange 表示请求的记录下标超出快照范围。
var ErrIndexOutOfRange = errors.New("merkle: index out of range")

// Short 返回哈希的短十六进制形式，用于展示。
func (h Hash) Short() string {
	var buf [8]byte
	copy(buf[:], h[:8])
	return hex.EncodeToString(buf[:])
}

// LeafHash 计算叶子哈希：SHA-256(0x00 || record)。
// 0x00 前缀为叶子与内部节点提供域分离（同 RFC 6962）。
func LeafHash(record []byte) Hash {
	h := sha256.New()
	h.Write([]byte{0x00})
	h.Write(record)
	var out Hash
	h.Sum(out[:0])
	return out
}

// nodeHash 计算内部节点哈希：SHA-256(0x01 || children...)。
func nodeHash(children []Hash) Hash {
	h := sha256.New()
	h.Write([]byte{0x01})
	for i := range children {
		h.Write(children[i][:])
	}
	var out Hash
	h.Sum(out[:0])
	return out
}

// Tree 是一棵默克尔树，levels[0] 为叶子层，最后一层仅含根。
type Tree struct {
	levels [][]Hash
	count  int
}

// Build 以 O(n) 时间从记录列表构建默克尔树。
func Build(records [][]byte) *Tree {
	t := &Tree{count: len(records)}
	if len(records) == 0 {
		return t
	}
	leaves := make([]Hash, len(records))
	for i, r := range records {
		leaves[i] = LeafHash(r)
	}
	t.levels = append(t.levels, leaves)
	for len(t.levels[len(t.levels)-1]) > 1 {
		cur := t.levels[len(t.levels)-1]
		n := (len(cur) + Fanout - 1) / Fanout
		next := make([]Hash, n)
		for i := 0; i < n; i++ {
			end := (i + 1) * Fanout
			if end > len(cur) {
				end = len(cur)
			}
			next[i] = nodeHash(cur[i*Fanout : end])
		}
		t.levels = append(t.levels, next)
	}
	return t
}

// Root 返回树根哈希；空树的根为 Zero。
func (t *Tree) Root() Hash {
	if len(t.levels) == 0 {
		return Zero
	}
	top := t.levels[len(t.levels)-1]
	return top[0]
}

// Count 返回树中的记录条数。
func (t *Tree) Count() int { return t.count }

// Height 返回树的层数（含叶子层）；空树为 0。
func (t *Tree) Height() int { return len(t.levels) }

// effectiveHash 返回 (level, idx) 处的节点哈希。
// 超出树范围时按 Zero 处理；当 level 高于本树的根层时，
// 下标 0 处视为根（相当于把较矮的树用零哈希兄弟向上补齐），
// 使两棵高度不同的树可以逐层对齐比较。
func (t *Tree) effectiveHash(level, idx int) Hash {
	if level >= len(t.levels) {
		if idx == 0 {
			return t.Root()
		}
		return Zero
	}
	if idx >= len(t.levels[level]) {
		return Zero
	}
	return t.levels[level][idx]
}

// ProofStep 是包含证明中一层的信息：待证节点在兄弟中的位置，
// 以及其余兄弟节点的哈希（按序）。
type ProofStep struct {
	Position int
	Siblings []Hash
}

// Prove 为下标 index 处的记录生成包含证明。
func (t *Tree) Prove(index uint64) ([]ProofStep, error) {
	if index >= uint64(t.count) {
		return nil, ErrIndexOutOfRange
	}
	var proof []ProofStep
	idx := int(index)
	for level := 0; level < len(t.levels)-1; level++ {
		nodes := t.levels[level]
		start := (idx / Fanout) * Fanout
		end := start + Fanout
		if end > len(nodes) {
			end = len(nodes)
		}
		step := ProofStep{Position: idx - start}
		for i := start; i < end; i++ {
			if i != idx {
				step.Siblings = append(step.Siblings, nodes[i])
			}
		}
		proof = append(proof, step)
		idx /= Fanout
	}
	return proof, nil
}

// Verify 校验 record 的包含证明是否能重算出给定的根哈希。
func Verify(root Hash, record []byte, proof []ProofStep) bool {
	h := LeafHash(record)
	for _, step := range proof {
		if step.Position < 0 || step.Position > len(step.Siblings) {
			return false
		}
		children := make([]Hash, 0, len(step.Siblings)+1)
		children = append(children, step.Siblings[:step.Position]...)
		children = append(children, h)
		children = append(children, step.Siblings[step.Position:]...)
		h = nodeHash(children)
	}
	return h == root
}
