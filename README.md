# batchsched — 相容类型分批的精确批调度

按相容类型（family）把计算作业分批：每批有固定启动成本与总资源上限，
作业不可拆分，目标是最小化总成本并满足全部截止约束。**不调用任何求解器**，
仅使用 Python 3.11 标准库，所有算术用 `fractions.Fraction` 精确进行
（合成数据可以是整数或 `p/q` 有理数）。

## 问题模型

- 每个作业：相容类型 `family`、资源量 `size`、处理时间 `time`、截止 `deadline`、成本 `cost`。
- 每批只能包含同一 family 的作业（**不相容作业绝不为了省成本而同批**）；
  批内 `size` 之和不超过 `capacity`；作业**不可拆分**（单作业超容量直接拒绝实例）。
- 批顺序执行，批处理时间 = 批内作业最大 `time`，批完成时批内所有作业同时完成，
  每个作业的完成时间不得超过其 `deadline`。
- **启动成本按批计（每批一次，非按作业）**：
  `总成本 = setup_cost × 批数 + Σ 作业成本`。

## 算法

- 精确算法：作业子集上的动态规划，每个子集维护 `(批数, 已用时间)` 的 Pareto 前沿；
  配合 EDD 贪心上界与按 family 的容量下界剪枝。空闲时间无收益，故只需枚举批序列。
- 节点预算 `--node-limit` 限制搜索量；结果状态严格区分：
  - `optimal`：搜索完成，返回成本已证明最小；
  - `feasible`：节点预算耗尽，返回当前最好可行解（未证明最优）；
  - `infeasible`：已证明不存在满足截止约束的调度；
  - `undecided`：预算耗尽且尚未找到任何可行解；
  - `rejected`：实例非法（如单作业超容量）。
- 预算判定 `--budget K` 单独输出三态：
  - `budget_met`：找到成本 ≤ K 的具体调度（证书）；
  - `budget_exceeded`：完整搜索后证明所有可行调度都超预算；
  - `budget_undecided`：预算耗尽，既未找到也未证否。

## 用法

```bash
python3.11 -m batchsched INSTANCE.json [--budget K] [--node-limit N] [--json]
python3.11 -m unittest discover -s tests -v
```

实例为 JSON：数值可以是整数、小数或 `"p/q"` 字符串。见 `examples/`。

## 运行样例（实际输出）

最优（`examples/optimal_small.json`，5 个作业、2 个 family）：

```
$ python3.11 -m batchsched examples/optimal_small.json
status: optimal
total_cost: 16
batches: 3
  batch 1: jobs=[j1, j3] family=A size=7 start=0 end=2
  batch 2: jobs=[j4, j5] family=B size=10 start=2 end=6
  batch 3: jobs=[j2] family=A size=6 start=6 end=9
nodes_explored: 0
note: proven optimal
```

（A 族 3 个作业总量 13 > 10 至少 2 批，B 族 1 批，下界即 3 批，贪心解即最优，
故 0 个 DP 节点完成证明。总成本 = 3 × 4 + 4 = 16。）

不可行（两作业时间均为 3，截止 3 与 4，任何顺序都有一方超时）：

```
$ python3.11 -m batchsched examples/infeasible_deadline.json
status: infeasible
nodes_explored: 2
note: proven: no batch schedule meets all deadlines
```

单作业超容量，实例被拒绝：

```
$ python3.11 -m batchsched examples/rejected_oversize.json
status: rejected
reason: job(s) exceed batch capacity and cannot be split: big
```

有理数精确计算（`3/4 + 3/4 = 3/2` 恰好满批，总成本为分数 `23/4`）：

```
$ python3.11 -m batchsched examples/rational.json
status: optimal
total_cost: 23/4
batches: 2
  batch 1: jobs=[r1, r2] family=A size=3/2 start=0 end=1
  batch 2: jobs=[r3] family=A size=1/2 start=1 end=5/4
```

预算判定三态（同一实例，最优成本为 16）：

```
$ python3.11 -m batchsched examples/optimal_small.json --budget 16
status: budget_met
total_cost: 16
...

$ python3.11 -m batchsched examples/optimal_small.json --budget 15
status: budget_exceeded
nodes_explored: 47
note: proven: every feasible schedule exceeds the budget

$ python3.11 -m batchsched examples/optimal_small.json --budget 15 --node-limit 0
status: budget_undecided
nodes_explored: 1
note: node limit exhausted: no schedule within budget found, and budget excess not proven
```

## 测试结果

`python3.11 -m unittest discover -s tests -v`（Python 3.11.15，17 个用例全部通过）：

```
test_budget_exceeded_proven ... ok
test_budget_met ... ok
test_budget_undecided_when_search_truncated ... ok
test_job_costs_alone_exceed_budget ... ok
test_deadline_can_force_a_split ... ok
test_empty_instance ... ok
test_incompatible_jobs_never_share_a_batch ... ok
test_jobs_are_indivisible ... ok
test_setup_cost_charged_per_batch_not_per_job ... ok
test_matches_brute_force_on_random_instances ... ok
test_node_limit_downgrades_to_feasible ... ok
test_infeasible_deadline ... ok
test_optimal_small ... ok
test_rejected_oversize ... ok
test_invalid_field_rejected ... ok
test_oversize_job_rejected ... ok
test_rational_numbers_parsed_exactly ... ok
----------------------------------------------------------------------
Ran 17 tests in 0.140s

OK
```

要点覆盖：启动成本按批非按作业；不相容作业即使容量允许也不同批；
作业不可拆；截止约束可强制拆批；单作业超容量拒绝；有理数精确解析；
随机小实例与独立的「排列 + 切分」暴力枚举逐一核对最优批数；
节点预算耗尽时降级为 `feasible` / `budget_undecided`，绝不谎报最优。

## 复杂度与适用范围

精确 DP 对作业数 n 是指数级（状态为子集，转移为可行批），适合合成小实例
（经验上 n ≤ 15 左右、family 较多时更大）；更大实例会按 `--node-limit`
体面地降级为 `feasible` / `undecided`，而不是给出未证明的“最优”。

## 文件结构

- `batchsched/model.py` — 实例解析（整数/有理数）、校验、超容量拒绝
- `batchsched/solver.py` — 精确子集 DP、贪心界、预算判定、调度校验
- `batchsched/cli.py` / `__main__.py` — 命令行入口
- `examples/` — 最优、不可行、拒绝、有理数四个样例实例
- `tests/test_solver.py` — 17 个 unittest 用例
