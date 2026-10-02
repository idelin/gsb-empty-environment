// Package mpt 实现带范围摘要的可验证前缀树（Merkle Patricia Trie）。
//
// 结构：Extension Node / Leaf Node 做路径压缩，Branch Node 做 16 路分叉。
// 每个节点携带子树范围摘要（MinKey / MaxKey / Count），并与其子节点哈希
// 一起纳入自身哈希承诺；任何节点变更都会级联改变根哈希。
package mpt

import (
	"crypto/sha256"
	"encoding/binary"
)

const branchWidth = 16

type node interface{}

// Leaf 叶子节点：Path 为压缩后的剩余路径，Key 为完整键（用于范围摘要与前缀检索）。
type Leaf struct {
	Path  []byte
	Key   string
	Value []byte
}

// Extension 扩展节点：压缩的共享路径，指向唯一子节点。
type Extension struct {
	Path  []byte
	Child node
	summary
}

// Branch 分支节点：16 路分叉；当某键恰好终止于此处时携带值。
type Branch struct {
	Children [branchWidth]node
	HasValue bool
	Key      string
	Value    []byte
	summary
}

// summary 范围摘要：子树内最小键、最大键与键数量，纳入节点哈希承诺。
type summary struct {
	MinKey string
	MaxKey string
	Count  int
}

func nodeSummary(n node) summary {
	switch x := n.(type) {
	case *Leaf:
		return summary{MinKey: x.Key, MaxKey: x.Key, Count: 1}
	case *Extension:
		return x.summary
	case *Branch:
		return x.summary
	}
	return summary{}
}

// Trie Merkle Patricia Trie。
type Trie struct {
	root node
}

// New 创建空树。
func New() *Trie { return &Trie{} }

// emptyRoot 为空树的确定性根哈希。
var emptyRoot = hashBytes([]byte("MPT-EMPTY-ROOT"))

// RootHash 返回当前根哈希；任何节点变更都会级联改变它。
func (t *Trie) RootHash() []byte {
	if t.root == nil {
		return emptyRoot
	}
	return hashNode(t.root)
}

// RangeSummary 返回整棵树的范围摘要（最小键、最大键、键数量）。
func (t *Trie) RangeSummary() (min, max string, count int) {
	s := nodeSummary(t.root)
	return s.MinKey, s.MaxKey, s.Count
}

// Put 插入或更新键值对，并沿路径级联刷新摘要与哈希。
func (t *Trie) Put(key string, value []byte) {
	t.root = t.insert(t.root, keyToNibbles(key), key, value)
}

func (t *Trie) insert(n node, key []byte, fullKey string, value []byte) node {
	switch x := n.(type) {
	case nil:
		return &Leaf{Path: append([]byte{}, key...), Key: fullKey, Value: value}

	case *Leaf:
		common := commonPrefix(x.Path, key)
		if common == len(x.Path) && common == len(key) {
			x.Value = value // 同键更新
			return x
		}
		// 路径分叉：新建分支节点，双方各自挂到对应槽位。
		branch := &Branch{}
		if rest := x.Path[common:]; len(rest) == 0 {
			branch.HasValue, branch.Key, branch.Value = true, x.Key, x.Value
		} else {
			branch.Children[rest[0]] = &Leaf{Path: rest[1:], Key: x.Key, Value: x.Value}
		}
		if rest := key[common:]; len(rest) == 0 {
			branch.HasValue, branch.Key, branch.Value = true, fullKey, value
		} else {
			branch.Children[rest[0]] = &Leaf{Path: rest[1:], Key: fullKey, Value: value}
		}
		updateSummary(branch)
		if common > 0 {
			// 共享前缀收缩进 Extension 节点。
			ext := &Extension{Path: append([]byte{}, x.Path[:common]...), Child: branch}
			updateSummary(ext)
			return ext
		}
		return branch

	case *Extension:
		common := commonPrefix(x.Path, key)
		if common == len(x.Path) {
			x.Child = t.insert(x.Child, key[common:], fullKey, value)
			updateSummary(x)
			return x
		}
		// 在共享路径中间分叉：拆分 Extension。
		branch := &Branch{}
		extRest := x.Path[common:]
		if len(extRest) == 1 {
			branch.Children[extRest[0]] = x.Child
		} else {
			mid := &Extension{Path: append([]byte{}, extRest[1:]...), Child: x.Child}
			updateSummary(mid)
			branch.Children[extRest[0]] = mid
		}
		if rest := key[common:]; len(rest) == 0 {
			branch.HasValue, branch.Key, branch.Value = true, fullKey, value
		} else {
			branch.Children[rest[0]] = t.insert(nil, rest[1:], fullKey, value)
		}
		updateSummary(branch)
		if common > 0 {
			ext := &Extension{Path: append([]byte{}, x.Path[:common]...), Child: branch}
			updateSummary(ext)
			return ext
		}
		return branch

	case *Branch:
		if len(key) == 0 {
			x.HasValue, x.Key, x.Value = true, fullKey, value
		} else {
			x.Children[key[0]] = t.insert(x.Children[key[0]], key[1:], fullKey, value)
		}
		updateSummary(x)
		return x
	}
	return nil
}

