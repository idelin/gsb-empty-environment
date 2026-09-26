# 多目标任务方案组合的 Pareto 枚举筛选

Python 3.11，仅标准库（`fractions` / `itertools` / `dataclasses` / `unittest`），
不调用任何求解器。约束数据为合成整数或有理数（`int` / `Fraction` / 分数字符串），
全程精确计算，拒绝 `float`。

## 模型

- 每个任务 `Task` 提供若干候选方案 `Plan`，方案带三维最小化目标：成本 `cost`、时延 `latency`、能耗 `energy`。
- 组合 `Combination` 从每个任务各选一个方案，目标值为各方案之和，`combo_id` 按枚举顺序从 0 递增。
- 组合受资源预算 `Budget` 约束（任一维可单独设为 `None` 表示不限）；超预算即不可行。

## 规则

- **支配判定**：`a` 支配 `b` 当且仅当所有维不劣且至少一维严格更优；
  一维更好、另一维更差的两个组合互不支配，都保留。
- **等值代表**：目标向量完全相同的组合只保留 `combo_id` 最小者。
- **结果截断**：`max_results` 限制返回的非支配代表数量，超出时按
  `(cost, latency, energy, combo_id)` 排序截取，并置 `truncated=True`、`omitted` 为省略数。
- **三组分离**：`SelectionResult` 中 `optimal`（非支配代表）、`feasible`（全部可行组合）、
  `over_budget`（超预算组合）分开存放；`optimal` 与 `feasible` 中每个组合都独立满足预算。

## 文件

- `pareto_select.py` — 核心模块，直接运行即执行内置合成数据示例。
- `test_pareto_select.py` — 18 个 unittest 用例。

## 运行样例

```
$ python3.11 pareto_select.py
任务数: 3，组合总数: 18
预算: cost<=14, latency<=16, energy<=14
可行组合: 8，超预算组合: 10

Pareto 非支配代表（optimal）:
  id= 8 T1B+T2B+T3A      cost= 11 latency= 15 energy=27/2
  id= 3 T1A+T2B+T3B      cost= 11 latency= 16 energy=19/2
  id= 9 T1B+T2B+T3B      cost= 13 latency= 13 energy=23/2
截断: True，因上限省略: 1

超预算组合（over_budget，前 5 个）:
  id= 2 T1A+T2B+T3A      cost=  9 latency= 18 energy=23/2
  id= 5 T1A+T2C+T3B      cost= 16 latency= 11 energy= 10
  id= 6 T1B+T2A+T3A      cost= 14 latency= 12 energy= 16
  id= 7 T1B+T2A+T3B      cost= 16 latency= 10 energy= 14
  id=10 T1B+T2C+T3A      cost= 16 latency= 10 energy= 14
```

示例中 `max_results=3`，完整 Pareto 前沿有 4 个代表，故明确截断并报告省略 1 个。

## 测试结果

```
$ python3.11 -m unittest test_pareto_select -v
test_identical_vectors_do_not_dominate ... ok
test_strict_dominance ... ok
test_tradeoff_dims_cannot_dominate_each_other ... ok
test_weak_dominance_with_one_equal_dim ... ok
test_cartesian_product_and_sums ... ok
test_empty_tasks_yield_nothing ... ok
test_fractional_objectives_are_exact ... ok
test_dominated_removed_tradeoff_kept ... ok
test_identical_vectors_keep_smallest_id ... ok
test_all_returned_plans_independently_feasible ... ok
test_float_objectives_rejected ... ok
test_groups_are_partitioned ... ok
test_invalid_max_results_rejected ... ok
test_no_budget_means_everything_feasible ... ok
test_optimal_is_subset_of_feasible_and_nondominated ... ok
test_partial_budget_dimension ... ok
test_task_validation ... ok
test_truncation_is_explicit ... ok

----------------------------------------------------------------------
Ran 18 tests in 0.004s

OK
```
