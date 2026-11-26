package memo

// 本文件提供独立于 Memo 的全物理计划空间穷举器，作为验收的参照实现：
// 它不使用 Union-Find、Group 或任何 Memo 缓存，只在关系集合上做朴素
// 自底向上枚举，覆盖每一种连接分区、连接实现（Hash/Merge）与
// Enforcer（Sort/Exchange）组合。

// ExhaustiveBest 在 ≤5 表的逻辑树上穷举全部物理计划，返回给定需求属性下
// 全局最小代价计划。
func ExhaustiveBest(schema *Schema, cost CostModel, tree *LogicalNode, req PhysProps) (Plan, error) {
	if cost == nil {
		cost = DefaultCostModel{}
	}
	if err := tree.validate(schema); err != nil {
		return nil, err
	}
	ex := &exhaustive{
		schema: schema,
		cost:   cost,
		best:   make(map[string]Plan),
		conds:  collectConds(tree),
	}
	relset, err := ex.relsetOf(tree)
	if err != nil {
		return nil, err
	}
	return ex.solve(relset, req), nil
}

type exhaustive struct {
	schema *Schema
	cost   CostModel
	// best 缓存“关系集合 + 需求属性 -> 最优计划”，但枚举仍然覆盖所有
	// 连接分区与实现，等价于对完整空间做动态规划。
	best  map[string]Plan
	conds []JoinCond
}

func collectConds(n *LogicalNode) []JoinCond {
	var out []JoinCond
	var walk func(*LogicalNode)
	walk = func(x *LogicalNode) {
		if x == nil {
			return
		}
		if x.Kind == KindInnerJoin {
			out = append(out, x.Conds...)
			walk(x.Left)
			walk(x.Right)
		}
	}
	walk(n)
	return out
}

func (ex *exhaustive) relsetOf(n *LogicalNode) (uint32, error) {
	switch n.Kind {
	case KindScan:
		bit := uint32(0)
		for i, t := range ex.schema.Tables {
			if t.Name == n.Table {
				bit = 1 << uint(i)
			}
		}
		if bit == 0 {
			return 0, errUnknownKind(n.Kind)
		}
		return bit, nil
	case KindInnerJoin:
		l, err := ex.relsetOf(n.Left)
		if err != nil {
			return 0, err
		}
		r, err := ex.relsetOf(n.Right)
		if err != nil {
			return 0, err
		}
		return l | r, nil
	default:
		return 0, errUnknownKind(n.Kind)
	}
}

// tableName 把单表位掩码翻译回表名。
func (ex *exhaustive) tableName(bit uint32) string {
	for i, t := range ex.schema.Tables {
		if bit == 1<<uint(i) {
			return t.Name
		}
	}
	return ""
}

// crossingConds 返回跨越两个关系集合的等值条件，并规范为左列属于 left。
func (ex *exhaustive) crossingConds(left, right uint32) []JoinCond {
	var out []JoinCond
	for _, c := range ex.conds {
		lb := ex.columnBit(c.Left)
		rb := ex.columnBit(c.Right)
		switch {
		case lb&left != 0 && rb&right != 0:
			out = append(out, JoinCond{Left: c.Left, Right: c.Right})
		case rb&left != 0 && lb&right != 0:
			out = append(out, JoinCond{Left: c.Right, Right: c.Left})
		}
	}
	return out
}

func (ex *exhaustive) columnBit(column string) uint32 {
	for i, t := range ex.schema.Tables {
		if len(column) >= len(t.Name)+1 && column[:len(t.Name)+1] == t.Name+"." {
			return 1 << uint(i)
		}
	}
	return 0
}

func (ex *exhaustive) key(relset uint32, req PhysProps) string {
	b := newKeyBuilder()
	b.writeInt(int(relset))
	b.writeByte('|')
	b.writeString(req.key())
	return b.string()
}

