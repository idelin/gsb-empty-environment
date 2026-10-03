// Package peritext 实现 Peritext/Fugue 风格的富文本格式属性标注区间 CRDT。
//
// 核心思想：
//   - 文本序列使用 Fugue 风格的元素 ID（Actor, Seq）标识每个字符，
//     插入操作携带左右邻居（left/right origin），并发插入按
//     (rightOrigin, ID) 全序确定性地归并。
//   - 格式标注（粗体/斜体/下划线/超链接等）不记录字符下标，而是锚定到
//     字符 ID 之间的“边界”，因此并发插入不会使标注断裂或错位。
//   - 同一 key 的冲突取值（如 A 加粗、B 并发取消加粗）按
//     (Lamport, Actor) 的 LWW 规则逐字符判定，因果在后者必胜。
package peritext

// CharID 唯一标识文本序列中的一个字符。
type CharID struct {
	Actor string
	Seq   uint64
}

func (id CharID) isZero() bool { return id.Actor == "" && id.Seq == 0 }

// less 定义字符 ID 的全序：先 Seq，后 Actor。
func (id CharID) less(o CharID) bool {
	if id.Seq != o.Seq {
		return id.Seq < o.Seq
	}
	return id.Actor < o.Actor
}

// charNode 是序列中的一个字符节点。depth 记录其在 Fugue 树中的深度，
// 用于归并插入时跳过兄弟节点的子树。
type charNode struct {
	id      CharID
	ch      rune
	parent  CharID // 左邻居（left origin），零值表示文档开头
	right   CharID // 插入时的右邻居（right origin），零值表示无
	depth   int
	lamport uint64 // 插入操作的 Lamport 时间，用于标注边界的因果判定
}

// InsertOp 是一次可交换、可重放的插入操作。
type InsertOp struct {
	ID      CharID
	Parent  CharID // 左邻居，零值表示文档开头
	Right   CharID // 插入时的右邻居，零值表示无
	Char    rune
	Lamport uint64
}

// Document 是一个富文本 CRDT 副本。
type Document struct {
	actor string
	seq   uint64 // 本副本的元素序号
	clock uint64 // Lamport 时钟

	nodes []charNode
	index map[CharID]int
	seen  map[CharID]bool
	ops   []InsertOp // 本地已应用的插入日志，用于向其他副本同步
	marks map[MarkID]Mark
}

// New 创建一个属于 actor 的空文档副本。
func New(actor string) *Document {
	return &Document{
		actor: actor,
		index: make(map[CharID]int),
		seen:  make(map[CharID]bool),
		marks: make(map[MarkID]Mark),
	}
}

// Insert 在字符下标 pos 处本地插入一个字符并返回对应操作。
func (d *Document) Insert(pos int, ch rune) InsertOp {
	var parent, right CharID
	if pos > 0 {
		parent = d.nodes[pos-1].id
	}
	if pos < len(d.nodes) {
		right = d.nodes[pos].id
	}
	d.seq++
	d.clock++
	op := InsertOp{
		ID:      CharID{Actor: d.actor, Seq: d.seq},
		Parent:  parent,
		Right:   right,
		Char:    ch,
		Lamport: d.clock,
	}
	d.applyInsert(op)
	d.ops = append(d.ops, op)
	return op
}

// InsertText 在字符下标 pos 处插入一串字符。
func (d *Document) InsertText(pos int, s string) {
	for _, ch := range s {
		d.Insert(pos, ch)
		pos++
	}
}

// Text 返回当前纯文本内容。
func (d *Document) Text() string {
	runes := make([]rune, len(d.nodes))
	for i, n := range d.nodes {
		runes[i] = n.ch
	}
	return string(runes)
}

// Len 返回字符数。
func (d *Document) Len() int { return len(d.nodes) }

// rightLess 比较两个右邻居：零值（无右邻居）视为无穷大。
func rightLess(a, b CharID) bool {
	if a.isZero() {
		return false
	}
	if b.isZero() {
		return true
	}
	return a.less(b)
}

// siblingBefore 判定同一父节点的两个直接子节点 a、b 谁排在前面：
// 先比较右邻居（right origin 小者靠前），相同则 ID 大者靠前（FugueMax）。
func siblingBefore(a, b charNode) bool {
	if a.right != b.right {
		return rightLess(a.right, b.right)
	}
	return b.id.less(a.id)
}

// applyInsert 应用一个插入操作，幂等。前提是其父节点已存在（见 tryApplyInsert）。
func (d *Document) applyInsert(op InsertOp) {
	if d.seen[op.ID] {
		return
	}
	parentPos := -1
	parentDepth := -1
	if !op.Parent.isZero() {
		parentPos = d.index[op.Parent]
		parentDepth = d.nodes[parentPos].depth
	}
	node := charNode{
		id:      op.ID,
		ch:      op.Char,
		parent:  op.Parent,
		right:   op.Right,
		depth:   parentDepth + 1,
		lamport: op.Lamport,
	}
	// Fugue 归并：从父节点之后开始扫描，
	// 跳过排序在前的兄弟节点及其整个子树。
	i := parentPos + 1
	for i < len(d.nodes) {
		n := d.nodes[i]
		if n.depth <= parentDepth {
			break // 已离开父节点的子树
		}
		if n.depth == parentDepth+1 && !siblingBefore(n, node) {
			break // 找到排序靠后的兄弟，插到它前面
		}
		i++
	}
	d.nodes = append(d.nodes, charNode{})
	copy(d.nodes[i+1:], d.nodes[i:])
	d.nodes[i] = node
	d.seen[op.ID] = true
	if op.Lamport > d.clock {
		d.clock = op.Lamport
	}
	d.reindex()
}

// tryApplyInsert 尝试应用插入操作；若父节点尚未到达则返回 false。
func (d *Document) tryApplyInsert(op InsertOp) bool {
	if d.seen[op.ID] {
		return true
	}
	if !op.Parent.isZero() {
		if _, ok := d.index[op.Parent]; !ok {
			return false
		}
	}
	d.applyInsert(op)
	return true
}

func (d *Document) reindex() {
	for i, n := range d.nodes {
		d.index[n.id] = i
	}
}

// Merge 将 other 副本的全部状态（插入操作与格式标注）合并进本副本。
// 合并满足交换律、结合律与幂等性，两个副本互相合并后必然收敛。
func (d *Document) Merge(other *Document) {
	pending := make([]InsertOp, 0, len(other.ops))
	for _, op := range other.ops {
		if !d.seen[op.ID] {
			pending = append(pending, op)
		}
	}
	// 父节点可能尚未到达，循环重试直至全部落地。
	for len(pending) > 0 {
		next := pending[:0]
		progress := false
		for _, op := range pending {
			if d.tryApplyInsert(op) {
				d.ops = append(d.ops, op)
				progress = true
			} else {
				next = append(next, op)
			}
		}
		if !progress {
			panic("peritext: 存在永远无法满足的插入操作（父节点缺失）")
		}
		pending = next
	}
	for _, m := range other.marks {
		d.ApplyMark(m)
	}
}
