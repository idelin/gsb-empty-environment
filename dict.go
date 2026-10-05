// Package fuzzdict 提供带权模糊词典检索：
// 支持插入、删除、替换、相邻交换四种编辑操作，
// 每种操作的代价由调用方给出的非负整数决定。
package fuzzdict

// childEntry 是字典树的一条出边，按 ch 升序存放。
type childEntry struct {
	ch byte
	nd *trieNode
}

// trieNode 是字典树节点。terminal 为 true 时 word 保存完整词条。
type trieNode struct {
	children []childEntry
	terminal bool
	word     string
}

// Dictionary 是基于共享前缀字典树的静态词典。
type Dictionary struct {
	root *trieNode
	size int
}

// New 用给定词条构建词典，重复词条只保留一份。
func New(words []string) *Dictionary {
	root := &trieNode{}
	seen := make(map[string]bool, len(words))
	size := 0
	for _, w := range words {
		if seen[w] {
			continue
		}
		seen[w] = true
		size++
		nd := root
		for i := 0; i < len(w); i++ {
			nd = nd.child(w[i])
		}
		nd.terminal = true
		nd.word = w
	}
	return &Dictionary{root: root, size: size}
}

// Len 返回词典中不同词条的数量。
func (d *Dictionary) Len() int { return d.size }

// child 返回 ch 对应的子节点，不存在则按序插入。
func (n *trieNode) child(ch byte) *trieNode {
	lo, hi := 0, len(n.children)
	for lo < hi {
		mid := (lo + hi) / 2
		if n.children[mid].ch < ch {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	if lo < len(n.children) && n.children[lo].ch == ch {
		return n.children[lo].nd
	}
	nd := &trieNode{}
	n.children = append(n.children, childEntry{})
	copy(n.children[lo+1:], n.children[lo:])
	n.children[lo] = childEntry{ch: ch, nd: nd}
	return nd
}
