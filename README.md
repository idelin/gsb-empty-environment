# replica_planner

为对象选择多个故障域不同的节点放置副本：在节点容量与反亲和约束下最小化总传输成本，
容量不足时输出诊断。纯 Python 3.11 标准库实现（`fractions.Fraction` 精确有理数），
不调用任何外部求解器；测试使用 `unittest`。

## 约束模型

- **机架隔离（故障域）**：同一对象的任意两个副本必须处于不同机架；同机架不同节点不满足隔离。
- **节点唯一**：一个对象的副本不能重复计在同一节点。
- **容量**：`capacity >= 1` 的节点才可接纳新副本；`capacity == 0` 表示已满/下线。
- **保留免费**：已持有副本的节点保留成本为 0，迁入新节点收取 `transfer_cost`。
- **迁移不变式**：迁移计划先全部 `add` 再全部 `remove`，任何时刻副本数不低于 `min_replicas`。

## 结果类别（严格分离）

| 状态 | 含义 |
| --- | --- |
| `OPTIMAL` | 分支定界搜索完成，成本已证明最优 |
| `FEASIBLE` | 搜索预算（`max_expansions`）耗尽，已持有可行解但最优性未证明 |
| `BUDGET_UNDECIDED` | 搜索预算耗尽且尚无可行解，预算内可行性未决 |
| `INFEASIBLE` | 已证明不可行，附 `CapacityDiagnostics` 缺容量诊断 |

给定 `budget` 时，`SolveResult.within_budget` 为 `True` / `False` / `None`（未决）。

## 用法

```python
from fractions import Fraction
from replica_planner import Node, ObjectSpec, solve, build_migration_plan

nodes = [
    Node("a1", "rackA", 1, 5),
    Node("b1", "rackB", 1, Fraction(3, 2)),
    Node("c1", "rackC", 1, 4),
    Node("d1", "rackD", 0, 6),          # 已下线
]
spec = ObjectSpec("obj-1", replicas=3, min_replicas=2, current=("a1", "b1", "d1"))
result = solve(nodes, spec, budget=Fraction(9, 2))
plan = build_migration_plan(spec.current, result.placement, spec.min_replicas)
```

## 运行

```bash
python3.11 -m replica_planner   # 运行样例（四种结果类别）
python3.11 -m unittest -v       # 运行测试（含 200 组随机实例与暴力枚举交叉验证）
```

## 求解方法

自实现分支定界：按机架分支（每机架至多选一个节点，可直接跳过），
下界为「已累积成本 + 剩余机架中最便宜的 need 个机架最小成本之和」。
`max_expansions` 限制扩展节点数，耗尽时按上表返回 `FEASIBLE` 或 `BUDGET_UNDECIDED`。
