# facility — 树上设施选址求解器

在一棵非负边长树上选择**至多 K 个节点**开设设施，最小化所有节点的
`需求 × 到最近设施的距离` 之和。部分节点可被禁止选址。需求为非负整数，
边长为非负浮点数（允许零长度边）。

## 构建与运行

```sh
go build -o fsolver .
./fsolver -i input.json -o output.json
cat input.json | ./fsolver            # 默认从 stdin 读、向 stdout 写
```

命令行参数：

- `-i` / `-o`：输入 / 输出 JSON 文件（缺省为标准输入 / 标准输出）
- `-budget ms`：启发式回退的时间预算（覆盖输入中的 `time_budget_ms`）
- `-force-heuristic`：跳过精确 DP，强制走启发式（用于测试）
- `-max-exact-entries N`：精确 DP 状态数上限（默认 4,000,000）

## 输入格式

```json
{
  "n": 3,
  "k": 2,
  "edges": [{"u": 0, "v": 1, "w": 1}, {"u": 1, "v": 2, "w": 2.5}],
  "demands": [5, 0, 5],
  "forbidden": [1],
  "time_budget_ms": 2000
}
```

- `edges` 必须恰好 `n-1` 条且构成一棵树（会校验连通、无环、非自环、非负权）
- `demands` 长度为 `n` 的非负整数
- `forbidden` 为禁止选址的节点列表（这些节点仍有需求、仍需被服务）
- `time_budget_ms` 可选，仅对启发式回退生效

## 输出格式

```json
{
  "status": "optimal",
  "facilities": [0, 2],
  "assignments": [0, 0, 2],
  "total_cost": 0,
  "optimal": true
}
```

- `status`：`optimal`（精确解）、`feasible`（预算耗尽时的可行上界）、`infeasible`
- `assignments[i]`：服务节点 i 的设施节点；等距时归属**设施 ID 较小者**；无设施时为 -1
- `total_cost`：按上述归属规则重新计算的总成本，与输出自洽；不可行时为 `null`
- 不可行场景（`k=0` 或全部节点禁止选址且总需求为正）返回 `infeasible`；
  总需求为 0 时返回成本 0 的空设施最优解

## 算法

- **精确 DP**（默认，状态数 `n²(K+1)` 不超阈值时）：以节点 0 为根，
  状态 `f[v][j][u]` 表示子树 v 内放置 j 个设施、且 v 由设施 u 服务时
  子树的最小成本；子节点通过背包合并，边可“切断”形成独立划分。
  树上最近设施区域（等距按 ID 划分）是连通的，因此该划分 DP 给出
  k-中位精确最优。复杂度 `O(n²K²)` 时间、`O(n²K)` 空间，含解重构。
- **启发式回退**（规模超阈值或 `-force-heuristic`）：1-中位换根种子 +
  自适应候选池贪心加点 + 交换局部搜索，全程受时间预算约束，
  预算耗尽时返回当前最优可行解（`status: feasible`，即最优值的上界）。

## 测试

```sh
go test ./...
```

包含：小树上与暴力枚举设施子集的对拍（随机 300 组，含零长度边）、
零长度边、全禁止选址、`k=0`、单节点、等距归属 tie-break、
启发式可行性与上界性质、中等规模精确解等。
