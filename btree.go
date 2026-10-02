// Package latchcrab 实现 B+ 树上的螃蟹脚（Latch Crabbing / Coupling）并发加锁协议。
//
// 协议要点：
//   - 读遍历：自上而下逐层 RLock 子节点，随后立即 RUnlock 父节点（耦合前进），
//     因此读操作在不会分裂的高层节点上无阻塞高速穿越。
//   - 写遍历：自上而下逐层 Lock；下行途中若发现孩子已满，先在父锁保护下
//     将其分裂（top-down 预分裂），使子节点变为“安全”（不会分裂），
//     随后立即释放父节点锁。任意时刻最多持有相邻两层锁，临界区最小。
//   - 所有锁都严格按 根 -> 叶 的固定全局顺序获取，因此协议本身无死锁。
package latchcrab

import (
	"fmt"
	"sort"
	"strings"
	"sync"
)

// node 是 B+ 树的内部节点或叶子节点。
// mu 的锁约定：调用方在访问节点字段前必须已持有该节点的锁。
type node struct {
	mu       sync.RWMutex
	isLeaf   bool
	keys     []int
	children []*node // 仅内部节点使用，len(children) == len(keys)+1
	next     *node   // 仅叶子节点使用，叶子链表（供范围查询）
}

// BTree 是支持并发读写的 B+ 树，并发控制采用螃蟹脚协议。
type BTree struct {
	rootMu sync.RWMutex // 保护 root 指针本身
	root   *node
	order  int // 每个节点最多 order-1 个键、order 个孩子
}

// New 创建一棵阶为 order 的空 B+ 树。order 必须 >= 3。
func New(order int) *BTree {
	if order < 3 {
		panic("latchcrab: order must be >= 3")
	}
	return &BTree{
		order: order,
		root:  &node{isLeaf: true},
	}
}

// Get 查找 key，返回其是否存在。
// 读路径：RLock 子节点后立即 RUnlock 父节点，不在任何祖先上滞留。
func (t *BTree) Get(key int) bool {
	t.rootMu.RLock()
	n := t.root
	n.mu.RLock()
	t.rootMu.RUnlock()

	for !n.isLeaf {
		child := n.children[childIndex(n, key)]
		child.mu.RLock() // 先锁子节点（耦合）
		n.mu.RUnlock()   // 再释放父节点
		n = child
	}
	idx := sort.SearchInts(n.keys, key)
	found := idx < len(n.keys) && n.keys[idx] == key
	n.mu.RUnlock()
	return found
}

// Insert 插入 key，重复键将被忽略。
// 写路径：满孩子先分裂再继续下行，因此子节点总是安全的，
// 父节点锁在锁住子节点后立即释放（耦合前进）。
func (t *BTree) Insert(key int) {
	t.rootMu.RLock()
	n := t.root
	n.mu.Lock()
	t.rootMu.RUnlock()

	// 根节点若已满则分裂。为避免 ABBA 死锁，rootMu 与节点锁的获取顺序
	// 必须始终是 rootMu -> 节点锁，因此这里先释放节点锁再重新按序获取。
	if len(n.keys) == t.order-1 {
		n.mu.Unlock()
		t.rootMu.Lock()
		n = t.root
		n.mu.Lock()
		if len(n.keys) == t.order-1 {
			newRoot := &node{isLeaf: false}
			t.root = newRoot
			newRoot.children = []*node{n}
			t.splitChild(newRoot, 0)
			n.mu.Unlock()
			newRoot.mu.Lock()
			n = newRoot
		}
		t.rootMu.Unlock()
	}

	for !n.isLeaf {
		idx := childIndex(n, key)
		child := n.children[idx]
		child.mu.Lock()
		if len(child.keys) == t.order-1 {
			// 孩子已满：在父锁保护下分裂，中键提升后重新选择孩子。
			t.splitChild(n, idx)
			child.mu.Unlock()
			if key >= n.keys[idx] {
				idx++
			}
			child = n.children[idx]
			child.mu.Lock()
		}
		// 子节点安全（不满，不会分裂），提前释放父节点锁。
		n.mu.Unlock()
		n = child
	}

	// 在叶子上插入（叶子此时必然不满）。
	idx := sort.SearchInts(n.keys, key)
	if idx >= len(n.keys) || n.keys[idx] != key {
		n.keys = append(n.keys, 0)
		copy(n.keys[idx+1:], n.keys[idx:])
		n.keys[idx] = key
	}
	n.mu.Unlock()
}

