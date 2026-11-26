package memo

// 本文件实现等价变换规则集：连接交换律（Commutativity）与连接结合律
// （Associativity）。规则在 Memo 上迭代到不动点，新表达式通过 admit 进入
// Memo，命中既有 Group 时立刻触发合并与级联去重。

// Explore 应用全部等价规则直到不动点，返回根 Group 的规范 ID。
func (m *Memo) Explore() int {
	for {
		changed := false

		// 快照当前存活的 Group/表达式，避免在遍历过程中被合并改写。
		type job struct {
			group int
			expr  int
		}
		var jobs []job
		for gid, g := range m.groups {
			for _, eid := range g.exprs {
				jobs = append(jobs, job{group: gid, expr: eid})
			}
		}

		for _, j := range jobs {
			if m.Find(j.group) != j.group {
				continue // 该 Group 已在本轮被合并。
			}
			e, ok := m.exprs[j.expr]
			if !ok {
				continue // 表达式在级联去重中被移除。
			}

			if e.op != opJoin {
				continue
			}
			if m.applyCommute(e) {
				changed = true
			}
			if m.applyAssociate(e) {
				changed = true
			}
		}

		if !changed {
			break
		}
	}
	m.root = m.Find(m.root)
	return m.root
}

// applyCommute 应用交换律：(A ⋈ B) -> (B ⋈ A)。
func (m *Memo) applyCommute(e *GroupExpr) bool {
	before := len(m.exprs) + m.mergeCount
	m.genJoin(e.children[1], e.children[0], e.conds)
	return len(m.exprs)+m.mergeCount != before
}

// applyAssociate 应用结合律：
//
//	(A ⋈ B) ⋈ C  ->  A ⋈ (B ⋈ C)
//
// 右子树为连接的形态由交换律旋转得到（(A ⋈ B) ⋈ (C ⋈ D) 先交换为
// (C ⋈ D) ⋈ (A ⋈ B) 再结合），因此仅处理左子树为连接的情形即可覆盖
// 全部括号重排。
func (m *Memo) applyAssociate(e *GroupExpr) bool {
	left := m.firstJoinExpr(e.children[0])
	if left == nil {
		return false
	}

	a := left.children[0]
	b := left.children[1]
	c := e.children[1]

	relsA := m.groupRelset(a)
	relsB := m.groupRelset(b)
	relsC := m.groupRelset(c)

	// 参与重写的全部等值条件 = 左连接内部条件 + 根连接条件。
	allConds := make([]JoinCond, 0, len(left.conds)+len(e.conds))
	allConds = append(allConds, left.conds...)
	allConds = append(allConds, e.conds...)

	var innerConds, outerConds []JoinCond
	for _, cond := range allConds {
		lr := m.columnBit(cond.Left)
		rr := m.columnBit(cond.Right)
		inA := lr&relsA != 0 || rr&relsA != 0
		inB := lr&relsB != 0 || rr&relsB != 0
		inC := lr&relsC != 0 || rr&relsC != 0
		switch {
		// 条件两端位于 B、C：归入新内连接 B ⋈ C。
		case inB && inC && !inA:
			innerConds = append(innerConds, cond)
		// 条件两端位于 A 与 B：提升为新外连接 A ⋈ (B ⋈ C) 的条件。
		case inA && inB && !inC:
			outerConds = append(outerConds, cond)
		// 条件两端位于 A 与 C：同样属于新外连接条件。
		case inA && inC && !inB:
			outerConds = append(outerConds, cond)
		default:
			// 两端同属一个子 Group 的条件在规范化后不应出现；忽略以免产生脏表达式。
			continue
		}
	}
	if len(innerConds) == 0 {
		// B 与 C 之间没有等值条件，内连接退化为笛卡尔积，不在规则集范围内。
		return false
	}
	if len(outerConds) == 0 {
		// A 与 B∪C 之间没有等值条件，外连接退化为笛卡尔积，跳过。
		return false
	}

	before := len(m.exprs) + m.mergeCount
	bc := m.genJoin(b, c, innerConds)
	m.genJoin(a, bc, outerConds)
	return len(m.exprs)+m.mergeCount != before
}

// firstJoinExpr 返回指定 Group 中第一个连接表达式（不存在则 nil）。
func (m *Memo) firstJoinExpr(groupID int) *GroupExpr {
	g := m.groups[m.Find(groupID)]
	if g == nil {
		return nil
	}
	for _, eid := range g.exprs {
		if e := m.exprs[eid]; e != nil && e.op == opJoin {
			return e
		}
	}
	return nil
}

// genJoin 构造一个连接表达式并通过 admit 进入 Memo，返回其所属规范 Group。
func (m *Memo) genJoin(left, right int, conds []JoinCond) int {
	e := m.newExpr(opJoin)
	e.children = []int{left, right}
	e.conds = append([]JoinCond(nil), conds...)
	return m.admitGenerated(e)
}
