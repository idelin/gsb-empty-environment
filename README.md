# diaglab

故障依赖有向图的根因诊断库（Go）。

## 第一部分：最小候选根集合

- `Graph.SCCs()` / `Graph.Condense()`：Tarjan 强连通分量压缩，得到冷凝 DAG。
- `MinRootCover(CoverRequest)`：覆盖定义为**可达**。候选根（≤ 20）先按 SCC
  归并（同一分量即同一根单元），再用分支定界求能解释全部观测故障的最小根集合。
- 输出 `CoverResult`：
  - `Roots` / `MinSize`：最小根集合；
  - `Evidence`：每个根到其解释的观测的最短可达路径（覆盖证据）；
  - `VerifiedOptimal`：对**原始候选节点**做 2^n 子集穷举，核对最小大小一致；
  - `CycleCheckPassed` / `CycleReports`：核对循环不被当作多个独立根——同一 SCC
    的候选覆盖集完全相同，任何节点级最优解不会同时选中同分量的两个成员。

## 第二部分：最小最坏费用决策树

- `OptimalPolicy(worlds, tests)`：世界（候选根集合）≤ 8，测试费用消耗共享预算。
  在所有 2^n 世界子集上做精确 DP：`f(S) = min_t cost(t) + max_o f(S∩t⁻¹(o))`，
  只需区分到根集合粒度。返回非预知策略树（每个节点的测试只依赖已观测结果）。
- `Policy.Format()`：打印缩进策略树。
- `Replay(policy, worlds, tests)`：逐世界重放，验证识别正确性与累计费用；
  `WorstReplayCost` 必须等于 DP 的最坏费用。

## 运行

```sh
go test ./...
go run ./cmd/diagdemo
```
