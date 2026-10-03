// Package merkle 实现可验证日志快照与差异回溯比对器。
//
// 记录按序组织为一棵 16 叉默克尔树：叶子是记录哈希，内部节点是其
// 16 个子节点哈希拼接后的 SHA-256。比对两个版本时，先比较根哈希，
// 相同则秒级判定全等；不同则沿哈希不一致的分支自顶向下下钻，
// 仅展开哈希不同的节点，最终输出变动的叶子列表。
package merkle

import (
	"crypto/sha256"
)

// BranchFactor 是树的分叉因子。16 叉树容纳 100 万条记录仅需 5 层，
// 单条变更最多触发 5 次节点展开即可定位。
const BranchFactor = 16

// Record 是日志中的一条记录。
type Record struct {
	Key   string
	Value []byte
}

// Node 是默克尔树节点。叶子节点 Rec 非空（空槽位 Rec 为 nil），
// 内部节点持有 BranchFactor 个子节点。
type Node struct {
	Hash     [32]byte
	Height   int // 叶子为 0，每向上一层加 1
	Children []*Node
	Rec      *Record
	Index    int // 叶子槽位序号
}

var emptyLeafHash = sha256.Sum256(nil)

var emptyHashes = [][32]byte{emptyLeafHash}

// emptyHash 返回全空子树在指定高度上的哈希（按需预计算，O(1) 摊还）。
func emptyHash(height int) [32]byte {
	for len(emptyHashes) <= height {
		prev := emptyHashes[len(emptyHashes)-1]
		h := sha256.New()
		for i := 0; i < BranchFactor; i++ {
			h.Write(prev[:])
		}
		var sum [32]byte
		copy(sum[:], h.Sum(nil))
		emptyHashes = append(emptyHashes, sum)
	}
	return emptyHashes[height]
}

func leafHash(r *Record) [32]byte {
	if r == nil {
		return emptyLeafHash
	}
	h := sha256.New()
	h.Write([]byte(r.Key))
	h.Write([]byte{0})
	h.Write(r.Value)
	var sum [32]byte
	copy(sum[:], h.Sum(nil))
	return sum
}

// emptyNode 构造指定高度的虚拟全空节点（不展开子节点，按需物化）。
func emptyNode(height int) *Node {
	return &Node{Hash: emptyHash(height), Height: height}
}

// nextCapacity 返回不小于 n 的最小 16 的幂（至少为 1）。
func nextCapacity(n int) int {
	cap := 1
	for cap < n {
		cap *= BranchFactor
	}
	return cap
}

// Build 将有序记录构建为默克尔树，返回根节点。
// 叶子数量补齐到 16 的幂，空槽位使用全空哈希。
func Build(records []Record) *Node {
	cap := nextCapacity(len(records))
	level := make([]*Node, cap)
	for i := range level {
		var rec *Record
		if i < len(records) {
			rec = &records[i]
		}
		level[i] = &Node{Hash: leafHash(rec), Rec: rec, Index: i}
	}
	height := 0
	for len(level) > 1 {
		height++
		next := make([]*Node, len(level)/BranchFactor)
		for i := range next {
			children := level[i*BranchFactor : (i+1)*BranchFactor]
			h := sha256.New()
			for _, c := range children {
				h.Write(c.Hash[:])
			}
			var sum [32]byte
			copy(sum[:], h.Sum(nil))
			next[i] = &Node{Hash: sum, Height: height, Children: children}
		}
		level = next
	}
	return level[0]
}

// Change 描述一条叶子差异：Old 为 nil 表示新增，New 为 nil 表示删除。
type Change struct {
	Index int
	Old   *Record
	New   *Record
}

// Stats 记录比对过程中的开销指标。
type Stats struct {
	// NodesExpanded 是下钻过程中展开（下载）的内部节点数。
	// 根相同的全等判定为 0。
	NodesExpanded int
}

// childAt 返回第 i 个子节点；虚拟空节点按需物化子节点。
func childAt(n *Node, i int) *Node {
	if n.Children != nil && n.Children[i] != nil {
		return n.Children[i]
	}
	return emptyNode(n.Height - 1)
}

// Diff 比对两棵默克尔树，返回全部变动叶子，并通过 stats 报告展开节点数。
// 允许两棵树高度不同（记录数不同）。
func Diff(a, b *Node, stats *Stats) []Change {
	var changes []Change
	var walk func(x, y *Node)
	walk = func(x, y *Node) {
		if x.Hash == y.Hash {
			return
		}
		// 高度不一致时只展开较高的一侧，直到对齐。
		if x.Height > y.Height {
			stats.NodesExpanded++
			for i := 0; i < BranchFactor; i++ {
				walk(childAt(x, i), y)
			}
			return
		}
		if y.Height > x.Height {
			stats.NodesExpanded++
			for i := 0; i < BranchFactor; i++ {
				walk(x, childAt(y, i))
			}
			return
		}
		if x.Height == 0 {
			idx := x.Index
			if y.Index > idx {
				idx = y.Index
			}
			changes = append(changes, Change{Index: idx, Old: x.Rec, New: y.Rec})
			return
		}
		stats.NodesExpanded++
		for i := 0; i < BranchFactor; i++ {
			walk(childAt(x, i), childAt(y, i))
		}
	}
	walk(a, b)
	return changes
}
