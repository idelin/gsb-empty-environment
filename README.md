# treeplan

Go 实现的树状分组折叠规划工具，包含两个子问题的求解器与独立核对器。

## 问题 1：最小展开集合（≤ 20 组）

给定分组树与目标叶子集合，选择一组要展开的分组，使得：

- 所有目标叶子可见（其全部祖先分组展开）；
- 展开集合满足祖先闭包：展开子组必须先展开父组；
- 总成本最小 = 展开分组成本之和（多个目标共享的祖先只计一次）
  + 被额外暴露的非目标叶子的罚分之和。

`SolveExpandSet` 对 2^n 个子集穷举（n ≤ 20），逐一检查祖先闭包与目标
可见性后取最小成本。`SolveExpandSetDP` 用树形 DP 独立求解同一问题，
测试中对拍两者结果；`VerifyExpandSet` 通过树遍历（而非位运算）重新
校验闭包、目标可见性与成本。

## 问题 2：受限峰值内存下的展开/折叠序列（≤ 12 组）

展开分组会加载数据块（分组自身块 + 直接暴露的叶子块），多个目标共享
祖先分组的块（只加载一次）。服务目标会打开一个持久视图，引用该目标
叶子的数据块；折叠祖先分组时，仍被其他视图引用的块不能释放。

`SolveSequence` 在 `(已展开集合, 已服务集合)` 状态空间上跑 Dijkstra，
动作包括 expand / collapse / serve，约束每步加载字节数不超过预算，
最小化总动作费用。配套核对：

- `Replay`：逐步引用账模拟器，独立校验动作合法性、逐步引用计数、
  内存与峰值，并要求所有目标最终被服务；
- `BruteForceSequenceCost`：深度优先分支限界，在小实例上与 Dijkstra
  对拍成本与可行性。

## 使用

```sh
go run ./cmd/treeplan expand-set examples/expand_set.json
go run ./cmd/treeplan sequence examples/sequence.json
```

输入为 JSON（见 `examples/`）：`root` 为分组树，分组带
`expandCost`/`collapseCost`/`memory`，叶子带 `penalty`/`memory`/
`serveCost`；`targets` 为目标叶子名列表；`budget` 为问题 2 的峰值
内存预算。

## 测试

```sh
go test ./...
```

覆盖：共享祖先成本只计一次、暴露罚分、穷举与 DP 对拍（含 20 组上界）、
引用保留导致的预算下界、共享子树数据只加载一次、Dijkstra 与暴力搜索
对拍、回放器对非法计划的拒绝。
