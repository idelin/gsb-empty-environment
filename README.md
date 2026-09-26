# pareto_plans — 资源约束下的多目标方案枚举

纯 Python 3.11 标准库实现（`fractions.Fraction` 精确有理数运算），**不调用任何求解器**：
显式枚举所有任务方案组合，筛选 Pareto 非支配解，并按类别分桶返回。

## 问题模型

- 每个任务（task）有若干候选方案（`Plan`），每个方案有三个最小化目标：**成本 cost、时延 latency、能耗 energy**。
- 一个组合（`Combination`）= 每个任务恰好选一个方案；组合目标值 = 各方案目标值之和。
- 资源约束（`Constraints`）：
  - `max_latency` / `max_energy`：**硬约束**，违反即不可行，绝不返回；
  - `max_cost`：**预算约束**，超预算但硬可行的组合进入"预算未决"桶，等待预算决策。
- 约束数据为合成整数或有理数：接受 `int` / `Fraction` / 数字字符串（如 `"3/2"`、`"1.5"`），
  **拒绝 `float`** 以保证所有比较精确。

## 分类规则（三类严格分开）

在全部硬可行组合上求 Pareto 前沿（A 支配 B ⟺ A 各维度 ≤ B 且至少一维严格 <）：

| 类别 | 含义 |
| --- | --- |
| `optimal` | 硬可行、预算内、非支配 |
| `feasible` | 硬可行、预算内、但被支配 |
| `budget_undecided` | 硬可行、超预算、非支配 |

超预算且被支配的组合直接丢弃，不出现在任何类别中。
由于超预算组合的成本严格高于所有预算内组合，它不可能支配预算内组合，
因此联合前沿可以干净地拆成上述三类。

## 关键语义

- **一维更好一维更差不能互删**：两个组合若各在一维占优则不可比，都保留在前沿上。
- **等值代表**：目标向量完全相同的组合只保留组合 ID（按任务序的方案 ID 元组）最小者。
- **明确截断**：每类最多返回 `max_results` 条（按 cost、latency、energy、ID 排序后截取），
  通过 `truncated` / `omitted_count` / `total_count` 显式报告。
- **独立可行**：所有返回的组合均各自满足硬约束；预算状态与所属类别一致。

## 目录结构

```
pareto_plans/
  models.py       # Plan / Constraints / Combination，精确有理数转换
  enumerator.py   # 枚举、支配判定、前沿计算、分类与截断
examples/
  run_sample.py   # 可运行样例（合成整数 + 有理数数据）
tests/
  test_enumerator.py  # unittest 测试（17 个用例）
```

## 运行方式

```bash
python3.11 examples/run_sample.py                  # 完整结果
python3.11 examples/run_sample.py --max-results 3  # 演示截断
python3.11 -m unittest discover -s tests -v        # 运行测试
```

## 运行样例

`python3.11 examples/run_sample.py` 输出：

```
=== Pareto plan enumeration (exact rational arithmetic, no solver) ===
tasks: 3 (encode, compress, transmit), plans: 10
constraints: cost <= 14 (budget), latency <= 13 (hard), energy <= 12 (hard)
combinations considered: 36
hard-feasible combinations: 34

[optimal] showing 4 of 4 (truncated: no, omitted: 0)
  C1+E1+T2       cost=11   latency=13   energy=12
  C1+E2+T2       cost=13   latency=11   energy=12
  C1+E1+T3       cost=13   latency=13   energy=10
  C3+E1+T1       cost=14   latency=12   energy=19/2

[feasible] showing 1 of 1 (truncated: no, omitted: 0)
  C1+E3+T1       cost=14   latency=12   energy=23/2

[budget_undecided] showing 10 of 10 (truncated: no, omitted: 0)
  C2+E2+T2       cost=15   latency=9    energy=12
  C1+E2+T3       cost=15   latency=11   energy=10
  C3+E1+T2       cost=16   latency=10   energy=19/2
  C2+E2+T3       cost=17   latency=9    energy=10
  C3+E2+T2       cost=18   latency=8    energy=19/2
  C3+E1+T3       cost=18   latency=10   energy=15/2
  C3+E3+T1       cost=19   latency=9    energy=9
  C3+E2+T3       cost=20   latency=8    energy=15/2
  C3+E3+T2       cost=21   latency=7    energy=9
  C3+E3+T3       cost=23   latency=7    energy=7
```

说明：样例中 `E1b` 与 `E1` 目标完全相同，其组合被等值代表规则去重（保留 ID 更小者）；
`--max-results 3` 时各类显示 `truncated: yes` 及 `omitted` 数量。

## 测试结果

`python3.11 -m unittest discover -s tests`（Python 3.11）：

```
Ran 17 tests in 0.001s

OK
```

覆盖：支配与不可比判定、等值代表按 ID 选择、三类分离且互不相交、
超预算被支配组合丢弃、返回组合均独立硬可行、有理数精确求和（如 1/3+1/6=1/2）、
截断标记、输入校验（空输入 / float / 非法字符串 / 重复 ID / 负上限）、结果确定性。

