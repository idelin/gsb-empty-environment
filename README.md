# plan-repair：已有计划的增量修复（无求解器）

已有可行计划在执行过程中遭遇**任务增加**或**资源失效**时，本工具给出修复方案：

1. **先最小化被移动的任务数**，再最小化总成本（字典序目标）；
2. **冻结已执行前缀**：已执行任务既不移动，也不挪到未来重跑；
3. **不受影响任务优先保持原位**（由字典序目标天然保证）；
4. 输出**变更清单**（谁从何时改到何时、新任务排到何时）；
5. **无可行修复时保留原计划**并输出冲突证据；
6. 三种决策状态分开报告：`OPTIMAL`（最优）、`FEASIBLE`（可行但未证最优）、
   `BUDGET_UNDECIDED`（预算内未决）；另有 `INFEASIBLE`（可证明无可行修复）。

全部数据为合成整数/有理数，内部一律用 `fractions.Fraction` 精确计算，
不调用任何求解器，仅使用 Python 3.11 标准库。

## 问题模型

- 每台机器（资源）容量为 1，任务不可重叠；任务绑定机器。
- 资源失效是机器上的不可用区间 `[start, end)`；`end: null` 表示永久停机。
- `now` 是当前时刻：未执行任务不得排到 `now` 之前；`executed: true` 的任务
  属于已执行前缀，位置冻结。
- 成本：被移动任务的成本为 `|新开始 - 原开始|`；新任务成本为其开始时刻
  （理想开始为 0）。机器间相互独立，字典序目标按机器精确分解后求和。

## 算法（精确、带预算）

每台机器独立求解：

1. 与失效区间重叠的未执行任务为**被迫移动者**，其余为**候选留位者**。
2. 按移动数 k 迭代加深：k = |被迫| + extra，枚举留位者中大小为 extra
   的子集，把"被迫 + 子集 + 新任务"作为自由任务，用单机精确放置器
   （枚举顺序 + 候选开始点分支限界 + 记忆化）求该子集的最小成本。
3. 首个可行的 k 即为可证明的最少移动数；该层完全探索则成本最优。

两个预算（`budget.subset` 组合节点数、`budget.placement` 放置节点数）限制
搜索量：层未探索完但已有解 → `FEASIBLE`；预算耗尽仍无任何可行解 →
`BUDGET_UNDECIDED`；所有层探索完均不可行（永久停机导致容量不足）或
已执行前缀与失效冲突 → `INFEASIBLE`，保留原计划并给出证据。

## 输入格式（JSON）

```json
{
  "now": 5,
  "tasks": [{"id": "A", "machine": "M1", "duration": 2, "start": 6,
             "executed": false}],
  "new_tasks": [{"id": "N", "machine": "M1", "duration": 1}],
  "failures": [{"machine": "M1", "start": 6, "end": 9}],
  "budget": {"subset": 100000, "placement": 100000}
}
```

数字可以是整数、浮点或 `"3/2"`、`"1.5"` 这类字符串，均按精确有理数处理。

## 运行

```bash
python3.11 -m repair samples/resource_failure.json   # 从 JSON 文件运行
python3.11 -m repair --sample rationals              # 运行内置样例
python3.11 -m repair --list-samples                  # 列出内置样例
python3.11 -m repair --sample limited_budget --subset-budget 100000
```

## 运行样例（实际输出）

`samples/` 下 7 个样例的实际运行结果：

| 样例 | 场景 | 状态 | 移动数 | 成本 | 说明 |
|---|---|---|---|---|---|
| `resource_failure` | 失效 [6,9) 挤走 A | OPTIMAL | 1 | 9 | A: 6→15；B、C 保持原位 |
| `new_task` | 新增任务 N | OPTIMAL | 0 | 4 | N 排入空隙 [4,5)，无人移动 |
| `frozen_conflict` | 已执行 E 与失效重叠 | INFEASIBLE | – | – | 保留原计划 + frozen_conflict 证据 |
| `budget_undecided` | 同上但 subset 预算为 0 | BUDGET_UNDECIDED | – | – | 预算内未决，保留原计划 |
| `limited_budget` | 永久停机 + 新任务 | FEASIBLE | 1 | 11 | 预算只够试移动 A（界，非最优） |
| `limited_budget`（全预算） | 同上 | OPTIMAL | 1 | 3 | 证明最优：C: 4→5，N→2 |
| `permanent_shutdown` | 永久停机 [4,inf) | INFEASIBLE | – | – | B 被挤走后无空隙可容纳；即使移动 A，总需 5 > 剩 4，附容量证据 |
| `rationals` | 有理数数据 | OPTIMAL | 1 | 1/2 | A: 3/2→1，精确分数运算 |

`resource_failure` 的完整输出：

```json
{
  "status": "OPTIMAL",
  "kept_original_plan": false,
  "moves": 1,
  "cost": "9",
  "changes": [
    {"task": "A", "machine": "M1", "kind": "moved",
     "old_start": "6", "new_start": "15"}
  ],
  "evidence": [],
  "machines": {"M1": {"status": "OPTIMAL", "moves": 1, "cost": "9",
                      "subset_nodes": 1, "placement_nodes": 2, "note": ""}},
  "notes": []
}
```

字典序目标的体现：该场景存在移动 2 个任务、成本仅 4 的方案
（A→9，B→11），但"先最少移动"优先，故选择 1 次移动、成本 9 的方案。

`frozen_conflict` 的证据输出（保留原计划）：

```json
{
  "status": "INFEASIBLE",
  "kept_original_plan": true,
  "evidence": [
    {"kind": "frozen_conflict",
     "message": "executed task 'E' on machine 'M1' overlaps resource failure [5, 8) and cannot be moved or re-run",
     "details": {"task": "E", "machine": "M1", "task_start": "4",
                 "task_end": "7", "failure_start": "5", "failure_end": "8"}}
  ]
}
```

## 测试

```bash
python3.11 -m unittest discover -s tests -v
```

结果：`Ran 27 tests ... OK`（27 个测试全部通过），覆盖：数字解析、
原计划校验、空隙计算、放置器精确性与预算中断、最少移动优先于成本、
留位优先、已执行前缀冻结、三种决策状态、不可行证据、有理数精确运算、
多机分解。

## 代码结构

- `repair/model.py` — 数据模型与 JSON 解析（Fraction 精确数值）
- `repair/scheduler.py` — 单机精确放置器（排列 + 分支限界 + 预算）
- `repair/repair.py` — 修复主流程（迭代加深、状态判定、证据生成）
- `repair/cli.py` / `repair/__main__.py` — 命令行入口
- `repair/samples.py` + `samples/*.json` — 内置样例（二者内容一致）
- `tests/test_repair.py` — unittest 测试套件

## 限制

- 搜索为指数级最坏复杂度，面向小规模合成数据；预算机制保证超时可降级为
  `FEASIBLE` / `BUDGET_UNDECIDED` 而非卡死。
- 任务绑定单台机器，不支持跨机迁移与多资源需求。
