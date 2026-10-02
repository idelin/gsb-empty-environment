package auditledger

import (
	"crypto/sha256"
	"errors"
)

// leafHash 与 nodeHash 使用不同的域分离前缀（0x00 / 0x01），
// 防止将叶子伪装成内部节点的第二原像攻击。
func leafHash(h [32]byte) [32]byte {
	buf := make([]byte, 0, 1+32)
	buf = append(buf, 0x00)
	buf = append(buf, h[:]...)
	return sha256.Sum256(buf)
}

func nodeHash(left, right [32]byte) [32]byte {
	buf := make([]byte, 0, 1+64)
	buf = append(buf, 0x01)
	buf = append(buf, left[:]...)
	buf = append(buf, right[:]...)
	return sha256.Sum256(buf)
}

// MerkleRoot 计算一组叶子（此处为区块哈希）的默克尔根。
// 奇数节点直接向上晋升，不复制。空集合返回零哈希。
func MerkleRoot(leaves [][32]byte) [32]byte {
	if len(leaves) == 0 {
		return [32]byte{}
	}
	level := make([][32]byte, len(leaves))
	for i, l := range leaves {
		level[i] = leafHash(l)
	}
	for len(level) > 1 {
		next := make([][32]byte, 0, (len(level)+1)/2)
		for i := 0; i < len(level); i += 2 {
			if i+1 == len(level) {
				next = append(next, level[i])
			} else {
				next = append(next, nodeHash(level[i], level[i+1]))
			}
		}
		level = next
	}
	return level[0]
}

// ProofNode 是默克尔路径上的一个兄弟节点。
type ProofNode struct {
	Hash     [32]byte `json:"hash"`
	FromLeft bool     `json:"from_left"` // true 表示兄弟节点在左侧
}

// Proof 是某历史版本下某个区块的默克尔路径证明。
type Proof struct {
	Index uint64      `json:"index"` // 区块在账簿中的序号
	Leaf  [32]byte    `json:"leaf"`  // 叶子哈希 = leafHash(区块哈希)
	Path  []ProofNode `json:"path"`
}

// BuildProof 为 leaves[index] 生成默克尔路径证明。
func BuildProof(leaves [][32]byte, index uint64) (Proof, error) {
	if index >= uint64(len(leaves)) {
		return Proof{}, errors.New("auditledger: proof index out of range")
	}
	p := Proof{Index: index, Leaf: leafHash(leaves[index])}
	level := make([][32]byte, len(leaves))
	for i, l := range leaves {
		level[i] = leafHash(l)
	}
	idx := index
	for len(level) > 1 {
		if idx%2 == 0 {
			if idx+1 < uint64(len(level)) {
				p.Path = append(p.Path, ProofNode{Hash: level[idx+1], FromLeft: false})
			}
		} else {
			p.Path = append(p.Path, ProofNode{Hash: level[idx-1], FromLeft: true})
		}
		next := make([][32]byte, 0, (len(level)+1)/2)
		for i := 0; i < len(level); i += 2 {
			if i+1 == len(level) {
				next = append(next, level[i])
			} else {
				next = append(next, nodeHash(level[i], level[i+1]))
			}
		}
		level = next
		idx /= 2
	}
	return p, nil
}

// VerifyProof 验证证明 p 是否能推导出给定的默克尔根。
func VerifyProof(p Proof, root [32]byte) bool {
	h := p.Leaf
	for _, n := range p.Path {
		if n.FromLeft {
			h = nodeHash(n.Hash, h)
		} else {
			h = nodeHash(h, n.Hash)
		}
	}
	return h == root
}
