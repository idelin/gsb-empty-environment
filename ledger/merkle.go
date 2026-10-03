package ledger

import "crypto/sha256"

// ProofNode 默克尔路径中的一个兄弟节点。
type ProofNode struct {
	Hash   [32]byte
	IsLeft bool // true 表示兄弟节点在左侧
}

// Proof 针对某一历史版本（前 Version 个区块）的默克尔路径证明。
type Proof struct {
	Index   int
	Version int
	Path    []ProofNode
}

func hashPair(left, right [32]byte) [32]byte {
	h := sha256.New()
	h.Write(left[:])
	h.Write(right[:])
	var sum [32]byte
	copy(sum[:], h.Sum(nil))
	return sum
}

// merkleRoot 计算 leaves 的默克尔根；奇数层复制末尾节点。
// 空集合返回零哈希。
func merkleRoot(leaves [][32]byte) [32]byte {
	if len(leaves) == 0 {
		return [32]byte{}
	}
	level := make([][32]byte, len(leaves))
	copy(level, leaves)
	for len(level) > 1 {
		if len(level)%2 == 1 {
			level = append(level, level[len(level)-1])
		}
		next := make([][32]byte, len(level)/2)
		for i := 0; i < len(level); i += 2 {
			next[i/2] = hashPair(level[i], level[i+1])
		}
		level = next
	}
	return level[0]
}

// buildProof 为 leaves[index] 生成默克尔路径证明。
func buildProof(leaves [][32]byte, index int) []ProofNode {
	var path []ProofNode
	level := make([][32]byte, len(leaves))
	copy(level, leaves)
	idx := index
	for len(level) > 1 {
		if len(level)%2 == 1 {
			level = append(level, level[len(level)-1])
		}
		var sibling [32]byte
		var isLeft bool
		if idx%2 == 0 {
			sibling = level[idx+1]
			isLeft = false
		} else {
			sibling = level[idx-1]
			isLeft = true
		}
		path = append(path, ProofNode{Hash: sibling, IsLeft: isLeft})
		next := make([][32]byte, len(level)/2)
		for i := 0; i < len(level); i += 2 {
			next[i/2] = hashPair(level[i], level[i+1])
		}
		level = next
		idx /= 2
	}
	return path
}

// VerifyProof 校验 leaf 在 root 所代表版本中的默克尔路径证明。
func VerifyProof(leaf [32]byte, proof Proof, root [32]byte) bool {
	if proof.Index < 0 || proof.Index >= proof.Version {
		return false
	}
	computed := leaf
	idx := proof.Index
	for _, node := range proof.Path {
		if node.IsLeft {
			computed = hashPair(node.Hash, computed)
		} else {
			computed = hashPair(computed, node.Hash)
		}
		idx /= 2
	}
	return computed == root
}
