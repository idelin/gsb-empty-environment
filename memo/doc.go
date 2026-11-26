// Package memo 实现一个可导入的 Cascades/Volcano 风格查询优化器 Memo 求解包。
//
// 它由四个协同部分组成：
//
//   - Memo 逻辑层：以等价 Group 组织逻辑表达式，支持 Union-Find 动态合并、
//     组内逻辑表达式去重、父表达式哈希重算与二次级联去重；
//   - 规则探索：应用连接交换律与结合律直到不动点，覆盖全部连接顺序与括号；
//   - 物理搜索：基于需求属性（排序列、分布列）的 Volcano 自顶向下搜索，
//     对每个 (Group, 需求属性) 缓存精确最优计划，权衡自然属性与
//     Sort/Exchange Enforcer，并结构上杜绝 Enforcer 无限递归；
//   - 穷举参照：ExhaustiveBest 独立枚举全物理计划空间，供验收比对全局最优。
//
// 典型用法：
//
//	schema := &memo.Schema{Tables: ...}
//	tree := memo.NewInnerJoin(memo.NewScan("t1"), memo.NewScan("t2"), conds)

// m := memo.NewMemo(schema)
// root, _ := m.Build(tree)
// m.Explore()
//
// opt := memo.NewOptimizer(m, memo.DefaultCostModel{})
// best, _ := opt.BestPlan(memo.PhysProps{SortCols: []string{"t1.a"}})
// _ = root
// _ = best
package memo
