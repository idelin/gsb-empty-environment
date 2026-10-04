# recovery-planner

恢复规划器：在有限磁盘额度内安排 **压缩块下载 → 临时块解压 → 原子发布**，
输入依赖 DAG、各阶段空间占用与释放时点，求使 **峰值磁盘占用最小** 的可行执行顺序。

## 模型

- 每个块（block）三个阶段顺序执行：`download`（压缩块）、`temp`（解压临时区）、
  `final`（原子发布）。发布是原子的：`final` 先完整出现，再按释放时点回收旧空间。
- 磁盘按 **命名 chunk** 记账：同名 chunk 是同一份物理数据，存活期间只算一次
  （共享块去重）；阶段可用 `size`（私有匿名空间）或 `chunks`（显式分解，可跨块共享）。
- 每个阶段的释放时点可配：
  - `on_decompress`：本块解压完成时（download 默认）
  - `on_publish`：本块发布完成时（temp 默认）
  - `after_dependents`：所有依赖本块的块都发布后
  - `end_of_plan`：整个计划结束（final 默认）
- 峰值是逐事件复算出来的，**不会把最终大小当成峰值**（峰值通常大于终态占用）。
- 依赖边 `deps` 构成 DAG；一个块必须在其全部依赖发布之后才能开始。

## 用法

```sh
go build -o recovery-planner ./cmd/recovery-planner
./recovery-planner -input example.json -trace
```

输入（JSON，也可从 stdin 读）：

```json
{
  "budget": 70,
  "limits": {"timeout_ms": 2000, "max_expanded": 1000000},
  "blocks": [
    {"id": "base", "download": {"size": 10}, "temp": {"size": 22}, "final": {"size": 8}},
    {"id": "app", "deps": ["base"], "download": {"size": 8}, "temp": {"size": 25},
     "final": {"chunks": [{"name": "shared", "size": 30}, {"name": "app-delta", "size": 3}]}}
  ]
}
```

输出：`status`（`optimal` / `feasible` / `unknown` / `infeasible`）、执行顺序 `order`、
`peak`、`final`、`upper_bound`、`lower_bound`、展开节点数与是否被预算中断。
`-trace` 把逐事件的磁盘水位打印到 stderr。计划被证明不可行时退出码为 2。

## 算法与界

1. **贪心构造**：每步在就绪块中选使当前峰值上升最小者，多项式时间给出一个
   *已证可行上界*（真实可执行计划的峰值）。
2. **分支限界枚举拓扑序**：小 DAG 上完整枚举复算，前缀峰值 ≥ 现任最优即剪枝，
   候选按前缀峰值升序访问以加速收敛；跑完即 `optimal`（上界 = 下界）。
3. **预算中断**（`max_expanded` / `timeout_ms` 任一耗尽）时返回：
   - 上界：当前最优具体计划的峰值（始终合法）；
   - 下界：`max(静态下界,  frontier 下界)`。静态下界 = 终态必存活 chunk 并集大小
     与每个块单独执行所需峰值的最大值；frontier 下界 = DFS 当前路径上所有未访问
     兄弟节点前缀峰值的最小值——任何未探索的完整拓扑序都必然经过其中某个节点，
     故其峰值不低于该值，界合法。

## 测试

```sh
go test ./...
```

覆盖：峰值复算（峰值 ≠ 终态）、共享块只算一次、`after_dependents` 释放、
非法输入（环/未知依赖/冲突 chunk）、随机小 DAG 上与暴力枚举对拍最优性、
预算中断时下界 ≤ 真实最优 ≤ 上界。
