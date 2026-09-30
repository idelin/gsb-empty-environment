# release-select — 最小差异发布选择

在互斥变更、依赖变更与整数资源容量约束下，精确求解收益最大的发布变更集合。
仅使用 Python 3.11 标准库。所有示例数据均为合成数据。

## 求解语义

- **目标**：最大化总收益 `sum(benefit)`。
- **平局裁决**：收益相同 → 选中变更数量最少 → 排序后 ID 列表字典序最小。
- **依赖**：选中某变更必须同时选中其全部（传递）依赖。
- **依赖环**：不报错。环（强连通分量）被压缩为原子单元，必须整体选取或整体放弃，
  其收益/成本为成员之和。
- **互斥**：同一 `mutex_groups` 组内至多选中一个变更。
- **容量**：选中变更的 `cost` 之和不得超过整数 `capacity`。
- 允许空选择（收益 0），全为负收益时输出空集。

## 运行

```sh
python3.11 -m release_select examples/acceptance_boundary.json
python3.11 -m release_select - < input.json     # 从 stdin 读取
python3.11 -m release_select input.json --no-verify   # 跳过穷举核验
```

退出码：`0` 成功；`2` 输入读取失败、JSON 非法或实例校验失败。

## 输入格式（JSON）

```json
{
  "capacity": 10,
  "changes": [
    {"id": "A", "benefit": 5, "cost": 2, "depends_on": ["B"]},
    {"id": "B", "benefit": -2, "cost": 1}
  ],
  "mutex_groups": [["C", "D"]]
}
```

- `capacity`：非负整数，必填。
- `changes[].id`：非空字符串，唯一。
- `changes[].benefit`：整数（可为负）。
- `changes[].cost`：非负整数。
- `changes[].depends_on`：字符串数组，可选，默认 `[]`；引用的 ID 必须存在。
- `mutex_groups`：可选，默认 `[]`；每组至少 2 个已存在的变更 ID。

## 输出格式（JSON，stdout）

```json
{
  "status": "optimal",
  "selected_ids": ["A", "B"],
  "change_count": 2,
  "total_benefit": 3,
  "total_cost": 3,
  "evidence": [
    {"constraint": "capacity", "used": 3, "limit": 10, "satisfied": true},
    {"constraint": "dependency", "change": "A", "requires": "B", "satisfied": true},
    {"constraint": "dependency_cycle_group", "members": ["X", "Y"],
     "selected_members": ["X", "Y"], "satisfied": true},
    {"constraint": "mutex", "group": ["C", "D"],
     "selected_in_group": ["C"], "satisfied": true}
  ],
  "verification": {
    "method": "exhaustive-enumeration-2^n",
    "subsets_enumerated": 4,
    "feasible_subsets": 3,
    "best_benefit": 3,
    "best_ids": ["A", "B"],
    "matches_solution": true,
    "proved_optimal": true
  }
}
```

- `status`：`optimal`（已经核验器确认）或 `optimal-unverified`（`--no-verify`）。
- `evidence`：逐条约束证据。`dependency_cycle_group` 仅在存在依赖环时出现。
- `verification`：独立穷举核验器结果。核验器不复用求解器的 SCC 压缩，
  直接对原始变更的全部 `2^n` 个子集逐一检查可行性并重新推导最优；
  `proved_optimal` 为 `true` 才表示最优性被证实。

## 验收边界（已验证）

A 收益 5 依赖 B，B 收益 -2，容量允许两者：最优解为 `{A, B}` 整体收益 3；
单独选 A（收益 5）违反依赖闭包，不可行。见
`examples/acceptance_boundary.json` 与 `tests/test_release_select.py` 中
`AcceptanceBoundaryTest`。

## 算法与复杂度

求解器：Tarjan SCC 压缩依赖图 → 在分量 DAG 上枚举 `2^k` 个子集（k 为分量数），
过滤依赖闭包/容量/互斥可行的子集，按目标键取最优。
核验器：独立枚举原始变更的全部 `2^n` 个子集。
两者均为指数级精确算法，适用于中小规模实例（n 约 ≤ 20）。

## 测试

```sh
python3.11 -m unittest discover -s tests -v
```
