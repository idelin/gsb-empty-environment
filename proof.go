package mpt

import (
	"bytes"
	"strconv"
)

// 节点类型标签。
const (
	kindLeaf      = "leaf"
	kindExtension = "extension"
	kindBranch    = "branch"
)

// ProofElem 证明中的单个节点：包含重算其哈希所需的全部字段。
type ProofElem struct {
	Kind     string              // leaf / extension / branch
	Path     []byte              // leaf/extension：Hex-Prefix 紧凑编码路径
	Digest   []byte              // extension：子节点哈希
	Children [branchWidth][]byte // branch：各槽位子节点哈希（空槽为 nil）
	HasValue bool                // branch：是否有键终止于此
	Key      string              // leaf 的完整键 / branch 终止键
	Value    []byte
	MinKey   string // 范围摘要
	MaxKey   string
	Count    int
}

// toElem 将内部节点转换为可序列化的证明元素。
func toElem(n node) ProofElem {
	switch x := n.(type) {
	case *Leaf:
		return ProofElem{
			Kind: kindLeaf, Path: compactEncode(x.Path, true),
			Key: x.Key, Value: x.Value,
			MinKey: x.Key, MaxKey: x.Key, Count: 1,
		}
	case *Extension:
		return ProofElem{
			Kind: kindExtension, Path: compactEncode(x.Path, false),
			Digest: hashNode(x.Child),
			MinKey: x.MinKey, MaxKey: x.MaxKey, Count: x.Count,
		}
	case *Branch:
		e := ProofElem{
			Kind:     kindBranch,
			HasValue: x.HasValue, Key: x.Key, Value: x.Value,
			MinKey: x.MinKey, MaxKey: x.MaxKey, Count: x.Count,
		}
		for i, c := range x.Children {
			if c != nil {
				e.Children[i] = hashNode(c)
			}
		}
		return e
	}
	return ProofElem{}
}

// elemHash 由证明元素重算节点哈希，与 hashNode 保持一致。
func elemHash(e ProofElem) []byte {
	switch e.Kind {
	case kindLeaf:
		return hashBytes([]byte{0}, e.Path, []byte(e.Key), e.Value)
	case kindExtension:
		return hashBytes([]byte{1}, e.Path, e.Digest,
			[]byte(e.MinKey), []byte(e.MaxKey), []byte(strconv.Itoa(e.Count)))
	case kindBranch:
		parts := [][]byte{{2}}
		parts = append(parts, e.Children[:]...)
		v := []byte{}
		if e.HasValue {
			v = append([]byte{1}, e.Value...)
		}
		parts = append(parts, v,
			[]byte(e.MinKey), []byte(e.MaxKey), []byte(strconv.Itoa(e.Count)))
		return hashBytes(parts...)
	}
	return nil
}

// Hash 返回证明元素的哈希。
func (e ProofElem) Hash() []byte { return elemHash(e) }

// Prove 生成从根到目标键的完整节点序列证明。
// 返回的布尔值表示键是否存在；不存在时证明在偏离处截断。
func (t *Trie) Prove(key string) ([]ProofElem, bool) {
	nib := keyToNibbles(key)
	var proof []ProofElem
	n := t.root
	for n != nil {
		proof = append(proof, toElem(n))
		switch x := n.(type) {
		case *Leaf:
			return proof, equalNib(x.Path, nib)
		case *Extension:
			if len(nib) < len(x.Path) || !equalNib(x.Path, nib[:len(x.Path)]) {
				return proof, false
			}
			nib = nib[len(x.Path):]
			n = x.Child
		case *Branch:
			if len(nib) == 0 {
				return proof, x.HasValue
			}
			n = x.Children[nib[0]]
			nib = nib[1:]
		}
	}
	return proof, false
}

// VerifyProof 针对根哈希、键、期望值验证节点序列证明：
//  1. 首个元素哈希必须等于根哈希；
//  2. 每一级与其父级引用的子哈希必须吻合（篡改父级哈希即断链）；
//  3. 键必须落在每一级承诺的范围摘要 [MinKey, MaxKey] 内；
//  4. 末级叶子/分支的键与值必须匹配。
func VerifyProof(rootHash []byte, key string, value []byte, proof []ProofElem) bool {
	if len(proof) == 0 {
		return false
	}
	if !bytes.Equal(elemHash(proof[0]), rootHash) {
		return false
	}
	nib := keyToNibbles(key)
	for i, e := range proof {
		if e.Count <= 0 || key < e.MinKey || key > e.MaxKey {
			return false // 范围摘要不含该键，承诺不可信
		}
		switch e.Kind {
		case kindExtension:
			path, leaf := compactDecode(e.Path)
			if leaf || len(nib) < len(path) || !equalNib(path, nib[:len(path)]) {
				return false
			}
			nib = nib[len(path):]
			if i+1 >= len(proof) || !bytes.Equal(elemHash(proof[i+1]), e.Digest) {
				return false
			}
		case kindBranch:
			if len(nib) == 0 {
				return i == len(proof)-1 && e.HasValue && bytes.Equal(e.Value, value)
			}
			if i+1 >= len(proof) {
				return false
			}
			childHash := e.Children[nib[0]]
			if childHash == nil || !bytes.Equal(elemHash(proof[i+1]), childHash) {
				return false
			}
			nib = nib[1:]
		case kindLeaf:
			path, leaf := compactDecode(e.Path)
			if !leaf {
				return false
			}
			return i == len(proof)-1 && equalNib(path, nib) &&
				e.Key == key && bytes.Equal(e.Value, value)
		default:
			return false
		}
	}
	return false
}

// DecodePath 将证明元素中的紧凑路径解码为可读的 nibble 十六进制串，
// 第二个返回值表示该节点是否为叶子。
func DecodePath(e ProofElem) (string, bool) {
	nib, leaf := compactDecode(e.Path)
	return nibblesHex(nib), leaf
}
