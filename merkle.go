package anchor

import "crypto/sha256"

// 域分离前缀，防止叶哈希与内部节点哈希混用造成二阶原像攻击。
const (
	leafPrefix = 0x00
	nodePrefix = 0x01
	emptyRootV = 0x02
)

// HashLeaf 计算数据叶子的哈希：H(0x00 || data)。
func HashLeaf(data []byte) [32]byte {
	buf := make([]byte, 0, 1+len(data))
	buf = append(buf, leafPrefix)
	buf = append(buf, data...)
	return sha256.Sum256(buf)
}

// hashNode 计算内部节点哈希：H(0x01 || left || right)。
func hashNode(left, right [32]byte) [32]byte {
	var buf [65]byte
	buf[0] = nodePrefix
	copy(buf[1:33], left[:])
	copy(buf[33:65], right[:])
	return sha256.Sum256(buf[:])
}

// EmptyRoot 是空树的约定根。
func EmptyRoot() [32]byte {
	return sha256.Sum256([]byte{emptyRootV})
}

// ProofNode 是默克尔包含证明中的一个兄弟节点。
// Left 为 true 表示兄弟哈希在路径节点的左侧。
type ProofNode struct {
	Hash [32]byte
	Left bool
}

// MerkleTree 由一组数据项构建的默克尔树。
type MerkleTree struct {
	leaves [][32]byte
	levels [][][32]byte // levels[0] 为叶子层，最后一层为根
}

// NewMerkleTree 构建默克尔树。奇数层节点复制最后一个节点补齐。
func NewMerkleTree(data [][]byte) *MerkleTree {
	t := &MerkleTree{}
	if len(data) == 0 {
		t.levels = [][][32]byte{{EmptyRoot()}}
		return t
	}
	level := make([][32]byte, len(data))
	t.leaves = make([][32]byte, len(data))
	for i, d := range data {
		level[i] = HashLeaf(d)
		t.leaves[i] = level[i]
	}
	t.levels = append(t.levels, level)
	for len(level) > 1 {
		next := make([][32]byte, 0, (len(level)+1)/2)
		for i := 0; i < len(level); i += 2 {
			right := level[i]
			if i+1 < len(level) {
				right = level[i+1]
			}
			next = append(next, hashNode(level[i], right))
		}
		level = next
		t.levels = append(t.levels, level)
	}
	return t
}

// Root 返回默克尔根。
func (t *MerkleTree) Root() [32]byte {
	return t.levels[len(t.levels)-1][0]
}

// LeafCount 返回叶子数量。
func (t *MerkleTree) LeafCount() int {
	return len(t.leaves)
}

// Proof 为第 index 个叶子生成包含证明。
func (t *MerkleTree) Proof(index int) ([]ProofNode, bool) {
	if index < 0 || index >= len(t.leaves) {
		return nil, false
	}
	var proof []ProofNode
	idx := index
	for level := 0; level < len(t.levels)-1; level++ {
		nodes := t.levels[level]
		if idx%2 == 0 {
			sib := idx + 1
			if sib >= len(nodes) {
				sib = idx // 奇数补齐：兄弟即自身
			}
			proof = append(proof, ProofNode{Hash: nodes[sib], Left: false})
		} else {
			proof = append(proof, ProofNode{Hash: nodes[idx-1], Left: true})
		}
		idx /= 2
	}
	return proof, true
}

// VerifyProof 由叶子哈希与证明路径重算根，并与 expectedRoot 比对。
func VerifyProof(leafHash [32]byte, proof []ProofNode, expectedRoot [32]byte) bool {
	cur := leafHash
	for _, node := range proof {
		if node.Left {
			cur = hashNode(node.Hash, cur)
		} else {
			cur = hashNode(cur, node.Hash)
		}
	}
	return cur == expectedRoot
}
