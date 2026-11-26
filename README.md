自顶向下（Cascades/Volcano）优化器在把等价变换规则应用到 Memo 森林时，新生成的等价表达式可能意外命中另一个已有 Group，必须立刻合并两个等价组并级联去重所有父表达式，同时在代价搜索阶段正确权衡自然满足排序/分布属性与插入 Sort/Exchange 强制算子（Enforcer）。为查询优化器交付可导入的 Go Memo 求解包。输入基表统计（行数、已有排序键与分布键）、初始逻辑连接树、代价模型（Scan、HashJoin、MergeJoin、Sort、Exchange 的精确整型代价公式）及等价规则集（连接交换律、连接结合律）。核心要求：支持等价组动态合并（Union-Find 重定向、组内逻辑表达式去重、触发父表达式哈希重算与二次级联去重）；物理搜索支持需求属性（所需排序列与分布列），对每组每种需求属性缓存精确最优物理计划（含先不排序后加 Sort Enforcer 与直接下推排序要求给子节点供 MergeJoin 复用两种路径比较），且严禁因 Enforcer 套 Enforcer 陷入无限递归。验收在小连接树（≤5 表）上对比全物理计划空间穷举的全局最小代价与最优计划等价性，覆盖结合律触发两个既有 Group 合并与父表达式去重、MergeJoin 复用基表自然有序省去 Sort、插入 Exchange 满足 HashJoin 同分布，以及防 Enforcer 递归循环。

## 交付内容

可导入包 `memo`（module `prescreen.local/ps-4a48ba7917`），无第三方依赖。

- `memo/types.go`：基表统计 `Table`/`Schema`、逻辑树 `LogicalNode`、需求属性 `PhysProps`。
- `memo/memo.go`：Memo 森林、Union-Find（`Find` 路径压缩）、逻辑表达式去重、
  等价组合并（`mergeGroups`）、父表达式哈希重算与二次级联去重（`cascade`）。
- `memo/rules.go`：连接交换律、结合律，迭代到不动点的 `Explore`。
- `memo/cost.go`：物理计划节点与 `CostModel`（精确整型公式，含默认实现，可整体替换）。
- `memo/optimizer.go`：Volcano 物理搜索，按 (Group, 需求属性) 缓存最优计划，
  比较“顶层 Sort Enforcer”与“排序下推 MergeJoin 复用”，Enforcer 放松递归 +
  in-flight 循环检测双重防递归。
- `memo/exhaustive.go`：独立的全物理空间穷举器 `ExhaustiveBest`，作为验收参照。

## 快速验证

```
go test ./...
go test -race ./...
```

测试覆盖：Union-Find 重定向、结合律全形态探索、注入等价 Group 触发合并与
父表达式哈希重算/二次级联去重、MergeJoin 复用自然有序省去 Sort、Exchange
满足 HashJoin 同分布、Enforcer 防递归，以及 3~5 表上 Memo 与穷举器的全局
最优代价/计划结构完全一致（默认与自定义代价模型各一组）。