// solve 枚举 relset 在 req 下的全部物理计划并保留最优。
func (ex *exhaustive) solve(relset uint32, req PhysProps) Plan {
	key := ex.key(relset, req)
	if p, ok := ex.best[key]; ok {
		return p
	}

	var best Plan
	consider := func(p Plan) {
		if p != nil && (best == nil || cheaper(p, best)) {
			best = p
		}
	}

	// 基表：仅扫描，且必须天然满足需求（Enforcer 在统一入口处理）。
	if relset&(relset-1) == 0 {
		t := ex.schema.table(ex.tableName(relset))
		natural := PhysProps{
			SortCols: append([]string(nil), t.SortKeys...),
			Distrib:  t.Distrib,
		}
		if satisfies(natural, req) {
			consider(&ScanPlan{
				basePlan: basePlan{
					cost:  ex.cost.ScanCost(t.Rows),
					props: natural,
					rows:  t.Rows,
				},
				Table: t.Name,
			})
		}
	} else {
		// 枚举所有非空真子集分区（每个方向各枚举一次）。
		for sub := (relset - 1) & relset; sub != 0; sub = (sub - 1) & relset {
			left := sub
			right := relset ^ sub
			if left == 0 || right == 0 || left > right {
				continue // 只保留 left<right 的唯一分区，方向在连接层显式枚举。
			}
			conds := ex.crossingConds(left, right)
			if len(conds) == 0 {
				continue
			}
			ex.enumJoinOrientations(left, right, conds, req, consider)
		}
	}

	// Enforcer 路径（放松需求后递归，结构保证不重复嵌套同类 Enforcer）。
	if len(req.SortCols) > 0 {
		relaxed := req
		relaxed.SortCols = nil
		if inner := ex.solve(relset, relaxed); inner != nil {
			consider(ex.enforceSort(inner, req.SortCols))
		}
	}
	if !req.Distrib.isZero() {
		relaxed := req
		relaxed.Distrib = Distrib{}
		if inner := ex.solve(relset, relaxed); inner != nil {
			consider(ex.enforceExchange(inner, req.Distrib))
		}
	}

	ex.best[key] = best
	return best
}

// enumJoinOrientations 对一个分区枚举两种方向 × 两种连接实现。
func (ex *exhaustive) enumJoinOrientations(left, right uint32, conds []JoinCond,
	req PhysProps, consider func(Plan)) {

	orientations := [][2]uint32{{left, right}}
	if left != right {
		orientations = append(orientations, [2]uint32{right, left})
	}
	for _, ori := range orientations {
		l, r := ori[0], ori[1]
		oc := ex.crossingConds(l, r)
		for _, kind := range []string{PhysHashJoin, PhysMergeJoin} {
			ex.enumJoin(l, r, oc, kind, req, consider)
		}
	}
}

func (ex *exhaustive) enumJoin(left, right uint32, conds []JoinCond, kind string,
	req PhysProps, consider func(Plan)) {

	leftKeys, rightKeys := splitJoinKeys(conds)

	leftReq := PhysProps{}
	rightReq := PhysProps{}
	if kind == PhysMergeJoin {
		leftReq.SortCols = append(leftReq.SortCols, leftKeys...)
		rightReq.SortCols = append(rightReq.SortCols, rightKeys...)
	}

	// 子节点的两种分布需求：
	//   - 自然路径：不约束分布，仅当两侧自然兼容时连接成立；
	//   - 哈希路径：要求子节点产出连接键哈希分布（天然满足或内部 Exchange）。
	hashLeftReq := leftReq
	hashLeftReq.Distrib = Distrib{Hash: true, Columns: leftKeys}
	hashRightReq := rightReq
	hashRightReq.Distrib = Distrib{Hash: true, Columns: rightKeys}

	naturalLeft := ex.solve(left, leftReq)
	naturalRight := ex.solve(right, rightReq)
	if naturalLeft != nil && naturalRight != nil {
		naturalOK := false
		if kind == PhysHashJoin {
			naturalOK = coLocated(naturalLeft.Props().Distrib,
				naturalRight.Props().Distrib, leftKeys, rightKeys)
		} else {
			naturalOK = !incompatibleHashDistrib(
				naturalLeft.Props().Distrib, naturalRight.Props().Distrib)
		}
		if naturalOK {
			consider(ex.makeJoin(kind, conds, naturalLeft, naturalRight,
				leftKeys, req, false))
		}
	}

	hashLeft := ex.solve(left, hashLeftReq)
	hashRight := ex.solve(right, hashRightReq)
	if hashLeft != nil && hashRight != nil {
		consider(ex.makeJoin(kind, conds, hashLeft, hashRight,
			leftKeys, req, true))
	}
}

