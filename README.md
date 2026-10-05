# rcsp — 风险约束最短路求解器

在带**非负费用**和**非负整数风险**的有向图中，求从 `source` 到 `target`
总风险不超过预算 `B` 的最低费用路径，并输出完整路径与风险账本。

## 目标与并列规则

按字典序比较路径：

1. 总费用更低者优；
2. 费用相同，边数（跳数）更少者优；
3. 再相同，边 ID 序列字典序更小者优。

风险只是约束（≤ B），不参与优劣排序。

## 算法（`solver.go`）

标签设置法（label-setting）：每个标签记录 `(节点, 费用, 已用风险, 跳数, 边ID序列)`，
优先队列按上述目标序弹出，第一次弹出到 `target` 的标签即为最优解。

每个节点保留一个**非支配标签的 Pareto 前沿**（不是每点只留一个距离）。
标签 `l` 支配 `o` 当且仅当：

- `l.risk ≤ o.risk`（风险只影响可行性），且
- `(cost, hops, seq)` 目标序上 `l ≤ o`。

注意不能只因「同价同跳数但风险更低」就支配对方：若对方边 ID 序列更小，
它在目标序下可能产出更优解（见 `TestCrossCheckRandom` 曾捕获的反例）。
零费用环由此自然处理：绕环一圈费用、风险不变而跳数更多，必然被支配，算法终止。
每节点标签数不超过 `B+1`（同一风险级别只保留目标序最优者）。

## 复算器（`statecheck.go`）

`SolveByStateExpansion` 面向小图的独立验证器：把图展开成显式状态图
`(点, 已用风险)`（风险 ∈ [0, B]），在状态图上跑 Dijkstra，全程不涉及支配推理，
用于与主求解器对拍（`TestCrossCheckRandom`，含平行边、自环、零费用环的随机图）。

## 测试要点（`solver_test.go`）

- `TestExpensivePrefixIsKept`：**先贵后省**——便宜前缀风险超标走不通，
  必须保留更贵但低风险的中途标签，每点只留一个距离的算法会误判无解；
- `TestExpensiveFirstHopSavesLater`：先花更多、后省更多；
- `TestTieBreakFewerHops` / `TestTieBreakEdgeIDOrder`：两级并列规则；
- `TestZeroCostCycle`：零费用环可终止且不进入答案；
- `TestBudgetBoundary`：风险恰好等于 B 可行；
- `TestLedger`：账本逐边累计与总额一致。

## CLI

```sh
go build -o rcsp ./cmd/rcsp
echo '{"num_nodes":4,"edges":[{"id":1,"from":0,"to":1,"cost":1,"risk":3},
      {"id":2,"from":0,"to":1,"cost":4,"risk":0},{"id":3,"from":1,"to":3,"cost":1,"risk":1}],
      "source":0,"target":3,"budget":3}' | ./rcsp -verify
```

输出 JSON：`edge_ids`（完整路径）、`nodes`、`total_cost`、`total_risk`
以及逐边累计费用/风险的 `ledger`。`-verify` 用状态展开复算器交叉校验（仅适合小图）。

## 约束

费用必须非负；风险必须为非负整数（允许负风险会使零费用负风险环产生无限非支配标签）。
边 ID 必须全局唯一，用于最终并列规则。
