package memo

import "fmt"

// 本文件实现 Volcano/Cascades 物理搜索：
//
//   - 对每个 (Group, 需求属性) 缓存精确最优物理计划；
//   - 同时比较“先不排序、最后加 Sort Enforcer”和“把排序要求直接下推给
//     子节点、由 MergeJoin 复用有序性”两条路径；
//   - Enforcer 只在被放松的需求上递归（in-flight 循环检测兜底），
//     结构上杜绝 Enforcer 套 Enforcer 的无限递归。

// Optimizer 在已探索的 Memo 上执行基于需求属性的物理搜索。
type Optimizer struct {
	memo  *Memo
	cost  CostModel
	cache map[string]Plan

	// inFlight 记录正在求解的 (Group, 需求属性)，任何重入都视为循环。
	inFlight map[string]bool

	groupRows map[int]int64
}

// NewOptimizer 基于 Memo 与代价模型构造物理优化器。
func NewOptimizer(m *Memo, cost CostModel) *Optimizer {
	if cost == nil {
		cost = DefaultCostModel{}
	}
	return &Optimizer{
		memo:      m,
		cost:      cost,
		cache:     make(map[string]Plan),
		inFlight:  make(map[string]bool),
		groupRows: make(map[int]int64),
	}
}

// BestPlan 返回根 Group 在指定需求属性下的精确最优物理计划。
func (o *Optimizer) BestPlan(req PhysProps) (Plan, error) {
	return o.solve(o.memo.Find(o.memo.Root()), req)
}

// CacheSize 返回已缓存的 (Group, 需求属性) 最优计划条目数。
func (o *Optimizer) CacheSize() int { return len(o.cache) }

func (o *Optimizer) cacheKey(group int, req PhysProps) string {
	return fmt.Sprintf("g%d|%s", group, req.key())
}

// solve 返回 Group 在需求属性 req 下的最优计划，是搜索的核心递归入口。
func (o *Optimizer) solve(group int, req PhysProps) (Plan, error) {
	group = o.memo.Find(group)
	key := o.cacheKey(group, req)
	if p, ok := o.cache[key]; ok {
		return p, nil
	}
	if o.inFlight[key] {
		return nil, &ErrEnforcerCycle{group: group, req: req}
	}
	o.inFlight[key] = true
	defer delete(o.inFlight, key)

	var best Plan
	consider := func(p Plan) {
		if p == nil {
			return
		}
		if best == nil || cheaper(p, best) {
			best = p
		}
	}

	g := o.memo.groups[group]
	for _, eid := range g.exprs {
		e := o.memo.exprs[eid]
		switch e.op {
		case opScan:
			p, err := o.buildScan(e, req)
			if err != nil {
				return nil, err
			}
			consider(p)
		case opJoin:
			hp, err := o.buildHashJoin(e, req)
			if err != nil {
				return nil, err
			}
			consider(hp)
			mp, err := o.buildMergeJoin(e, req)
			if err != nil {
				return nil, err
			}
			consider(mp)
		}
	}

	// 路径一：放松排序需求求解，再用 Sort Enforcer 强制满足。
	if len(req.SortCols) > 0 {
		relaxed := req
		relaxed.SortCols = nil
		inner, err := o.solve(group, relaxed)
		if err != nil {
			return nil, err
		}
		consider(o.enforceSort(inner, req.SortCols))
	}

	// 路径二：放松分布需求求解，再用 Exchange Enforcer 强制满足。
	// 零值分布表示无分布需求，无需 Exchange。
	if !req.Distrib.isZero() {
		relaxed := req
		relaxed.Distrib = Distrib{}
		inner, err := o.solve(group, relaxed)
		if err != nil {
			return nil, err
		}
		consider(o.enforceExchange(inner, req.Distrib))
	}

	if best == nil {
		return nil, fmt.Errorf("memo: no physical plan for group %d", group)
	}
	o.cache[key] = best
	return best, nil
}

// cheaper 以总代价为准，代价并列时用结构键做确定性裁决。
func cheaper(a, b Plan) bool {
	if a.Cost() != b.Cost() {
		return a.Cost() < b.Cost()
	}
	return a.PlanKey() < b.PlanKey()
}

// buildScan 构造满足需求属性的扫描计划（扫描天然只产生基表原生属性）。
func (o *Optimizer) buildScan(e *GroupExpr, req PhysProps) (Plan, error) {
	t := o.memo.schema.table(e.table)
	natural := PhysProps{
		SortCols: append([]string(nil), t.SortKeys...),
		Distrib:  t.Distrib,
	}
	if !satisfies(natural, req) {
		return nil, nil
	}
	return &ScanPlan{
		basePlan: basePlan{
			cost:  o.cost.ScanCost(t.Rows),
			props: natural,
			rows:  t.Rows,
		},
		Table: e.table,
	}, nil
}

