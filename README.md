# planrepair

已有可行计划在遭遇**任务新增**或**资源失效**后的精确修复工具。
纯 Python 3.11 标准库实现（`fractions.Fraction` 精确有理数运算），
不调用任何外部求解器——优化由自实现的深度优先分支定界完成。

## 问题模型

- **资源** `Resource`：容量 `capacity`（同一时刻可并行任务数）+ 若干可用时间窗 `[start, end)`。
- **任务** `Task`：时长、可选释放时间/截止时间和 `costs`（可用资源 → 成本，映射外的资源不可用）。
- **计划** `Plan`：任务 → `(资源, 开始时间)`，并标记**已执行任务集合**（冻结前缀）。
- **扰动** `Disruption`：新增任务 `added_tasks`、失效资源 `failed_resources`；`now` 为当前时刻。

修复规则：

- 已执行任务**冻结**：保持原位置，绝不挪到未来重跑；新安排的任务开始时间必须 `>= now`。
- 目标为字典序：**先最小化被移动任务数**（不受影响任务优先保持原位），**再最小化总成本**。
- 原计划在失效资源上的已执行任务保留在历史位置（失效只影响 `now` 之后）。
- 约束数据为合成整数或有理数（`int` / `Fraction` / `"3/4"` 字符串均可），
  搜索在输入常数公分母 `1/D` 网格上进行，保证完备性。

## 结果状态（严格区分）

| 状态 | 含义 |
|---|---|
| `OPTIMAL` | 搜索树在预算内耗尽，返回计划对 (移动数, 成本) 可证明最优 |
| `FEASIBLE` | 找到可行修复并返回，但节点预算耗尽，最优性未证实 |
| `BUDGET_UNDECIDED` | 预算耗尽且未找到任何可行修复，可行性未决，**保留原计划** |
| `INFEASIBLE` | 已证明不存在可行修复，**保留原计划**并附冲突证据 |

冲突证据（`INFEASIBLE`）：逐个任务给出原因（资源失效/不存在、
时间窗放不下时长与释放/截止约束），或容量竞争的联合记录（`tid="*"`）。

## 用法

```python
from planrepair import Disruption, Plan, Placement, Resource, Task, TimeWindow, repair

resources = [Resource("A", (TimeWindow(0, 24),)), Resource("B", (TimeWindow(0, 24),))]
tasks = [Task("t1", 3, {"A": 2}), Task("t2", 3, {"A": 1, "B": 4})]
plan = Plan({"t1": Placement("A", 0), "t2": Placement("A", 5)}, executed={"t1"})

result = repair(resources, tasks, plan,
                Disruption(added_tasks=(Task("n1", 2, {"B": 2}),),
                           failed_resources=frozenset({"A"})),
                now=4, node_budget=200_000)
print(result.status, result.moved_count, result.total_cost)
for change in result.changes:   # 变更清单: frozen / kept / moved / added
    print(change)
```

## 运行样例

`python3.11 -m planrepair` 输出（四个场景：资源失效+新增、不可行、预算未决、有理数数据）：

```
--- resource A fails at now=4, task n1 added ---
status      : optimal
moved tasks : 2
total cost  : 11
search nodes: 51 (budget 200000)
change list :
  [frozen] t1: A@0 -> A@0
  [moved ] t2: A@5 -> B@10
  [kept  ] t3: B@6 -> B@6
  [moved ] t4: A@10 -> B@13
  [added ] n1: - -> B@4

--- infeasible: n2 only eligible for failed resource A ---
status      : infeasible
conflicts   :
  - n2: resource 'A' failed

--- budget undecided: node_budget=0 ---
status      : budget_undecided

--- rational data: pack u2 (1/2) and u3 (3/4) after frozen u1 ---
status      : optimal
moved tasks : 0
total cost  : 2
change list :
  [frozen] u1: R@0 -> R@0
  [added ] u2: - -> R@5/4
  [added ] u3: - -> R@1/2
```

## 测试

`python3.11 -m unittest discover -s tests -v` —— 14 个测试全部通过，
覆盖：无扰动保持原位、新增任务零移动、资源失效最小强制移动、
移动数优先于成本的字典序、同移动数下成本最优、已执行任务不重跑、
不可行保留原计划+冲突证据、容量竞争不可行、预算未决、
紧预算下可行但未证实最优、有理数网格调度、容量并行、确定性、输入校验。

```
Ran 14 tests in 0.012s

OK
```

## 设计说明

- **完备性**：所有时间常数为有理数，取公分母 D 后在 1/D 网格上枚举候选开始时间
  （差分约束若有解则网格上必有解），分支定界因此是精确算法而非启发式。
- **剪枝**：以下界 `已移动数 + 剩余强制移动数`（原资源失效或原开始时间 < now 的任务）
  与剩余任务最小成本和对当前最优解做字典序剪枝。
- **确定性**：任务按候选数升序（同数按 id）、候选按"原位优先、成本、开始时间"排序，
  同一输入永远得到同一修复结果。
- 网格过大（公分母 > 10^6 或单任务候选 > 10^5）时抛出 `ValueError`，
  不做静默截断，以保证"最优/不可行"结论的可信性。
