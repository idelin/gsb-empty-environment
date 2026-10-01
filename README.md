# 有限缓存重计算规划器（Bounded-Memory Recomputation Planner）

对有向无环计算图（DAG）求**最小总计算成本**的执行序列：每个节点有整数
计算成本与结果大小；执行节点时其**全部父结果必须驻留内存**；内存上限固定；
允许随时免费驱逐缓存结果，也允许重算（每次重算都重新付费）。
目标：使所有指定输出节点最终同时驻留内存，且总计算成本最小。

仅使用 Python 3.11 标准库，无第三方依赖。

## 计算模型

- 计算节点 `n` 的瞬时峰值内存为 `size(n) + Σ size(parent)`，必须 ≤ 内存上限，
  否则该节点永远无法计算（整体不可行）。**父结果不会在计算中被隐式丢弃**。
- 每步计算完成后可免费驱逐任意缓存结果；保留的缓存总大小必须 ≤ 上限。
- 同一节点可计算多次，每次都计入成本（重算）。
- 所有输出必须能同时驻留，否则不可行。

## 算法

- 以"当前缓存集合"为状态做精确 Dijkstra 搜索（边权 = 节点计算成本 ≥ 0）。
- 转移只枚举**极大保留集**（驱逐免费，非极大集合被同成本的极大超集支配，
  不损失最优性）；另有超集支配剪枝与"只算输出祖先节点"剪枝。
- 小图精确求解；`expansion_budget` 限制状态扩展数，预算耗尽时中断并保留：
  - `lower_bound`：Dijkstra  frontier 最小成本，是最优值的有效下界；
  - `upper_bound`：已生成的最优可行目标状态成本（若存在），并附对应计划
    （该计划可行但**未证最优**，输出中 `optimal: false`）。

## 命令行

```bash
python3 planner.py INPUT.json [-b BUDGET] [--compact]
```

- `INPUT.json`：问题描述文件（格式见下）。
- `-b/--budget`：覆盖输入文件中的 `expansion_budget`。
- `--compact`：单行紧凑输出。
- 退出码：`0` = 求得最优解；`1` = 不可行或被预算中断；`2` = 输入非法。

## 输入格式

```json
{
  "memory_limit": 7,
  "nodes": [
    {"id": "A", "cost": 5, "size": 4, "parents": []},
    {"id": "B", "cost": 1, "size": 2, "parents": ["A"]}
  ],
  "outputs": ["B"],
  "expansion_budget": 100000
}
```

- `memory_limit`（必需）：非负整数，内存上限。
- `nodes`（必需）：节点数组。`id` 唯一非空字符串；`cost`、`size` 为非负整数；
  `parents` 为已定义节点 id 数组（可省略，默认 `[]`）。图必须无环。
- `outputs`（必需）：非空输出节点 id 数组。
- `expansion_budget`（可选）：状态扩展预算。

## 输出格式

```json
{
  "status": "optimal | infeasible | interrupted",
  "total_cost": 14,
  "lower_bound": 14,
  "upper_bound": 14,
  "expansions": 8,
  "memory_limit": 7,
  "steps": [
    {
      "step": 1,
      "compute": "A",
      "cost": 5,
      "parents": [{"id": "P", "size": 2, "resident": true}],
      "peak_memory": 4,
      "memory_before": 0,
      "memory_after": 4,
      "cached_after": ["A"],
      "evicted": []
    }
  ]
}
```

每步包含：计算的节点与成本、**依赖证据**（各父节点 id/大小/驻留标志）、
瞬时峰值内存、步骤前后内存占用、步骤后缓存集合、本步驱逐的节点。

- `infeasible`：附 `reason`；若因峰值超限，附 `violations`（节点及所需峰值）。
- `interrupted`：附 `lower_bound`（有效下界）与 `upper_bound`（可能为
  `null`）；若有可行计划则附 `steps` 且 `optimal: false`。

## 示例

```bash
python3 planner.py examples/shared_parent.json      # 共享父节点复用，最优 7
python3 planner.py examples/forced_recompute.json   # 内存紧张强制重算，最优 14
python3 planner.py examples/infeasible.json         # 峰值超限，不可行
python3 planner.py examples/forced_recompute.json -b 3   # 预算中断，保留界
```

## 测试

```bash
python3 -m unittest test_planner -v
```

包含：验收边界（共享父节点复用、峰值超限不可行）、强制重算、计划一致性
（逐步校验依赖证据与内存占用）、预算中断界的有效性、输入校验、CLI 冒烟，
以及 60 个随机小图上与"枚举全部保留子集"参考求解器的最优性交叉验证。

## 适用范围与限制

- 面向**小图精确求解**：状态空间随可同时驻留的节点数指数增长，
  大图请使用 `expansion_budget` 获得带界的中断结果（此时不保证最优）。
- 中断结果的下界是 frontier 最小成本（松界），上界仅在已生成目标状态时存在。
