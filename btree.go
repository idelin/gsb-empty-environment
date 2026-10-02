// Package bptree 实现一个基于固定大小页面的 B+ 树，重点演示删除时的
// 级联收缩与物理合并（Merging）：当兄弟节点均无法借调时，将当前页与兄弟页
// 合二为一，从父节点删除索引项；若父节点亦下溢则递归合并，直至根节点收缩。
package bptree

import (
	"fmt"
	"sort"
)

const (
	// MaxKeys 每个页面（叶子/内部）最多容纳的键数。
	MaxKeys = 4
	// MinKeys 非根页面允许的最少键数（下溢阈值），ceil(MaxKeys/2)。
	MinKeys = MaxKeys / 2
	// maxChildren 内部节点最大孩子数（最大扇出）。
	maxChildren = MaxKeys + 1
)

// node 是一个物理页面。叶子页面只含 keys；内部页面含 keys 与 children，
// 满足 len(children) == len(keys)+1，且 keys[i] 为 children[i+1] 子树的最小键。
type node struct {
	isLeaf   bool
	keys     []int
	children []int // 内部节点的孩子页面 ID
}

// pager 模拟磁盘页管理器：页面定长、按 ID 寻址，释放的页面进入空闲链表
// （Free Page List）等待复用。
type pager struct {
	pages    []*node // nil 表示该页已释放
	freeList []int   // 空闲页面 ID 链表（栈式复用）
	frees    int     // 累计释放次数
}

func (p *pager) alloc(n *node) int {
	if m := len(p.freeList); m > 0 {
		id := p.freeList[m-1]
		p.freeList = p.freeList[:m-1]
		p.pages[id] = n
		return id
	}
	p.pages = append(p.pages, n)
	return len(p.pages) - 1
}

func (p *pager) free(id int) {
	p.pages[id] = nil
	p.freeList = append(p.freeList, id)
	p.frees++
}

// active 当前在用的页面数。
func (p *pager) active() int { return len(p.pages) - len(p.freeList) }

// minKey 返回 id 子树中的最小键（最左叶子的首键）。
func (p *pager) minKey(id int) int {
	n := p.pages[id]
	for !n.isLeaf {
		id = n.children[0]
		n = p.pages[id]
	}
	return n.keys[0]
}

// Tree 是一棵 B+ 树。
type Tree struct {
	p      *pager
	root   int
	height int

	Merges  int // 累计物理合并次数
	Borrows int // 累计借调（再分配）次数
}

// groupSizes 把 n 个元素均匀分组成每组不超过 max 个，保证非根分组满足
// 最小填充率（叶子 >= MinKeys，内部 >= MinKeys+1 个孩子）。
func groupSizes(n, max int) []int {
	g := (n + max - 1) / max
	base, extra := n/g, n%g
	sizes := make([]int, g)
	for i := range sizes {
		sizes[i] = base
		if i < extra {
			sizes[i]++
		}
	}
	return sizes
}

// BulkLoad 由一组已排序且无重复的键批量构建一棵“满树”：
// 叶子页尽量装满，内部层逐层按最大扇出聚合。
func BulkLoad(keys []int) *Tree {
	t := &Tree{p: &pager{}}
	if len(keys) == 0 {
		t.root = t.p.alloc(&node{isLeaf: true})
		t.height = 1
		return t
	}
	// 叶子层。
	var level []int
	off := 0
	for _, sz := range groupSizes(len(keys), MaxKeys) {
		leaf := &node{isLeaf: true, keys: append([]int(nil), keys[off:off+sz]...)}
		level = append(level, t.p.alloc(leaf))
		off += sz
	}
	t.height = 1
	// 逐层构建内部节点，直到收敛为根。
	for len(level) > 1 {
		var next []int
		off := 0
		for _, sz := range groupSizes(len(level), maxChildren) {
			children := append([]int(nil), level[off:off+sz]...)
			in := &node{children: children}
			for _, c := range children[1:] {
				in.keys = append(in.keys, t.p.minKey(c))
			}
			next = append(next, t.p.alloc(in))
			off += sz
		}
		level = next
		t.height++
	}
	t.root = level[0]
	return t
}

