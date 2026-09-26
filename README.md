# 单机调度：释放时间 + 截止时间 + 类型切换耗时，最小化总迟延

纯 Python 3.11 标准库实现（`fractions.Fraction` 精确算术），不调用任何外部求解器。

## 问题模型

- 任务 `Job`：释放时间 `release`、加工时长 `processing`、截止时间 `due`、类型 `job_type`；
- 类型切换耗时 `SetupMatrix`：**同类型切换为 0**，不同类型按矩阵计耗时（支持对称/一致/任意有向矩阵）；
- 目标：最小化总迟延 `Σ max(0, C_j − d_j)`；
- 给定次序按最早开工构造半主动排程，未释放时插入被迫空闲；搜索覆盖全部次序，因此
  **“空等后续任务释放”优于“立即加工当前可加工任务”** 的情形（见样例 1）也能被找到；
- 数据支持整数与有理数（`int` / `float` / `str` / `Fraction`，如 `"3/2"`），全程精确计算。

## 求解器

`BranchAndBoundSolver(jobs, setups, budget).solve()`：

- 深度优先分支定界；下界 = 已发生迟延 + 剩余任务下界（位置松弛与单任务松弛取大）；
- 占优剪枝：已排集合与末尾类型相同的状态下，(时间更早, 迟延更小) 占优；
- 贪心构造初始上界（不计预算）；
- `SearchBudget(max_nodes=…, max_seconds=…)` 限制搜索预算。

### 结论三态（可行 / 最优 / 预算未决 明确分开）

| `Status` | 含义 |
|---|---|
| `OPTIMAL` | 搜索空间耗尽，已证明最优（`objective == lower_bound`） |
| `FEASIBLE` | **预算未决**：预算耗尽，返回最佳可行解，不贴最优标签；`gap = objective − lower_bound` 给出质量界 |

`SolveResult` 字段：`schedule`（最佳可行排程）、`objective`（上界）、`lower_bound`（全局下界）、
`nodes_explored`、`budget_exhausted`。

## 运行

```bash
python3.11 main.py                      # 运行样例
python3.11 -m unittest discover -s tests -v   # 运行测试
```

## 运行样例（`python3.11 main.py` 摘要）

样例 1：t=0 时只有长任务 A 可加工，但最优解选择空等 3 个单位先做工期紧的 B：

```
--- 样例 1：空闲优于立即执行（最优次序先等 B） ---
状态: optimal  (已证明最优: True)
总迟延(上界): 6   全局下界: 6   间隙: 0   展开节点: 2
任务    类型      切换    空闲    开工    完工    截止    迟延
B     Y        0     3     3     5     5     0
A     X        1     0     6    16    10     6
```

样例 2（8 任务、3 类型、含有理数数据）：预算 `max_nodes=5` 时返回最佳可行解并标记
`feasible`（预算未决），不限预算时证明最优：

```
--- 样例 2a：搜索预算 max_nodes=5（预算未决，返回最佳可行解） ---
状态: feasible  (已证明最优: False)
总迟延(上界): 99/2   全局下界: 1/2   间隙: 49   展开节点: 5
--- 样例 2b：不限预算（证明最优） ---
状态: optimal  (已证明最优: True)
总迟延(上界): 35/2   全局下界: 35/2   间隙: 0   展开节点: 199
```

## 测试结果

`python3.11 -m unittest discover -s tests`：**13 个测试全部通过（OK，约 2.4s）**，覆盖：

- 同类型无切换、异类型计耗时、未释放任务的被迫空闲；
- 有理数数据的精确计算；
- 空闲优于立即执行的实例（最优次序先等后到的紧任务）；
- 多个随机整数/有理数实例上与全排列穷举对照最优值；
- 预算受限时返回 `FEASIBLE`（非最优标签）且解可行、上界不优于真最优、下界有效；
- 时间预算、空实例、单任务、重复 job_id 校验。

## 目录结构

```
scheduler/
  model.py     # Job / SetupMatrix / Schedule / build_schedule
  solver.py    # BranchAndBoundSolver / SearchBudget / Status / SolveResult
main.py        # 运行样例
tests/test_scheduler.py
```
