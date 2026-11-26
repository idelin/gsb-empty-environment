package memo

import (
	"hash/fnv"
	"sort"
	"strings"
)

// 逻辑算子名。
const (
	opScan = "Scan"
	opJoin = "InnerJoin"
)

// GroupExpr 是 Memo 内的逻辑表达式，子节点以 Group ID 引用。
//
// 当两个等价 Group 合并时，子 Group ID 通过 Union-Find 重定向，表达式的
// 确定性键与哈希随之重算（canonicalize）。
type GroupExpr struct {
	id       int
	op       string
	table    string
	conds    []JoinCond
	children []int
	group    int
	relset   uint32
	key      string
	hash     uint64
}

// Group 是一个等价组，持有若干逻辑等价的 GroupExpr。
type Group struct {
	id     int
	relset uint32
	exprs  []int
}

// Memo 是 Cascades 风格的等价表达式森林。
type Memo struct {
	schema *Schema

	groups map[int]*Group
	exprs  map[int]*GroupExpr
	parent []int // Union-Find 父指针；根节点 parent[id] == id。

	nextGroupID int
	nextExprID  int

	// byRelset 维护“关系集合 -> 规范 Group”的唯一映射。
	byRelset map[uint32]int
	// bySig 维护“稳定语义签名 -> 表达式 ID”，用于规则幂等去重。
	// 签名只依赖列名与关系集合，不依赖易变的 Group ID。
	bySig map[string]int

	// 观测计数器，供验收测试检查合并与级联行为。
	mergeCount       int
	dedupeCount      int
	hashRecomputeCnt int

	root int
}

// NewMemo 创建空 Memo。
func NewMemo(schema *Schema) *Memo {
	return &Memo{
		schema:      schema,
		groups:      make(map[int]*Group),
		exprs:       make(map[int]*GroupExpr),
		byRelset:    make(map[uint32]int),
		bySig:       make(map[string]int),
		nextGroupID: 1,
		nextExprID:  1,
		root:        -1,
	}
}

// Build 把初始逻辑连接树装入 Memo，返回根 Group ID。
func (m *Memo) Build(tree *LogicalNode) (int, error) {
	if err := tree.validate(m.schema); err != nil {
		return 0, err
	}
	root, err := m.insertLogical(tree)
	if err != nil {
		return 0, err
	}
	m.root = root
	return root, nil
}

// Root 返回 Memo 根 Group 的规范 ID。
func (m *Memo) Root() int { return m.root }

// Find 实现带路径压缩的 Union-Find 查询。
func (m *Memo) Find(id int) int {
	root := id
	for m.parent[root] != root {
		root = m.parent[root]
	}
	for x := id; m.parent[x] != x; {
		next := m.parent[x]
		m.parent[x] = root
		x = next
	}
	return root
}

func (m *Memo) newGroup(relset uint32) int {
	id := m.nextGroupID
	m.nextGroupID++
	for len(m.parent) <= id {
		m.parent = append(m.parent, 0)
	}
	m.parent[id] = id
	m.groups[id] = &Group{id: id, relset: relset}
	m.byRelset[relset] = id
	return id
}

// newDetachedGroup 创建一个不写入 byRelset 唯一索引的 Group，
// 供“新表达式意外命中另一个已有 Group”场景临时独立成组，
// 随后立刻与同 relset 的旧 Group 合并。
func (m *Memo) newDetachedGroup(relset uint32) int {
	id := m.nextGroupID
	m.nextGroupID++
	for len(m.parent) <= id {
		m.parent = append(m.parent, 0)
	}
	m.parent[id] = id
	m.groups[id] = &Group{id: id, relset: relset}
	return id
}

func (m *Memo) newExpr(op string) *GroupExpr {
	id := m.nextExprID
	m.nextExprID++
	return &GroupExpr{id: id, op: op}
}

// insertLogical 自底向上把一棵逻辑树插入 Memo。
func (m *Memo) insertLogical(n *LogicalNode) (int, error) {
	switch n.Kind {
	case KindScan:
		e := m.newExpr(opScan)
		e.table = n.Table
		return m.admit(e), nil
	case KindInnerJoin:
		left, err := m.insertLogical(n.Left)
		if err != nil {
			return 0, err
		}
		right, err := m.insertLogical(n.Right)
		if err != nil {
			return 0, err
		}
		e := m.newExpr(opJoin)
		e.children = []int{left, right}
		e.conds = append([]JoinCond(nil), n.Conds...)
		return m.admit(e), nil
	default:
		return 0, errUnknownKind(n.Kind)
	}
}

