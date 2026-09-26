# 半开区间任务的最少同质资源分配

纯 Python 3.11 标准库实现（`fractions` / `dataclasses` / `enum`），不调用任何求解器。
约束数据为合成整数或有理数（`int` / `Fraction`，内部统一为 `Fraction`）。

## 模型

- 任务占据**半开区间** `[start, end)`：`[0,5)` 与 `[5,8)` 可共用同一资源。
- **无额外约束**时，最少资源数等于**峰值重叠数**（区间图为完美图），按起点首次适配贪心即可达到。
- 额外约束：
  - `pinned`：任务指定资源编号（编号绝对固定，池大小至少为最大编号 + 1）；
  - `cannot_share`：不可同资源的任务对（即使区间不重叠也不能共用）。
- 带约束后等价于图着色，转为**带节点预算的回溯搜索**（DSATUR 选点），并报告下界/上界：
  - 下界 = max(峰值重叠, 贪心团, 最大指定编号 + 1)；
  - 上界 = 当前最好分配的资源数（初始为贪心着色）。
- **指定资源冲突提前发现**：两个任务指定同一资源且区间重叠（或被声明不可同资源），
  在进入搜索前直接返回 `INFEASIBLE`（消耗 0 节点）。

## 结果状态（互相区分）

| 状态 | 含义 |
|---|---|
| `OPTIMAL` | 找到分配且已证明资源数最少（达到下界，或更小的池已证明不可行） |
| `FEASIBLE` | 找到分配，但预算耗尽，最优性未证明（附下界/上界） |
| `UNDECIDED` | 预算耗尽，连一份完整分配都未构造出来（仅有下界） |
| `INFEASIBLE` | 约束矛盾（指定资源冲突，提前发现） |

预算语义：每次尝试给任务着色计 1 个节点（含初始贪心构造）。

## 用法

```python
from resource_alloc import Task, Status, peak_overlap, solve

tasks = [Task("a", 0, 5), Task("b", 5, 8)]          # 半开区间，可共享
result = solve(tasks)                                # OPTIMAL，1 个资源

tasks = [Task("a", 0, 5, pinned=0), Task("b", 2, 6, pinned=0)]
result = solve(tasks)                                # INFEASIBLE（提前发现，0 节点）

tasks = [Task("a", 0, 2), Task("b", 3, 5)]
result = solve(tasks, cannot_share=[("a", "b")])     # OPTIMAL，2 个资源

result = solve(tasks, cannot_share=edges, budget=4)  # UNDECIDED：预算不足
```

## 运行样例（`python3.11 demo.py`）

```
== 1. 半开区间端点相接可共享 ==
  状态: optimal
  分配: {'a': 0, 'b': 0}
  资源数: 1
  下界: 1  上界: 1  节点: 2/100000

== 2. 无额外约束：资源数等于峰值重叠 ==   （峰值重叠 = 3）
  状态: optimal
  分配: {'a': 0, 'b': 1, 'c': 2, 'd': 2, 'e': 0, 'f': 1, 'g': 0}
  资源数: 3
  下界: 3  上界: 3  节点: 7/100000

== 3. 指定资源冲突（提前发现） ==
  状态: infeasible
  下界: None  上界: None  节点: 0/100000
  说明: 任务 'a' 与 'b' 同指定资源 0，但区间重叠或被声明不可同资源

== 4. 不可同资源迫使增加资源 ==
  状态: optimal
  分配: {'a': 0, 'b': 1}
  资源数: 2

== 5. 有理数（Fraction）端点 ==
  状态: optimal
  分配: {'a': 0, 'b': 0, 'c': 1}
  资源数: 2

== 6a. 预算未决（budget=4） ==
  状态: undecided
  下界: 2  上界: None  节点: 5/4

== 6b. 可行未证最优（budget=12） ==
  状态: feasible
  分配: {'t0': 0, 't1': 1, 't2': 0, 't3': 1, 't4': 2}
  资源数: 3
  下界: 2  上界: 3  节点: 13/12

== 6c. 最优（budget 充足） ==
  状态: optimal
  资源数: 3
  下界: 2  上界: 3  节点: 13/1000000
```

样例 6 使用五元环不可同资源实例（5 个互不重叠的任务构成 C5）：
色数 3、团数 2，因此下界 < 上界，可在三档状态间切换。

## 测试结果（`python3.11 -m unittest -v`）

```
Ran 22 tests in 0.001s

OK
```

覆盖：半开区间共享/严格重叠、峰值重叠扫描、无约束时资源数等于峰值重叠、
指定资源（冲突提前发现、端点相接不冲突、编号绝对性、与不可同资源叠加冲突）、
不可同资源（迫使增加资源、奇环需 3 色）、有理数端点、
预算三档（UNDECIDED / FEASIBLE / OPTIMAL）与上下界夹逼、输入校验（9 类非法输入）。

## 文件

- `resource_alloc.py` — 核心模块（`Task` / `solve` / `peak_overlap` / `Result` / `Status`）
- `test_resource_alloc.py` — unittest 测试（22 个）
- `demo.py` — 运行样例
