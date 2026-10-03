package deltacrdt

import "sort"

// rootID 是文档虚拟根节点的标识（零值 Dot）。
var rootID = Dot{}

// node 是文档中的一个字符节点（RGA 元素）。
type node struct {
	id        Dot
	parent    Dot
	value     rune
	tombstone bool
	deletedAt Dot // 删除事件的 Dot，用于水位比较
}

// Document 是基于 RGA（Replicated Growable Array）的字符序列 CRDT。
// 插入产生新节点，删除打墓碑标记，二者均为可交换、幂等操作。
type Document struct {
	site     uint64
	counter  uint64
	nodes    map[Dot]*node
	children map[Dot][]*node // parent -> 子节点，按 id 降序
	vv       VersionVector
}

// NewDocument 创建一个以 site 为站点ID的文档副本。
func NewDocument(site uint64) *Document {
	return &Document{
		site:     site,
		nodes:    make(map[Dot]*node),
		children: make(map[Dot][]*node),
		vv:       make(VersionVector),
	}
}

// nextDot 产生下一个本地事件 Dot 并推进本地水位。
func (d *Document) nextDot() Dot {
	d.counter++
	dot := Dot{Site: d.site, Counter: d.counter}
	d.vv.Observe(dot)
	return dot
}

// Version 返回当前文档水位（版本向量）。
func (d *Document) Version() VersionVector {
	return d.vv.Clone()
}

// InsertAfter 在 parent 之后插入字符，返回新节点 ID。parent 为零值表示插到文档头部。
func (d *Document) InsertAfter(parent Dot, value rune) Dot {
	n := &node{id: d.nextDot(), parent: parent, value: value}
	d.addNode(n)
	return n.id
}

// Delete 删除指定 ID 的字符（打墓碑）。
func (d *Document) Delete(id Dot) {
	n, ok := d.nodes[id]
	if !ok || n.tombstone {
		return
	}
	n.tombstone = true
	n.deletedAt = d.nextDot()
}

// InsertAt 在可见位置 pos 处插入字符（0 表示文档头部）。
func (d *Document) InsertAt(pos int, value rune) Dot {
	ids := d.VisibleIDs()
	parent := rootID
	if pos > 0 && pos <= len(ids) {
		parent = ids[pos-1]
	}
	return d.InsertAfter(parent, value)
}

// DeleteAt 删除可见位置 pos 处的字符。
func (d *Document) DeleteAt(pos int) {
	ids := d.VisibleIDs()
	if pos < 0 || pos >= len(ids) {
		return
	}
	d.Delete(ids[pos])
}

// ReplaceAt 用新字符替换可见位置 pos 处的字符（删除 + 插入）。
func (d *Document) ReplaceAt(pos int, value rune) {
	ids := d.VisibleIDs()
	if pos < 0 || pos >= len(ids) {
		return
	}
	prev := rootID
	if pos > 0 {
		prev = ids[pos-1]
	}
	d.Delete(ids[pos])
	d.InsertAfter(prev, value)
}

// addNode 将节点挂入 RGA 树，兄弟间按 id 降序排列。
func (d *Document) addNode(n *node) {
	d.nodes[n.id] = n
	siblings := d.children[n.parent]
	idx := sort.Search(len(siblings), func(i int) bool {
		return siblings[i].id.less(n.id)
	})
	siblings = append(siblings, nil)
	copy(siblings[idx+1:], siblings[idx:])
	siblings[idx] = n
	d.children[n.parent] = siblings
}

// mergeNode 合并远端节点：未知则挂载，已存在则对墓碑取或（幂等）。
func (d *Document) mergeNode(n *node) {
	existing, ok := d.nodes[n.id]
	if ok {
		if n.tombstone && !existing.tombstone {
			existing.tombstone = true
			existing.deletedAt = n.deletedAt
		}
		return
	}
	d.addNode(n)
}

// VisibleIDs 按文档顺序返回所有可见（未删除）节点的 ID。
func (d *Document) VisibleIDs() []Dot {
	var ids []Dot
	var walk func(parent Dot)
	walk = func(parent Dot) {
		for _, n := range d.children[parent] {
			if !n.tombstone {
				ids = append(ids, n.id)
			}
			walk(n.id)
		}
	}
	walk(rootID)
	return ids
}

// String 返回文档当前可见文本。
func (d *Document) String() string {
	var buf []rune
	var walk func(parent Dot)
	walk = func(parent Dot) {
		for _, n := range d.children[parent] {
			if !n.tombstone {
				buf = append(buf, n.value)
			}
			walk(n.id)
		}
	}
	walk(rootID)
	return string(buf)
}

// Len 返回可见字符数。
func (d *Document) Len() int {
	count := 0
	for _, n := range d.nodes {
		if !n.tombstone {
			count++
		}
	}
	return count
}