func updateSummary(n node) {
	switch x := n.(type) {
	case *Extension:
		x.summary = nodeSummary(x.Child)
	case *Branch:
		s := summary{}
		first := true
		if x.HasValue {
			s.MinKey, s.MaxKey, s.Count = x.Key, x.Key, 1
			first = false
		}
		for _, c := range x.Children {
			if c == nil {
				continue
			}
			cs := nodeSummary(c)
			if first || cs.MinKey < s.MinKey {
				s.MinKey = cs.MinKey
			}
			if first || cs.MaxKey > s.MaxKey {
				s.MaxKey = cs.MaxKey
			}
			s.Count += cs.Count
			first = false
		}
		x.summary = s
	}
}

// Get 按键精确检索。
func (t *Trie) Get(key string) ([]byte, bool) {
	nib := keyToNibbles(key)
	n := t.root
	for n != nil {
		switch x := n.(type) {
		case *Leaf:
			if equalNib(x.Path, nib) {
				return x.Value, true
			}
			return nil, false
		case *Extension:
			if len(nib) < len(x.Path) || !equalNib(x.Path, nib[:len(x.Path)]) {
				return nil, false
			}
			nib = nib[len(x.Path):]
			n = x.Child
		case *Branch:
			if len(nib) == 0 {
				if x.HasValue {
					return x.Value, true
				}
				return nil, false
			}
			n = x.Children[nib[0]]
			nib = nib[1:]
		}
	}
	return nil, false
}

// PrefixSearch 按键前缀检索，返回该前缀下全部键值对。
func (t *Trie) PrefixSearch(prefix string) map[string][]byte {
	nib := keyToNibbles(prefix)
	n := t.root
	for n != nil && len(nib) > 0 {
		switch x := n.(type) {
		case *Leaf:
			if len(nib) <= len(x.Path) && equalNib(nib, x.Path[:len(nib)]) {
				return map[string][]byte{x.Key: x.Value}
			}
			return nil
		case *Extension:
			if len(nib) <= len(x.Path) {
				if equalNib(nib, x.Path[:len(nib)]) {
					return collect(x)
				}
				return nil
			}
			if !equalNib(x.Path, nib[:len(x.Path)]) {
				return nil
			}
			nib = nib[len(x.Path):]
			n = x.Child
		case *Branch:
			n = x.Children[nib[0]]
			nib = nib[1:]
		}
	}
	if n == nil {
		return nil
	}
	return collect(n)
}

// PrefixSummary 返回某前缀子树的范围摘要。
func (t *Trie) PrefixSummary(prefix string) (min, max string, count int, ok bool) {
	nib := keyToNibbles(prefix)
	n := t.root
	for n != nil && len(nib) > 0 {
		switch x := n.(type) {
		case *Leaf:
			if len(nib) <= len(x.Path) && equalNib(nib, x.Path[:len(nib)]) {
				return x.Key, x.Key, 1, true
			}
			return "", "", 0, false
		case *Extension:
			if len(nib) <= len(x.Path) {
				if equalNib(nib, x.Path[:len(nib)]) {
					return x.MinKey, x.MaxKey, x.Count, true
				}
				return "", "", 0, false
			}
			if !equalNib(x.Path, nib[:len(x.Path)]) {
				return "", "", 0, false
			}
			nib = nib[len(x.Path):]
			n = x.Child
		case *Branch:
			n = x.Children[nib[0]]
			nib = nib[1:]
		}
	}
	if n == nil {
		return "", "", 0, false
	}
	s := nodeSummary(n)
	return s.MinKey, s.MaxKey, s.Count, true
}

func collect(n node) map[string][]byte {
	out := map[string][]byte{}
	var walk func(node)
	walk = func(nd node) {
		switch x := nd.(type) {
		case *Leaf:
			out[x.Key] = x.Value
		case *Extension:
			walk(x.Child)
		case *Branch:
			if x.HasValue {
				out[x.Key] = x.Value
			}
			for _, c := range x.Children {
				if c != nil {
					walk(c)
				}
			}
		}
	}
	walk(n)
	return out
}

// ---- 哈希承诺 ----

// hashBytes 对若干字节片段做带长度前缀的 SHA-256，防止拼接歧义。
func hashBytes(parts ...[]byte) []byte {
	h := sha256.New()
	var lenBuf [8]byte
	for _, p := range parts {
		binary.LittleEndian.PutUint64(lenBuf[:], uint64(len(p)))
		h.Write(lenBuf[:])
		h.Write(p)
	}
	return h.Sum(nil)
}

// hashNode 计算节点的默克尔哈希：类型标签 + 压缩路径 + 子节点哈希 + 范围摘要。
func hashNode(n node) []byte {
	return elemHash(toElem(n))
}
