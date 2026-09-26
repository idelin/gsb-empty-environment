# 预算内项目组合选择（依赖 + 互斥 + 共享依赖）

纯标准库实现（Python 3.11，`fractions` + `unittest`），不调用任何外部求解器。

## 规则

- **依赖必选**：选中项目必须选中其全部（传递）依赖；只选子项目不选父项目是非法组合，枚举时自动补闭包。
- **共享依赖只付一次**：两个项目共享同一依赖时，该依赖成本只计一次（按集合求和）。
- **互斥组**：`mutex_groups` 中每组至多选中一个成员；若必选集合的依赖闭包内部冲突，则该需求不可行。
- **负收益依赖**：依赖本身收益可以为负，只要被高收益子项目拉动仍可进入最优组合。
- **依赖环（必须显式表态）**：
  - `cycle_policy="group"`（默认）：用 Tarjan 求强连通分量，环作为一个整体原子组，要么整组选、要么整组不选，成本/收益合并；
  - `cycle_policy="reject"`：存在环即抛 `CycleError`，拒绝该实例。
- **精确数值**：成本/收益/预算接受 `int`、`Fraction` 或 `"p/q"`、`"1.5"` 有理数字符串；禁止 `float`。非负成本，负成本报错。
- **三类问题分开回答**：
  - 可行性 `FEASIBLE / INFEASIBLE`：`check_feasibility`（可给 `required`）；
  - 预算内最优 `OPTIMAL / OVER_BUDGET / INFEASIBLE`：`optimize(..., budget=...)`；
  - 预算未决 `UNDECIDED`：`optimize(..., budget=None)` 或 `undecide(...)`，返回完整成本-收益 Pareto 前沿。

### 状态语义

| 状态 | 含义 |
|---|---|
| `FEASIBLE` | 必选项目的依赖闭包无互斥冲突（结构可行） |
| `INFEASIBLE` | 必选集合在依赖/互斥约束下自相矛盾（或环组内互斥） |
| `OPTIMAL` | 预算内找到最优；空组合本身可行，所以无项目可买时也返回 `OPTIMAL` 且 `projects=()` |
| `OVER_BUDGET` | 必选项目结构可行，但其最小依赖闭包成本超过预算 |
| `UNDECIDED` | 未给预算，返回 Pareto 前沿；每个前沿点对某个预算都是最优 |
| `INVALID_CYCLE` | 预留状态；`reject` 策略实际以 `CycleError` 抛出 |

## 算法

Tarjan SCC 折叠环 → 以 SCC 为原子、位掩码表示选中/排除集合 → DFS 分支定界：
包含一个原子时按依赖做闭包传播并排除互斥伙伴，排除一个原子时级联排除依赖它的原子；预算搜索用可行上界（剩余正收益之和）剪枝，并用"最小非空成本"快速判定。Pareto 前沿枚举全部可行闭包后做非支配过滤。

## 运行

```bash
python3.11 project_select.py        # 运行样例
python3.11 -m unittest test_project_select -v
```

## 快速使用

```python
from project_select import optimize, check_feasibility, undecide

projects = {
    "platform": {"cost": 10, "value": -2, "deps": []},
    "app_a":    {"cost": 3,  "value": 8,  "deps": ["platform"]},
    "app_b":    {"cost": 4,  "value": 6,  "deps": ["platform"]},
}

optimize(projects, budget=17, mutex_groups=[("app_a", "app_b")])
check_feasibility(projects, required=["app_a"])
undecide(projects)  # 预算未决：Pareto 前沿
```

## 测试

37 个用例覆盖：共享依赖只计一次、子不选父非法、负收益依赖被拉动、
互斥（含环内互斥、跨依赖冲突）、环两种策略、有理数精确计算、
输入校验、Pareto 前沿，以及对约 40×20 个随机实例与暴力枚举子集的逐一对拍。

```
Ran 37 tests in 0.129s

OK
```
