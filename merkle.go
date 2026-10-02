package merkleanchor

import "crypto/sha256"

// 域分离前缀，防止叶子与内部节点的哈希发生第二原像混淆。
const (
	domainLeaf = 0x00
	domainNode = 0x01
)

// HashLeaf 计算叶子哈希：SHA256(0x00 || data)。
func HashLeaf(data []byte) [32]byte {
	h := sha256.New()
	h.Write([]byte{domainLeaf})
	h.Write(data)
	var out [32]byte
	copy(out[:], h.Sum(nil))
	return out
}

// hashNode 计算内部节点哈希：SHA256(0x01 || left || right)。
func hashNode(left, right [32]byte) [32]byte {
	h := sha256.New()
	h.Write([]byte{domainNode})
	h.Write(left[:])
	h.Write(right[:])
	var out [32]byte
	copy(out[:], h.Sum(nil))
	return out
}

// MerkleRoot 由叶子哈希列表计算默克尔根。
// 奇数个节点时复制最后一个节点配对（与比特币一致）。
func MerkleRoot(leaves [][32]byte) [32]byte {
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
			next[i/2] = hashNode(level[i], level[i+1])
		}
		level = next
	}
	return level[0]
}

// ProofStep 是默克尔路径中的一步：兄弟哈希及其方位。
type ProofStep struct {
	Sibling [32]byte
	// Left 为 true 表示兄弟在左、当前节点在右。
	Left bool
}

// InclusionProof 从叶子哈希列表中为指定下标生成包含证明。
func InclusionProof(leaves [][32]byte, index int) []ProofStep {
	if index < 0 || index >= len(leaves) {
		return nil
	}
	level := make([][32]byte, len(leaves))
	copy(level, leaves)
	var proof []ProofStep
	idx := index
	for len(level) > 1 {
		if len(level)%2 == 1 {
			level = append(level, level[len(level)-1])
		}
		var step ProofStep
		if idx%2 == 0 {
			step = ProofStep{Sibling: level[idx+1], Left: false}
		} else {
			step = ProofStep{Sibling: level[idx-1], Left: true}
		}
		proof = append(proof, step)
		next := make([][32]byte, len(level)/2)
		for i := 0; i < len(level); i += 2 {
			next[i/2] = hashNode(level[i], level[i+1])
		}
		level = next
		idx /= 2
	}
	return proof
}

// VerifyInclusion 验证 leaf 经 proof 归约后是否等于 root。
func VerifyInclusion(leaf [32]byte, proof []ProofStep, root [32]byte) bool {
	cur := leaf
	for _, step := range proof {
		if step.Left {
			cur = hashNode(step.Sibling, cur)
		} else {
			cur = hashNode(cur, step.Sibling)
		}
	}
	return cur == root
}
