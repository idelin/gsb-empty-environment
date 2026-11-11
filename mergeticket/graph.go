package mergeticket

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"sort"
	"sync"
)

// Hash 是提交图中内容寻址对象的标识（SHA-256 的截断十六进制形式）。
type Hash string

// Commit 是内存提交图中的一个提交。
// 提交的哈希同时取决于父提交与树，因此两棵内容相同的树只要挂在不同
// 父提交下，就会得到两个不同的提交哈希——这正是"相同树不同父提交"
// 场景下票据不能互换的根源。
type Commit struct {
	ID     Hash
	Tree   Hash
	Parent Hash // 零值表示根提交
}

// Graph 是一个线程安全的内存提交图，用内容寻址方式存储提交与树。
type Graph struct {
	mu      sync.RWMutex
	commits map[Hash]Commit
	trees   map[Hash]struct{}
	refs    map[string]Hash // 分支/基线名 -> 提交哈希
}

// NewGraph 创建空的内存提交图。
func NewGraph() *Graph {
	return &Graph{
		commits: make(map[Hash]Commit),
		trees:   make(map[Hash]struct{}),
		refs:    make(map[string]Hash),
	}
}

// digest 将任意字节归纳为一个 Hash。
func digest(prefix string, parts ...[]byte) Hash {
	h := sha256.New()
	h.Write([]byte(prefix))
	for _, p := range parts {
		var lenBuf [8]byte
		n := len(p)
		for i := 7; i >= 0; i-- {
			lenBuf[i] = byte(n)
			n >>= 8
		}
		h.Write(lenBuf[:])
		h.Write(p)
	}
	return Hash(hex.EncodeToString(h.Sum(nil)[:16]))
}

// TreeHash 模拟 Git 的树对象哈希：对文件名排序后归纳。
func TreeHash(files map[string]string) Hash {
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)
	h := sha256.New()
	h.Write([]byte("tree\n"))
	for _, name := range names {
		h.Write([]byte(name))
		h.Write([]byte{0})
		h.Write([]byte(files[name]))
		h.Write([]byte{0})
	}
	return Hash(hex.EncodeToString(h.Sum(nil)[:16]))
}

// commitHash 由父提交与树共同决定提交哈希。
func commitHash(parent, tree Hash) Hash {
	return digest("commit", []byte(parent), []byte(tree))
}

// AddTree 登记一棵已存在的树，返回其哈希（幂等）。
func (g *Graph) AddTree(files map[string]string) Hash {
	tree := TreeHash(files)
	g.mu.Lock()
	g.trees[tree] = struct{}{}
	g.mu.Unlock()
	return tree
}

// AddCommit 在给定父提交上登记一个指向 tree 的提交，返回提交哈希（幂等）。
// 父提交为零值表示根提交。父提交与树必须已存在于图中。
func (g *Graph) AddCommit(parent, tree Hash) (Hash, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if parent != "" {
		if _, ok := g.commits[parent]; !ok {
			return "", errors.New("mergeticket: unknown parent commit")
		}
	}
	if _, ok := g.trees[tree]; !ok {
		return "", errors.New("mergeticket: unknown tree")
	}
	id := commitHash(parent, tree)
	g.commits[id] = Commit{ID: id, Tree: tree, Parent: parent}
	return id, nil
}

// Lookup 返回提交的副本。
func (g *Graph) Lookup(id Hash) (Commit, error) {
	g.mu.RLock()
	defer g.mu.RUnlock()
	c, ok := g.commits[id]
	if !ok {
		return Commit{}, errors.New("mergeticket: unknown commit")
	}
	return c, nil
}

// HasCommit 报告提交是否存在。
func (g *Graph) HasCommit(id Hash) bool {
	g.mu.RLock()
	defer g.mu.RUnlock()
	_, ok := g.commits[id]
	return ok
}

// SetRef 以快进方式移动命名引用（分支/基线）。目标提交必须已存在。
func (g *Graph) SetRef(name string, target Hash) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if _, ok := g.commits[target]; !ok {
		return errors.New("mergeticket: unknown commit")
	}
	if cur, ok := g.refs[name]; ok && cur != target && !g.isAncestorLocked(cur, target) {
		return errors.New("mergeticket: non-fast-forward ref update")
	}
	g.refs[name] = target
	return nil
}

// Ref 返回命名引用当前指向的提交；不存在时返回零值。
func (g *Graph) Ref(name string) Hash {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.refs[name]
}

// IsAncestor 报告 ancestor 是否为 descendant 的祖先（同一提交也算）。
func (g *Graph) IsAncestor(ancestor, descendant Hash) bool {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.isAncestorLocked(ancestor, descendant)
}

func (g *Graph) isAncestorLocked(ancestor, descendant Hash) bool {
	cur := descendant
	for cur != "" {
		if cur == ancestor {
			return true
		}
		c, ok := g.commits[cur]
		if !ok {
			return false
		}
		cur = c.Parent
	}
	return false
}
