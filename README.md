# 预算化规则最小删除（Budgeted Minimum Rule Deletion）

规则集合构成有向优先关系图。当图中存在环时，删除总代价最小的一组规则（边），
使剩余图无环——即**最小权反馈弧集**（minimum-weight feedback arc set）。
纯 Python 3.11 标准库实现，无第三方依赖。

## 语义与平局规则

- 每条规则成本为正整数；目标是最小化被删规则的总成本。
- 平局依次打破：① 删除条数最少；② 被删 ID 排序后的字典序最小。
- 共享边只计一次：一条边若同时参与多个环，删除它一次即可同时破所有环，
  求解器在全局图上精确搜索，不会把"逐环最低删除"简单相加。

## 算法

- **精确分支限界**：每次找出一个环，对环上每条边分支"删除该边"；
  按边成本升序展开子节点，先用贪心（反复删环上最便宜边）得到初始可行解。
- **下界**：边不相交环包装（edge-disjoint cycle packing）——反复找一个环，
  计入环上最小边成本并将该环的边移出考虑。每个环至少要删一条边，
  且所计环边不相交，故求和是合法下界。
- **预算**：`--budget N` 限制分支扩展的节点数。预算耗尽时返回当前最优
  **可行解**（已经过独立验证器检查）与**合法下界**（所有被预算截断的
  前沿节点下界的最小值），并置 `optimal: false`、`budget_exhausted: true`。
- **拓扑顺序**：对剩余图用堆优化的 Kahn 算法，输出字典序确定的拓扑序。

## 输入格式（JSON）

```json
{
  "rules": [
    {"id": "e1", "from": "A", "to": "B", "cost": 1}
  ]
}
```

- `id`：非空字符串，全局唯一。
- `from` / `to`：非空字符串节点名；允许自环与平行边（自环必须被删除）。
- `cost`：正整数（布尔值与浮点数会被拒绝）。

## 输出格式（JSON）

| 字段 | 含义 |
| --- | --- |
| `optimal` | 是否已证明最优（搜索完整结束） |
| `budget_exhausted` | 预算是否耗尽 |
| `deleted` | 被删规则 ID（字典序排序） |
| `deleted_cost` / `deleted_count` | 总成本 / 删除条数 |
| `lower_bound` | 最优解成本的合法下界；`optimal` 为真时等于 `deleted_cost` |
| `topological_order` | 剩余图的拓扑顺序 |
| `nodes_explored` / `budget` | 已扩展节点数 / 预算 |
| `verified` | 独立验证器对本输出的复核结果 |

## 命令行用法

```bash
# 求解（输入文件，或 '-' 读标准输入）
python3.11 min_delete.py examples/three_cycle.json
python3.11 min_delete.py examples/shared_edge.json --budget 1000 --output result.json

# 独立验证模式：对照原始实例复核一份结果文件，非法时退出码为 1
python3.11 min_delete.py examples/shared_edge.json --verify result.json
```

退出码：`0` 成功（验证模式表示结果合法）；`1` 验证失败；`2` 输入非法。

## 独立验证器

`verify_solution(rules, deleted_ids, topo_order)` 与求解器相互独立
（用 Kahn 算法而非求解器的 DFS 判环），检查：删除 ID 合法且无重复、
拓扑序是节点的排列、所有保留边在拓扑序中前向、剩余图确实无环，并重算成本。

## 测试

```bash
python3.11 -m unittest test_min_delete -v
```

覆盖：验收边界（三环删成本 1 边；共享边不重复计费）、平局规则、自环/平行边/
空实例、40 组随机合成实例与蛮力枚举对拍、预算耗尽时的可行性与下界有效性、
验证器拒绝非法解、CLI 端到端往返。所有数据均为合成数据。