// splitChild 分裂 n.children[i]（已满），中键提升到 n。
// 调用方必须持有 n 及其孩子的写锁。
func (t *BTree) splitChild(n *node, i int) {
	child := n.children[i]
	mid := len(child.keys) / 2
	sibling := &node{isLeaf: child.isLeaf}

	if child.isLeaf {
		// B+ 树叶子分裂：中键复制到父节点，同时保留在右兄弟中。
		sibling.keys = append(sibling.keys, child.keys[mid:]...)
		child.keys = child.keys[:mid]
		sibling.next = child.next
		child.next = sibling
		n.keys = insertInt(n.keys, i, sibling.keys[0])
	} else {
		// 内部节点分裂：中键提升到父节点。
		sibling.keys = append(sibling.keys, child.keys[mid+1:]...)
		sibling.children = append(sibling.children, child.children[mid+1:]...)
		midKey := child.keys[mid]
		child.keys = child.keys[:mid]
		child.children = child.children[:mid+1]
		n.keys = insertInt(n.keys, i, midKey)
	}
	n.children = insertNode(n.children, i+1, sibling)
}

// Range 返回 [lo, hi] 闭区间内所有已存在的键（升序）。
// 读路径同样采用螃蟹脚耦合，到达叶子后沿叶子链表前进。
func (t *BTree) Range(lo, hi int) []int {
	if lo > hi {
		return nil
	}
	t.rootMu.RLock()
	n := t.root
	n.mu.RLock()
	t.rootMu.RUnlock()

	for !n.isLeaf {
		child := n.children[childIndex(n, lo)]
		child.mu.RLock()
		n.mu.RUnlock()
		n = child
	}

	var out []int
	for n != nil {
		for _, k := range n.keys {
			if k > hi {
				n.mu.RUnlock()
				return out
			}
			if k >= lo {
				out = append(out, k)
			}
		}
		next := n.next
		if next != nil {
			next.mu.RLock()
		}
		n.mu.RUnlock()
		n = next
	}
	return out
}

// childIndex 返回 key 应当落入的孩子下标。调用方须持有 n 的锁。
func childIndex(n *node, key int) int {
	return sort.Search(len(n.keys), func(i int) bool { return key < n.keys[i] })
}

func insertInt(s []int, i, v int) []int {
	s = append(s, 0)
	copy(s[i+1:], s[i:])
	s[i] = v
	return s
}

func insertNode(s []*node, i int, v *node) []*node {
	s = append(s, nil)
	copy(s[i+1:], s[i:])
	s[i] = v
	return s
}

// checkInvariants 校验树结构不变量，仅供测试与调试使用。
func (t *BTree) checkInvariants() error {
	t.rootMu.RLock()
	root := t.root
	t.rootMu.RUnlock()
	if root == nil {
		return fmt.Errorf("nil root")
	}
	leafDepth, err := t.checkNode(root, 0, true)
	if err != nil {
		return err
	}
	_ = leafDepth
	return nil
}

func (t *BTree) checkNode(n *node, depth int, isRoot bool) (int, error) {
	if !isRoot && len(n.keys) > t.order-1 {
		return 0, fmt.Errorf("node overflow: %d keys (order %d)", len(n.keys), t.order)
	}
	for i := 1; i < len(n.keys); i++ {
		if n.keys[i-1] >= n.keys[i] {
			return 0, fmt.Errorf("keys not strictly increasing: %v", n.keys)
		}
	}
	if n.isLeaf {
		return depth, nil
	}
	if len(n.children) != len(n.keys)+1 {
		return 0, fmt.Errorf("children/keys mismatch: %d children, %d keys",
			len(n.children), len(n.keys))
	}
	leafDepth := -1
	for i, c := range n.children {
		// 每个孩子的键必须落在父节点对应的分界范围内。
		for _, k := range c.keys {
			if i > 0 && k < n.keys[i-1] {
				return 0, fmt.Errorf("key %d below lower bound %d", k, n.keys[i-1])
			}
			if i < len(n.keys) && k >= n.keys[i] {
				return 0, fmt.Errorf("key %d above upper bound %d", k, n.keys[i])
			}
		}
		d, err := t.checkNode(c, depth+1, false)
		if err != nil {
			return 0, err
		}
		if leafDepth == -1 {
			leafDepth = d
		} else if d != leafDepth {
			return 0, fmt.Errorf("unbalanced leaf depths: %d vs %d", leafDepth, d)
		}
	}
	return leafDepth, nil
}

// String 返回树的多行文本表示，仅供调试。
func (t *BTree) String() string {
	t.rootMu.RLock()
	root := t.root
	t.rootMu.RUnlock()
	var sb strings.Builder
	var walk func(n *node, depth int)
	walk = func(n *node, depth int) {
		fmt.Fprintf(&sb, "%*s%v\n", depth*2, "", n.keys)
		if !n.isLeaf {
			for _, c := range n.children {
				walk(c, depth+1)
			}
		}
	}
	walk(root, 0)
	return sb.String()
}