// admit 接收初始树的表达式：每个关系集合对应一个稳定 Group。
func (m *Memo) admit(e *GroupExpr) int {
	m.canonicalize(e)
	target, existed := m.byRelset[e.relset]
	if !existed {
		target = m.newGroup(e.relset)
	}
	return m.admitInto(e, target)
}

// admitGenerated 接收规则新生成的表达式。
//
// 规则探索期间每个关系集合始终对应一个稳定 Group：新表达式按语义签名
// 去重后直接并入该 Group，因此探索过程天然幂等收敛。
// Union-Find 的“两个既有 Group 合并”由 InjectEquivalentGroup 显式驱动
// （模拟新表达式意外命中另一个已有 Group），并级联去重所有父表达式。
func (m *Memo) admitGenerated(e *GroupExpr) int {
	m.canonicalize(e)

	sig := m.semanticSignature(e)
	if eid, ok := m.bySig[sig]; ok {
		owner := m.Find(m.exprs[eid].group)
		m.dedupeCount++
		return owner
	}

	target, existed := m.byRelset[e.relset]
	if !existed {
		target = m.newGroup(e.relset)
	}
	target = m.Find(target)
	e.group = target
	m.exprs[e.id] = e
	m.groups[target].exprs = append(m.groups[target].exprs, e.id)
	m.bySig[sig] = e.id
	return target
}

// InjectEquivalentGroup 显式制造“两个覆盖同一关系集合的既有 Group”并立即
// 合并，用于驱动 Union-Find 重定向与父表达式级联去重（验收用入口）。
//
// 它复制 target Group 的全部表达式到一个新的独立 Group，再把新 Group 合并
// 回 target。合并后所有引用相关表达式的父表达式都会在 cascade 中被重算
// 哈希并二次去重。返回收敛后的规范 Group ID。
func (m *Memo) InjectEquivalentGroup(target int) (int, error) {
	target = m.Find(target)
	g, ok := m.groups[target]
	if !ok {
		return 0, errUnknownGroup(target)
	}

	fresh := m.newDetachedGroup(g.relset)
	clones := make([]int, 0, len(g.exprs))
	for _, eid := range g.exprs {
		src := m.exprs[eid]
		clone := m.newExpr(src.op)
		clone.table = src.table
		clone.conds = append([]JoinCond(nil), src.conds...)
		clone.children = append([]int(nil), src.children...)
		m.canonicalize(clone)
		clone.group = fresh
		m.exprs[clone.id] = clone
		clones = append(clones, clone.id)
	}
	m.groups[fresh].exprs = clones

	m.mergeGroups(target, fresh)
	m.cascade()
	return m.Find(target), nil
}

// injectEquivalentGroupReroot 与 InjectEquivalentGroup 类似，但让新建的
// 等价 Group 成为 Union-Find survivor（旧 target 被重定向到新组）。
//
// 这样所有引用旧 target 的父表达式都必须改指新 Group ID，其确定性键与
// 哈希随之重算，用于在验收中确定性地观测“父表达式哈希重算 + 二次级联
// 去重”。
func (m *Memo) injectEquivalentGroupReroot(target int) (int, error) {
	target = m.Find(target)
	g, ok := m.groups[target]
	if !ok {
		return 0, errUnknownGroup(target)
	}

	fresh := m.newDetachedGroup(g.relset)
	clones := make([]int, 0, len(g.exprs))
	for _, eid := range g.exprs {
		src := m.exprs[eid]
		clone := m.newExpr(src.op)
		clone.table = src.table
		clone.conds = append([]JoinCond(nil), src.conds...)
		clone.children = append([]int(nil), src.children...)
		m.canonicalize(clone)
		clone.group = fresh
		m.exprs[clone.id] = clone
		clones = append(clones, clone.id)
	}
	m.groups[fresh].exprs = clones

	// 反向合并：fresh 作为 survivor，target 被重定向。
	m.mergeGroups(fresh, target)
	m.cascade()
	return m.Find(target), nil
}

