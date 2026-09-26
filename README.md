# 容量约束证据打包（evidence_packer）

在容量上限、依赖闭包完整、互斥版本组三重约束下，选择总价值最高的证据文件交付包。
纯 Python 3.11 标准库实现，无第三方依赖。

## 运行

```bash
python3 evidence_packer.py input.json [--max-nodes N] [--pretty]
python3 evidence_packer.py - < input.json   # 从标准输入读取
python3 -m unittest -v                       # 运行测试
```

退出码：`0` 正常；`2` 输入非法。

## 输入格式（JSON）

```json
{
  "capacity": 5,
  "items": [
    {"id": "a", "size": 2, "value": 5, "dependencies": ["b"], "mutex_group": null},
    {"id": "b", "size": 3, "value": 0}
  ],
  "search": {"max_nodes": 1000000}
}
```

- `capacity`：非负整数，容量上限。
- `items[].id`：唯一字符串标识（必填）。
- `items[].size`：非负整数（必填）。
- `items[].value`：整数（必填，可为 0 或负数）。
- `items[].dependencies`：依赖文件 id 数组，可省略；依赖是传递的。
- `items[].mutex_group`：互斥版本组名，同组至多选一个，可省略。
- `search.max_nodes`：分支限界节点预算，可省略；命令行 `--max-nodes` 优先。

## 选择规则

1. 所选集合的依赖闭包必须完整（选中即连带全部传递依赖）。
2. 总大小为所选文件（去重后）大小之和——重复依赖只计一次。
3. 总大小不超过容量；同一互斥组至多一个文件。
4. 目标：最大化总价值；平局依次取更小总大小、ID 字典序更小者。

## 输出格式（JSON）

```json
{
  "status": "proven_optimal",
  "proven_optimal": true,
  "capacity": 5,
  "selection": {"ids": ["a", "b"], "total_value": 5, "total_size": 5},
  "dependency_topology": {
    "edges": {"a": ["b"], "b": []},
    "topological_order": ["b", "a"],
    "acyclic": true
  },
  "feasibility_recheck": {
    "feasible": true,
    "problems": [],
    "checks": {"all_ids_known": true, "dependency_closure_complete": true,
               "mutex_groups_respected": true, "within_capacity": true},
    "recomputed_total_size": 5,
    "recomputed_total_value": 5
  },
  "search_stats": {"nodes_explored": 7, "node_budget": null, "budget_exhausted": false}
}
```

- `status` / `proven_optimal`：`proven_optimal` 表示搜索在预算内完成（含有效上界剪枝），
  结果已证最优；若节点预算耗尽提前终止，状态为 `best_known_not_proven_optimal`，
  此时 `selection` 仅为最佳已知可行解，不保证最优。
- `dependency_topology`：所选文件的依赖边、拓扑序（依赖先于依赖者）及无环标记。
- `feasibility_recheck`：独立复核——从原始输入重新校验 id 合法性、依赖闭包、
  互斥组与容量，并重算总大小/总价值，不复用求解器内部状态。

## 验收边界示例

`a(size=2, value=5, deps=[b])`，`b(size=3, value=0)`：

- 容量 4：`a` 不可选（闭包需 2+3=5 > 4），最优为空集。
- 容量 5：选 `a` 与 `b`，总大小 5、总价值 5；不会遗漏 `b` 而误报大小 2。

## 测试

```bash
python3 -m unittest -v
```

覆盖：验收边界、共享依赖去重计大小、传递闭包、依赖环、互斥组（含闭包内冲突）、
平局裁决（大小、字典序）、预算耗尽时的 best-known 语义、独立复核的各类违规检测、
输入校验与 CLI 行为。所有示例数据均为合成数据。