// Height 返回树高（叶子层为 1）。
func (t *Tree) Height() int { return t.height }

// ActivePages 返回当前在用的页面数。
func (t *Tree) ActivePages() int { return t.p.active() }

// FreeListLen 返回空闲链表中的页面数。
func (t *Tree) FreeListLen() int { return len(t.p.freeList) }

// TotalFrees 返回累计释放的页面数。
func (t *Tree) TotalFrees() int { return t.p.frees }

// Get 判断 key 是否存在。
func (t *Tree) Get(key int) bool {
	id := t.root
	for {
		n := t.p.pages[id]
		if n.isLeaf {
			i := sort.Search(len(n.keys), func(i int) bool { return n.keys[i] >= key })
			return i < len(n.keys) && n.keys[i] == key
		}
		id = n.children[childIndex(n, key)]
	}
}

// childIndex 返回 key 应落入的孩子下标。
func childIndex(n *node, key int) int {
	return sort.Search(len(n.keys), func(i int) bool { return key < n.keys[i] })
}

// Delete 删除 key；返回是否找到。删除触发下溢时先尝试向兄弟借调，
// 兄弟均无法借调则物理合并，并向上递归直至根节点收缩。
func (t *Tree) Delete(key int) bool {
	if !t.deleteFrom(t.root, key) {
		return false
	}
	// 根节点收缩：内部根只剩一个孩子时，孩子升格为新根，旧根页回收。
	for {
		r := t.p.pages[t.root]
		if r.isLeaf || len(r.children) > 1 {
			break
		}
		old := t.root
		t.root = r.children[0]
		t.p.free(old)
		t.height--
	}
	return true
}

// deleteFrom 在 id 子树中删除 key；返回后若该子树下溢，由调用方（父节点）
// 负责借调或合并。
func (t *Tree) deleteFrom(id, key int) bool {
	n := t.p.pages[id]
	if n.isLeaf {
		i := sort.Search(len(n.keys), func(i int) bool { return n.keys[i] >= key })
		if i >= len(n.keys) || n.keys[i] != key {
			return false
		}
		n.keys = append(n.keys[:i], n.keys[i+1:]...)
		return true
	}
	idx := childIndex(n, key)
	childID := n.children[idx]
	if !t.deleteFrom(childID, key) {
		return false
	}
	// 删除可能使该子树的最小键增大，刷新父节点中对应的索引项；
	// 若本节点恰是父节点的最左孩子，该变化会在回溯时逐层向上传播。
	if idx > 0 {
		n.keys[idx-1] = t.p.minKey(childID)
	}
	child := t.p.pages[childID]
	if len(child.keys) >= MinKeys {
		return true // 孩子未下溢，无需修复
	}
	t.rebalance(n, idx, childID, child)
	return true
}