// buildHashJoin 枚举 HashJoin 实现：自然同分布复用或插入 Exchange 强制同分布。
func (o *Optimizer) buildHashJoin(e *GroupExpr, req PhysProps) (Plan, error) {
	leftKeys, rightKeys := splitJoinKeys(e.conds)

	natural, err := o.tryJoin(e, PhysHashJoin, leftKeys, rightKeys, req, false)
	if err != nil {
		return nil, err
	}
	enforced, err := o.tryJoin(e, PhysHashJoin, leftKeys, rightKeys, req, true)
	if err != nil {
		return nil, err
	}
	return pickCheaper(natural, enforced), nil
}

// buildMergeJoin 枚举 MergeJoin 实现：排序要求直接下推给子节点，
// 使基表自然有序可被复用而省去 Sort。
func (o *Optimizer) buildMergeJoin(e *GroupExpr, req PhysProps) (Plan, error) {
	leftKeys, rightKeys := splitJoinKeys(e.conds)

	natural, err := o.tryJoin(e, PhysMergeJoin, leftKeys, rightKeys, req, false)
	if err != nil {
		return nil, err
	}
	enforced, err := o.tryJoin(e, PhysMergeJoin, leftKeys, rightKeys, req, true)
	if err != nil {
		return nil, err
	}
	return pickCheaper(natural, enforced), nil
}

func pickCheaper(a, b Plan) Plan {
	switch {
	case a == nil:
		return b
	case b == nil:
		return a
	case cheaper(b, a):
		return b
	default:
		return a
	}
}

// splitJoinKeys 把规范条件拆成左/右连接键列（顺序一一对应）。
func splitJoinKeys(conds []JoinCond) (left, right []string) {
	for _, c := range conds {
		left = append(left, c.Left)
		right = append(right, c.Right)
	}
	return left, right
}

// tryJoin 构造一棵具体连接计划。
//
// enforceDist=true 时对两侧显式插入 Exchange 使其在连接键上同分布；
// false 时仅当两侧自然分布已经同分布才返回计划。
func (o *Optimizer) tryJoin(e *GroupExpr, kind string, leftKeys, rightKeys []string,
	req PhysProps, enforceDist bool) (Plan, error) {

	leftReq := PhysProps{}
	rightReq := PhysProps{}

	// 排序需求下推：MergeJoin 要求两侧按各自连接键有序；HashJoin 不消费排序，
	// 仅在父需求带排序且计划本身能保持时才下推——为保持代价模型清晰，
	// HashJoin 的排序统一走顶层 Sort Enforcer 路径。
	if kind == PhysMergeJoin {
		leftReq.SortCols = append(leftReq.SortCols, leftKeys...)
		rightReq.SortCols = append(rightReq.SortCols, rightKeys...)
	}

	// 分布策略由调用方通过 enforceDist 选择：
	//
	//   - false：子节点按“无分布需求”求解，仅当两侧自然同分布（含同为单
	//     节点）时连接成立——对应自然满足路径；
	//   - true ：要求子节点产出连接键哈希分布（优化器在天然满足与插入
	//     Exchange 间选最优），保证两侧同分布——对应强制 Enforcer 路径。
	var leftPlan, rightPlan Plan
	var err error
	if enforceDist {
		hashLeftReq := leftReq
		hashLeftReq.Distrib = Distrib{Hash: true, Columns: leftKeys}
		hashRightReq := rightReq
		hashRightReq.Distrib = Distrib{Hash: true, Columns: rightKeys}
		leftPlan, err = o.solve(e.children[0], hashLeftReq)
		if err != nil {
			return nil, err
		}
		rightPlan, err = o.solve(e.children[1], hashRightReq)
		if err != nil {
			return nil, err
		}
	} else {
		leftPlan, err = o.solve(e.children[0], leftReq)
		if err != nil {
			return nil, err
		}
		rightPlan, err = o.solve(e.children[1], rightReq)
		if err != nil {
			return nil, err
		}
		// 自然路径：HashJoin 要求两侧自然同分布；MergeJoin 在单节点可对
		// 任意分布归并，仅当两侧都是（不同的）哈希分布时才判定不兼容。
		if kind == PhysHashJoin && !coLocated(leftPlan.Props().Distrib,
			rightPlan.Props().Distrib, leftKeys, rightKeys) {
			return nil, nil
		}
		if kind == PhysMergeJoin && incompatibleHashDistrib(
			leftPlan.Props().Distrib, rightPlan.Props().Distrib) {
			return nil, nil
		}
	}

	leftRows := leftPlan.Rows()
	rightRows := rightPlan.Rows()
	outRows := estimateRows(leftRows, rightRows)

	var joinCost int64
	switch kind {
	case PhysHashJoin:
		joinCost = o.cost.HashJoinCost(leftRows, rightRows, outRows)
	case PhysMergeJoin:
		joinCost = o.cost.MergeJoinCost(leftRows, rightRows, outRows)
	}

	props := PhysProps{Distrib: o.joinOutputDistribFromPlans(leftPlan, rightPlan, leftKeys)}
	if kind == PhysMergeJoin {
		// MergeJoin 的输出沿连接键有序；等值连接下左、右连接键位置一一
		// 对应，因此同时声明左、右两套键序，使任一侧的前缀排序需求都能命中，
		// 避免无谓再叠加 Sort。
		props.SortCols = append(append([]string(nil), leftKeys...), rightKeys...)
	}

	join := &JoinPlan{
		basePlan: basePlan{
			cost:  leftPlan.Cost() + rightPlan.Cost() + joinCost,
			props: props,
			rows:  outRows,
		},
		kind:     kind,
		keys:     append([]JoinCond(nil), e.conds...),
		left:     leftPlan,
		right:    rightPlan,
		enforced: enforceDist,
	}

	// 连接结果必须满足调用方需求；不满足则返回 nil，由顶层 Enforcer 路径兜底。
	if !satisfies(join.Props(), req) {
		return nil, nil
	}
	return join, nil
}