func (ex *exhaustive) makeJoin(kind string, conds []JoinCond, lp, rp Plan,
	leftKeys []string, req PhysProps, enforced bool) Plan {

	outRows := estimateRows(lp.Rows(), rp.Rows())
	var joinCost int64
	switch kind {
	case PhysHashJoin:
		joinCost = ex.cost.HashJoinCost(lp.Rows(), rp.Rows(), outRows)
	case PhysMergeJoin:
		joinCost = ex.cost.MergeJoinCost(lp.Rows(), rp.Rows(), outRows)
	}

	props := PhysProps{Distrib: ex.outputDistrib(lp, rp, leftKeys)}
	if kind == PhysMergeJoin {
		rightKeys := make([]string, len(conds))
		for i, c := range conds {
			rightKeys[i] = c.Right
		}
		props.SortCols = append(append([]string(nil), leftKeys...), rightKeys...)
	}
	join := &JoinPlan{
		basePlan: basePlan{
			cost:  lp.Cost() + rp.Cost() + joinCost,
			props: props,
			rows:  outRows,
		},
		kind:     kind,
		keys:     append([]JoinCond(nil), conds...),
		left:     lp,
		right:    rp,
		enforced: enforced,
	}
	if !satisfies(join.Props(), req) {
		return nil
	}
	return join
}

func (ex *exhaustive) outputDistrib(lp, rp Plan, leftKeys []string) Distrib {
	dl, dr := lp.Props().Distrib, rp.Props().Distrib
	if dl.Hash && dr.Hash {
		return Distrib{Hash: true, Columns: append([]string(nil), leftKeys...)}
	}
	if dl.Singleton && dr.Singleton {
		return Distrib{Singleton: true}
	}
	return Distrib{Singleton: true}
}

func (ex *exhaustive) enforceSort(inner Plan, cols []string) Plan {
	if s, ok := inner.(*SortPlan); ok && sameColumnSet(s.cols, cols) {
		return inner
	}
	return &SortPlan{
		basePlan: basePlan{
			cost: inner.Cost() + ex.cost.SortCost(inner.Rows(), len(cols)),
			props: PhysProps{
				SortCols: append([]string(nil), cols...),
				Distrib:  inner.Props().Distrib,
			},
			rows: inner.Rows(),
		},
		cols:  append([]string(nil), cols...),
		child: inner,
	}
}

func (ex *exhaustive) enforceExchange(inner Plan, distrib Distrib) Plan {
	if ex2, ok := inner.(*ExchangePlan); ok && equalDistrib(ex2.Props().Distrib, distrib) {
		return inner
	}
	if equalDistrib(inner.Props().Distrib, distrib) {
		return inner
	}
	return &ExchangePlan{
		basePlan: basePlan{
			cost: inner.Cost() + ex.cost.ExchangeCost(inner.Rows(), len(distrib.Columns)),
			props: PhysProps{
				SortCols: append([]string(nil), inner.Props().SortCols...),
				Distrib:  distrib,
			},
			rows: inner.Rows(),
		},
		child: inner,
	}
}
