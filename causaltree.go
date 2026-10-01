// Package causaltree 实现基于因果树（Causal Tree）的无冲突协作文本编辑内核。
//
// 每个字符是树中的一个节点，携带唯一 Lamport 时间戳与客户端 ID，
// 并显式指向其父字符节点。并发在同一父节点下插入的字符，
// 按 (Lamport 时间戳, 客户端 ID) 字典序稳定排序，
// 从而保证所有客户端在同步后收敛到完全一致的文档视图。
package causaltree

import (
	"fmt"
	"sort"
	"strings"
)

// ID 是字符节点的全局唯一标识：Lamport 时间戳 + 客户端 ID。
type ID struct {
	Lamport uint64
	Client  string
}

// Compare 定义 ID 的全序：先比较 Lamport 时间戳，再按客户端 ID 字典序决胜。
func (a ID) Compare(b ID) int {
	if a.Lamport != b.Lamport {
		if a.Lamport < b.Lamport {
			return -1
		}
		return 1
	}
	return strings.Compare(a.Client, b.Client)
}

func (a ID) String() string { return fmt.Sprintf("%d@%s", a.Lamport, a.Client) }

// rootID 是虚拟根节点的 ID（Lamport=0，空客户端），永远小于任何真实节点。
var rootID = ID{}

// Node 是因果树中的一个字符节点。
type Node struct {
	ID     ID   // 唯一标识（Lamport 时间戳 + 客户端 ID）
	Char   rune // 字符内容
	Parent ID   // 父字符节点的 ID（显式因果指针）
}

// Doc 是单个客户端持有的协作文档副本（因果树 + 本地 Lamport 时钟）。
type Doc struct {
	client   string
	lamport  uint64
	nodes    map[ID]*Node   // 全部节点（含虚拟根）
	children map[ID][]*Node // 父 ID -> 已排序的子节点列表
}

// New 创建一个属于 client 的空文档。
func New(client string) *Doc {
	root := &Node{ID: rootID, Char: 0, Parent: rootID}
	return &Doc{
		client:   client,
		nodes:    map[ID]*Node{rootID: root},
		children: map[ID][]*Node{},
	}
}

// tick 推进本地 Lamport 时钟并返回新时间戳。
func (d *Doc) tick() uint64 {
	d.lamport++
	return d.lamport
}

// InsertAfter 在 parent 字符之后本地插入字符 ch，返回新节点（可广播给其他客户端）。
// parent 传零值 ID{} 表示插入到文档开头（虚拟根之下）。
func (d *Doc) InsertAfter(parent ID, ch rune) Node {
	if _, ok := d.nodes[parent]; !ok {
		panic(fmt.Sprintf("causaltree: unknown parent %v", parent))
	}
	n := &Node{
		ID:     ID{Lamport: d.tick(), Client: d.client},
		Char:   ch,
		Parent: parent,
	}
	d.integrate(n)
	return *n
}

// Apply 将远端广播来的节点并入本地因果树。
// 幂等：重复应用同一节点不会产生副作用。
func (d *Doc) Apply(n Node) {
	if n.ID.Lamport > d.lamport {
		d.lamport = n.ID.Lamport // Lamport 时钟同步：取本地与观测到的最大值
	}
	if _, ok := d.nodes[n.ID]; ok {
		return
	}
	node := n
	d.integrate(&node)
}

// integrate 把节点挂到其父节点的子列表中，并保持子列表按 ID 全序排列。
// 并发同点插入的字符由此获得确定性的相对顺序，保证各端收敛。
func (d *Doc) integrate(n *Node) {
	d.nodes[n.ID] = n
	sibs := d.children[n.Parent]
	i := sort.Search(len(sibs), func(i int) bool {
		return sibs[i].ID.Compare(n.ID) > 0
	})
	sibs = append(sibs, nil)
	copy(sibs[i+1:], sibs[i:])
	sibs[i] = n
	d.children[n.Parent] = sibs
}

// String 以深度优先顺序遍历因果树，返回当前文档文本。
func (d *Doc) String() string {
	var b strings.Builder
	var walk func(id ID)
	walk = func(id ID) {
		for _, c := range d.children[id] {
			b.WriteRune(c.Char)
			walk(c.ID)
		}
	}
	walk(rootID)
	return b.String()
}

// Chars 按文档顺序返回所有字符节点（含因果指针），用于调试与一致性校验。
func (d *Doc) Chars() []Node {
	var out []Node
	var walk func(id ID)
	walk = func(id ID) {
		for _, c := range d.children[id] {
			out = append(out, *c)
			walk(c.ID)
		}
	}
	walk(rootID)
	return out
}

// LastID 返回文档末尾字符的 ID；空文档返回虚拟根 ID。
func (d *Doc) LastID() ID {
	last := rootID
	for _, n := range d.Chars() {
		last = n.ID
	}
	return last
}