// rebalance 修复下溢的孩子 child（parent 的第 idx 个孩子）：
// 优先从键数富余的兄弟借调；兄弟均处于最小填充时执行物理合并，
// 从父节点删除对应索引项，被清空的兄弟页回收至空闲链表。
func (t *Tree) rebalance(parent *node, idx, childID int, child *node) {
	p := t.p
	// 1) 尝试从左兄弟借调。
	if idx > 0 {
		if left := p.pages[parent.children[idx-1]]; len(left.keys) > MinKeys {
			if child.isLeaf {
				child.keys = append([]int{left.keys[len(left.keys)-1]}, child.keys...)
				left.keys = left.keys[:len(left.keys)-1]
				parent.keys[idx-1] = child.keys[0]
			} else {
				child.children = append([]int{left.children[len(left.children)-1]}, child.children...)
				left.children = left.children[:len(left.children)-1]
				child.keys = append([]int{parent.keys[idx-1]}, child.keys...)
				parent.keys[idx-1] = left.keys[len(left.keys)-1]
				left.keys = left.keys[:len(left.keys)-1]
			}
			t.Borrows++
			return
		}
	}
	// 2) 尝试从右兄弟借调。
	if idx < len(parent.children)-1 {
		if right := p.pages[parent.children[idx+1]]; len(right.keys) > MinKeys {
			if child.isLeaf {
				child.keys = append(child.keys, right.keys[0])
				right.keys = right.keys[1:]
				parent.keys[idx] = right.keys[0]
			} else {
				child.children = append(child.children, right.children[0])
				right.children = right.children[1:]
				child.keys = append(child.keys, parent.keys[idx])
				parent.keys[idx] = right.keys[0]
				right.keys = right.keys[1:]
			}
			t.Borrows++
			return
		}
	}
	// 3) 物理合并：兄弟均无法借调，合二为一，父节点删除索引项。
	t.Merges++
	if idx > 0 {
		// 并入左兄弟，释放当前页。
		left := p.pages[parent.children[idx-1]]
		if child.isLeaf {
			left.keys = append(left.keys, child.keys...)
		} else {
			left.keys = append(left.keys, parent.keys[idx-1])
			left.keys = append(left.keys, child.keys...)
			left.children = append(left.children, child.children...)
		}
		parent.keys = append(parent.keys[:idx-1], parent.keys[idx:]...)
		parent.children = append(parent.children[:idx], parent.children[idx+1:]...)
		p.free(childID)
	} else {
		// 无左兄弟：右兄弟并入当前页，释放右兄弟页。
		rightID := parent.children[idx+1]
		right := p.pages[rightID]
		if child.isLeaf {
			child.keys = append(child.keys, right.keys...)
		} else {
			child.keys = append(child.keys, parent.keys[idx])
			child.keys = append(child.keys, right.keys...)
			child.children = append(child.children, right.children...)
		}
		parent.keys = append(parent.keys[:idx], parent.keys[idx+1:]...)
		parent.children = append(parent.children[:idx+1], parent.children[idx+2:]...)
		p.free(rightID)
	}
}

// Keys 按键升序返回树中全部键（中序遍历）。
func (t *Tree) Keys() []int {
	var out []int
	var walk func(id int)
	walk = func(id int) {
		n := t.p.pages[id]
		if n.isLeaf {
			out = append(out, n.keys...)
			return
		}
		for _, c := range n.children {
			walk(c)
		}
	}
	walk(t.root)
	return out
}

// Validate 校验树的结构不变量：键有序、填充率、索引项与子树最小键一致、
// 所有叶子位于同一层且等于树高。返回 nil 表示合法。
func (t *Tree) Validate() error {
	_, _, err := t.validateNode(t.root, 1, true)
	return err
}

func (t *Tree) validateNode(id, depth int, isRoot bool) (lo, hi int, err error) {
	n := t.p.pages[id]
	if n == nil {
		return 0, 0, fmt.Errorf("page %d is nil", id)
	}
	for i := 1; i < len(n.keys); i++ {
		if n.keys[i-1] >= n.keys[i] {
			return 0, 0, fmt.Errorf("page %d: keys not strictly increasing", id)
		}
	}
	if !isRoot && (len(n.keys) < MinKeys || len(n.keys) > MaxKeys) {
		return 0, 0, fmt.Errorf("page %d: occupancy %d out of [%d,%d]", id, len(n.keys), MinKeys, MaxKeys)
	}
	if n.isLeaf {
		if depth != t.height {
			return 0, 0, fmt.Errorf("page %d: leaf at depth %d, tree height %d", id, depth, t.height)
		}
		if len(n.keys) == 0 {
			return 0, 0, fmt.Errorf("page %d: empty leaf", id)
		}
		return n.keys[0], n.keys[len(n.keys)-1], nil
	}
	if len(n.children) != len(n.keys)+1 {
		return 0, 0, fmt.Errorf("page %d: %d keys but %d children", id, len(n.keys), len(n.children))
	}
	lo, hi = 0, 0
	for i, c := range n.children {
		clo, chi, err := t.validateNode(c, depth+1, false)
		if err != nil {
			return 0, 0, err
		}
		if i == 0 {
			lo = clo
		} else {
			if n.keys[i-1] != clo {
				return 0, 0, fmt.Errorf("page %d: separator %d != min key %d of child %d", id, n.keys[i-1], clo, c)
			}
		}
		if i > 0 && chi <= hi {
			return 0, 0, fmt.Errorf("page %d: child key ranges overlap", id)
		}
		hi = chi
	}
	return lo, hi, nil
}