// admitInto 把已规范化表达式放入目标 Group，处理同签名去重。
func (m *Memo) admitInto(e *GroupExpr, target int) int {
	sig := m.semanticSignature(e)
	if eid, ok := m.bySig[sig]; ok {
		owner := m.Find(m.exprs[eid].group)
		m.dedupeCount++
		return owner
	}
	e.group = target
	m.exprs[e.id] = e
	m.groups[target].exprs = append(m.groups[target].exprs, e.id)
	m.bySig[sig] = e.id
	return target
}

// semanticSignature 返回只依赖稳定信息（算子、表名、关系集合、顶层子树
// 关系集合划分、无向等值边集合）的签名。它不依赖 Group ID，因此在
// Union-Find 合并前后保持稳定，保证规则去重幂等、探索必然收敛。
func (m *Memo) semanticSignature(e *GroupExpr) string {
	b := newKeyBuilder()
	b.writeInt(int(e.relset))
	b.writeByte('|')
	switch e.op {
	case opScan:
		b.writeString(opScan)
		b.writeByte('(')
		b.writeString(e.table)
		b.writeByte(')')
	case opJoin:
		b.writeString(opJoin)
		b.writeByte('{')
		b.writeInt(int(m.groupRelset(e.children[0])))
		b.writeByte(',')
		b.writeInt(int(m.groupRelset(e.children[1])))
		b.writeByte('}')
		b.writeByte('(')
		edges := make([]string, 0, len(e.conds))
		for _, c := range e.conds {
			l, r := c.Left, c.Right
			if l > r {
				l, r = r, l
			}
			edges = append(edges, l+"="+r)
		}
		sort.Strings(edges)
		for i, s := range edges {
			if i > 0 {
				b.writeByte(';')
			}
			b.writeString(s)
		}
		b.writeByte(')')
	}
	return b.string()
}

// mergeGroups 把 other 合并进 survivor（Union-Find 重定向 + 表达式迁移去重）。
func (m *Memo) mergeGroups(survivor, other int) {
	survivor = m.Find(survivor)
	other = m.Find(other)
	if survivor == other {
		return
	}

	m.parent[other] = survivor
	sg := m.groups[survivor]
	og := m.groups[other]

	kept := make([]int, 0, len(og.exprs))
	for _, eid := range og.exprs {
		e := m.exprs[eid]
		e.group = survivor
		sig := m.semanticSignature(e)
		if _, conflict := m.bySig[sig]; conflict {
			// 迁移即去重：survivor 已持有同签名表达式。
			delete(m.exprs, eid)
			m.dedupeCount++
			continue
		}
		m.bySig[sig] = eid
		kept = append(kept, eid)
	}
	sg.exprs = append(sg.exprs, kept...)
	delete(m.groups, other)
	m.byRelset[og.relset] = survivor
	m.mergeCount++
}

// cascade 反复执行“子组重定向 -> 父表达式哈希重算 -> 组内去重 -> 跨组
// 再合并”，直到 Memo 达到不动点（二次级联去重）。
func (m *Memo) cascade() {
	for {
		changed := m.rewriteAndDedupe()
		pair := m.findCrossGroupMerge()
		if pair == nil {
			if !changed {
				return
			}
			continue
		}
		m.mergeGroups(pair[0], pair[1])
	}
}

// rewriteAndDedupe 重算所有表达式的规范子引用/键/哈希，做组内去重并重建索引。
func (m *Memo) rewriteAndDedupe() bool {
	changed := false

	for _, g := range m.groups {
		seen := make(map[string]int, len(g.exprs))
		kept := make([]int, 0, len(g.exprs))
		for _, eid := range g.exprs {
			e := m.exprs[eid]
			oldKey := e.key
			m.canonicalize(e)
			if e.key != oldKey {
				// 子 Group 合并导致父表达式哈希重算。
				m.hashRecomputeCnt++
				changed = true
			}
			sig := m.semanticSignature(e)
			if _, dup := seen[sig]; dup {
				delete(m.exprs, eid)
				m.dedupeCount++
				changed = true
				continue
			}
			seen[sig] = eid
			kept = append(kept, eid)
		}
		g.exprs = kept
	}

	m.rebuildIndexes()
	return changed
}

// findCrossGroupMerge 检测跨 Group 的同签名表达式冲突。
func (m *Memo) findCrossGroupMerge() *[2]int {
	owner := make(map[string]int)
	for _, g := range m.groups {
		for _, eid := range g.exprs {
			sig := m.semanticSignature(m.exprs[eid])
			if first, ok := owner[sig]; ok {
				if first != g.id {
					return &[2]int{first, g.id}
				}
			} else {
				owner[sig] = g.id
			}
		}
	}
	return nil
}