// joinOutputDistribFromPlans 基于具体子计划计算连接输出分布：两侧在连接键
// 上同分布哈希时，输出沿左连接键哈希分布；否则汇聚为单节点分布。
func (o *Optimizer) joinOutputDistribFromPlans(leftPlan, rightPlan Plan, leftKeys []string) Distrib {
	dl, dr := leftPlan.Props().Distrib, rightPlan.Props().Distrib
	if dl.Hash && dr.Hash {
		return Distrib{Hash: true, Columns: append([]string(nil), leftKeys...)}
	}
	if dl.Singleton && dr.Singleton {
		return Distrib{Singleton: true}
	}
	return Distrib{Singleton: true}
}

// coLocated 判定两侧是否在对应连接键上同分布（同为单节点也视为同分布）。
func coLocated(left, right Distrib, leftKeys, rightKeys []string) bool {
	if left.Singleton && right.Singleton {
		return true
	}
	// 两侧都按各自的连接键哈希分区，即满足连接同分布要求（分区位置一致）。
	if left.Hash && right.Hash &&
		sameColumnSet(left.Columns, leftKeys) && sameColumnSet(right.Columns, rightKeys) {
		return true
	}
	// 两侧哈希分布键完全相同（同名列），同样保证同分布。
	if left.Hash && right.Hash && equalDistrib(left, right) {
		return true
	}
	return false
}

// incompatibleHashDistrib 判定两个哈希分布是否无法在单节点直接归并：
// 仅当两侧都是哈希分布且分区键集合不同时返回 true。
func incompatibleHashDistrib(left, right Distrib) bool {
	return left.Hash && right.Hash && !equalDistrib(left, right)
}

// enforceSort 在子计划外套一层 Sort Enforcer；若子计划已是 Sort 则直接复用，
// 结构上保证不会出现 Sort 套 Sort。
func (o *Optimizer) enforceSort(inner Plan, cols []string) Plan {
	if s, ok := inner.(*SortPlan); ok && sameColumnSet(s.cols, cols) {
		return inner
	}
	return &SortPlan{
		basePlan: basePlan{
			cost: inner.Cost() + o.cost.SortCost(inner.Rows(), len(cols)),
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

// enforceExchange 在子计划外套一层 Exchange Enforcer；若子计划已是同分布
// Exchange 则直接复用，结构上保证不会出现 Exchange 套 Exchange。
func (o *Optimizer) enforceExchange(inner Plan, distrib Distrib) Plan {
	if ex, ok := inner.(*ExchangePlan); ok && equalDistrib(ex.Props().Distrib, distrib) {
		return inner
	}
	if equalDistrib(inner.Props().Distrib, distrib) {
		return inner
	}
	return &ExchangePlan{
		basePlan: basePlan{
			cost: inner.Cost() + o.cost.ExchangeCost(inner.Rows(), len(distrib.Columns)),
			props: PhysProps{
				SortCols: append([]string(nil), inner.Props().SortCols...),
				Distrib:  distrib,
			},
			rows: inner.Rows(),
		},
		child: inner,
	}
}
