# 有限马尔可夫链吸收分析（精确有理数 + 经验模拟）

Python 3.11，仅标准库（`fractions`、`random`、`unittest`、`dataclasses`）。

## 功能

- **结构识别**：`Chain.closed_classes()` 识别闭类，`absorbing_states()` 识别吸收态
  （自环概率 1），`transient_states()` 识别暂态。
- **精确吸收概率**：`absorption_probabilities(chain, target)` 用 `Fraction` 高斯消元
  精确求解首步方程；不可达目标的状态概率为 0；自环概率 1 的非目标态永不吸收。
  `probability_sum_witness` 给出各闭类吸收概率之和（有限链恒为 1，满足 ≤ 1）。
- **期望吸收步数**：`expected_hitting_times(chain, targets)` 对无法到达目标的
  状态返回 `math.inf`（永不吸收），目标态为 0。
- **条件期望时间**：`conditional_expected_time(chain, target)` 返回给定被吸收进
  目标闭类条件下的期望步数；目标命中概率为 0 的状态返回 `None`（未定义，
  绝不输出 0）。
- **残差证据**：`absorption_residuals` / `hitting_time_residuals` 逐状态给出线性
  方程残差，精确解下全部为 0，可复核。
- **经验估计**：`estimate_absorption(chain, start, rng, n, seed=...)` 的随机源由
  调用方注入（`random.Random`），结果记录 `seed` 与 `n_samples`，并以
  `kind="empirical"` 与精确结论区分。
- **部分观测/分组压缩**：`check_strong_lumpability` 验证强可聚合条件；不满足时
  返回精确见证（同组两状态向另一组转移概率不同的 `Fraction` 速率），
  `lumped_chain` 拒绝盲目压缩（抛 `ValueError`）。
- **行修订增量重算**：`revise_row(chain, row, delta)` 对一行施加有理数增量，
  仅重算受影响（在旧/新链中可达被修订行的）状态与可达闭类，未受影响区域
  用旧链子链的精确边界值；输出行和、概率界、线性方程残差证据，并与全量
  重新分解逐状态对照（`max_abs_diff` 精确为 0）。

## 运行样例

```
$ python3 demo.py
== structure (exact) ==
closed classes: [[2], [3], [4]]
absorbing states: [2, 3, 4]
transient states: [0, 1]

== absorption probabilities into target {2} (exact) ==
  P(hit 2 | start 0) = 1/2
  P(hit 2 | start 1) = 1/2
  P(hit 2 | start 2) = 1
  P(hit 2 | start 3) = 0
  P(hit 2 | start 4) = 0
probability sum over closed classes from 0: {(2,): '1/2', (3,): '1/3', (4,): '1/6'} total = 1 (must be <= 1; exactly 1 here)

== expected steps to absorption (exact) ==
  E[T | start 0] = 11/3
  E[T | start 1] = 10/3
  E[T | start 2] = 0
  E[T | start 3] = 0
  E[T | start 4] = 0

== expected steps to hit target {2} only (exact) ==
  E[T_2 | start 0] = 11/3
  E[T_2 | start 1] = 10/3
  E[T_2 | start 2] = 0
  E[T_2 | start 3] = inf
  E[T_2 | start 4] = inf  <- never absorbed (self-loop-1 trap)

== conditional E[T | absorbed in {2}] (exact) ==
  start 0: 11/3
  start 1: 10/3
  start 2: 0
  start 3: undefined (hit probability 0)
  start 4: undefined (hit probability 0)

== residuals of the linear equations (exact, must be 0) ==
  absorption: {0: Fraction(0, 1), 1: Fraction(0, 1), 2: Fraction(0, 1), 3: Fraction(0, 1), 4: Fraction(0, 1)}
  hitting time: {0: Fraction(0, 1), 1: Fraction(0, 1)}

== simulation (empirical, seed recorded) ==
  kind=empirical seed=42 n_samples=20000
  frequencies: {2: 0.4967, 3: 0.3332, 4: 0.1701}
  mean steps: 3.6749
  exact for comparison: P(hit 2) = 0.5  E[T] = inf

== partial observation / lumping ==
  groups [{0, 1}, {2}, {3}, {4}] lumpable? False
  exact witness: states 0 and 1 (both in group 0) move to group 0 with different exact rates 3/4 != 2/3
  groups [{0}, {1}, {2}, {3, 4}] lumpable? True
  lumped matrix:
    ['1/2', '1/4', '1/8', '1/8']
    ['1/3', '1/3', '1/6', '1/6']
    ['0', '0', '1', '0']
    ['0', '0', '0', '1']

== row revision (incremental vs full re-solve) ==
  row 0: old sum=1, delta sum=0, new sum=1
  probability bounds: [0, 1/2] ok=True
  affected states: [0, 1, 2, 3, 4]
  affected closed classes: [[2], [3], [4]]
  P(hit [2]) incremental: {0: '9/14', 1: '4/7', 2: '1', 3: '0', 4: '0'}
  P(hit [3]) incremental: {0: '2/7', 1: '1/7', 2: '0', 3: '1', 4: '0'}
  P(hit [4]) incremental: {0: '1/14', 1: '2/7', 2: '0', 3: '0', 4: '1'}
  max |incremental - full| over all states: 0
  residuals for class [2]: {0: '0', 1: '0', 2: '0', 3: '0', 4: '0'}
  residuals for class [3]: {0: '0', 1: '0', 2: '0', 3: '0', 4: '0'}
  residuals for class [4]: {0: '0', 1: '0', 2: '0', 3: '0', 4: '0'}
```

## 测试结果

```
$ python3 -m unittest discover -s tests -v
test_invalid_matrices_rejected ... ok
test_periodic_closed_class ... ok
test_absorption_probabilities_exact ... ok
test_conditional_expectation_undefined_not_zero ... ok
test_conditional_expected_time ... ok
test_expected_hitting_times ... ok
test_probabilities_over_absorbing_states_sum_to_one ... ok
test_residuals_are_exactly_zero ... ok
test_self_loop_trap_never_absorbs_into_target ... ok
test_target_must_be_closed_class ... ok
test_unreachable_target_gives_infinite_expectation ... ok
test_blind_compression_refused ... ok
test_invalid_partition_gives_exact_witness ... ok
test_non_partition_rejected ... ok
test_valid_partition_compresses ... ok
test_incremental_matches_full_resolve ... ok
test_invalid_revision_rejected ... ok
test_unaffected_region_uses_boundary_values ... ok
test_reproducible_with_same_seed ... ok
test_seed_and_sample_size_recorded_and_labeled ... ok
test_self_loop_state_absorbs_immediately ... ok
----------------------------------------------------------------------
Ran 22 tests in 0.042s

OK
```

## 文件结构

- `markov/chain.py` — 转移矩阵校验、可达性、闭类/吸收态识别
- `markov/exact.py` — Fraction 精确求解：吸收概率、期望步数、条件期望、残差
- `markov/lumping.py` — 强可聚合验证（含精确见证）与分组压缩
- `markov/simulate.py` — 调用方注入随机源的蒙特卡洛估计（记录 seed/样本量）
- `markov/revision.py` — 行修订增量重算与全量对照
- `demo.py` — 端到端样例
- `tests/test_markov.py` — 22 个 unittest 用例