// rebuildIndexes 重建 relset 与签名全局索引。
func (m *Memo) rebuildIndexes() {
	for k := range m.byRelset {
		delete(m.byRelset, k)
	}
	for k := range m.bySig {
		delete(m.bySig, k)
	}
	for gid, g := range m.groups {
		m.byRelset[g.relset] = gid
		for _, eid := range g.exprs {
			e := m.exprs[eid]
			m.bySig[m.semanticSignature(e)] = eid
		}
	}
}

// canonicalize 规范化表达式：Union-Find 重定向子 Group、规范化等值条件、
// 计算关系集合与确定性键/哈希。
func (m *Memo) canonicalize(e *GroupExpr) {
	switch e.op {
	case opScan:
		e.relset = m.tableBit(e.table)
	case opJoin:
		left := m.Find(e.children[0])
		right := m.Find(e.children[1])
		e.children[0] = left
		e.children[1] = right
		e.relset = m.groupRelset(left) | m.groupRelset(right)
		e.conds = m.canonicalConds(e.conds, left, right)
	}
	e.key = m.exprKey(e)
	h := fnv.New64a()
	_, _ = h.Write([]byte(e.key))
	e.hash = h.Sum64()
}

// exprKey 生成含规范 Group ID 的确定性字符串键（用于哈希重算观测）。
func (m *Memo) exprKey(e *GroupExpr) string {
	b := newKeyBuilder()
	switch e.op {
	case opScan:
		b.writeString(opScan)
		b.writeByte('(')
		b.writeString(e.table)
		b.writeByte(')')
	case opJoin:
		b.writeString(opJoin)
		b.writeByte('[')
		b.writeInt(e.children[0])
		b.writeByte(',')
		b.writeInt(e.children[1])
		b.writeByte(']')
		b.writeByte('(')
		for i, c := range e.conds {
			if i > 0 {
				b.writeByte(';')
			}
			b.writeString(c.Left)
			b.writeByte('=')
			b.writeString(c.Right)
		}
		b.writeByte(')')
	}
	return b.string()
}

// canonicalConds 把等值条件统一规范为“左侧列属于左子 Group”的方向，
// 再按 (Left, Right) 字典序排序。
func (m *Memo) canonicalConds(conds []JoinCond, leftGroup, rightGroup int) []JoinCond {
	lr := m.groupRelset(leftGroup)
	rr := m.groupRelset(rightGroup)
	out := make([]JoinCond, 0, len(conds))
	for _, c := range conds {
		lt := m.columnBit(c.Left)
		rt := m.columnBit(c.Right)
		switch {
		case lt&lr != 0 && rt&rr != 0:
			out = append(out, JoinCond{Left: c.Left, Right: c.Right})
		case rt&lr != 0 && lt&rr != 0:
			out = append(out, JoinCond{Left: c.Right, Right: c.Left})
		default:
			out = append(out, c)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Left != out[j].Left {
			return out[i].Left < out[j].Left
		}
		return out[i].Right < out[j].Right
	})
	return out
}

// tableBit 返回基表对应的关系位掩码。
func (m *Memo) tableBit(table string) uint32 {
	for i, t := range m.schema.Tables {
		if t.Name == table {
			return 1 << uint(i)
		}
	}
	return 0
}

// columnBit 返回列所属基表的位掩码（列名约定为 “表名.列名”）。
func (m *Memo) columnBit(column string) uint32 {
	for _, t := range m.schema.Tables {
		if strings.HasPrefix(column, t.Name+".") {
			return m.tableBit(t.Name)
		}
	}
	return 0
}

// groupRelset 返回规范 Group 的关系集合。
func (m *Memo) groupRelset(id int) uint32 {
	if g, ok := m.groups[id]; ok {
		return g.relset
	}
	return 0
}

// MergeCount 返回累计触发的等价组合并次数。
func (m *Memo) MergeCount() int { return m.mergeCount }

// DedupeCount 返回累计去重的逻辑表达式数量。
func (m *Memo) DedupeCount() int { return m.dedupeCount }

// HashRecomputeCount 返回父表达式哈希重算次数。
func (m *Memo) HashRecomputeCount() int { return m.hashRecomputeCnt }
