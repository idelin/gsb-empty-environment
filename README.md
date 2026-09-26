# riskpath — 共享风险组感知的双预算最可靠简单路径

Python 3.11，仅标准库（`math` / `fractions` / `heapq` / `dataclasses` /
`unittest`），不调用任何求解器。约束数据（成本、时延、预算）只接受整数或
`fractions.Fraction`，预算比较是精确有理数运算。

## 模型

- 有向图的每条边属于一个**风险组**，每个风险组有成功率 `p ∈ [0,1]`。
- 不同风险组的失效**相互独立**；同一风险组的边由**同一个失效事件**控制
  （要么一起存活，要么一起失效）。
- 一条简单路径（节点不重复）的成功率是它经过的**不同风险组**概率之积：
  - 两条独立边：`0.9 × 0.9 = 0.81`，绝不是相加（`0.9 + 0.9 = 1.8` 没有
    物理意义，也不是截断为 1.0）；
  - 同组两条边：只乘一次 `0.9`，结果为 `0.9`。
- 双预算：路径成本 `≤ C` 且路径时延 `≤ D`，二者都是精确 `Fraction`。

### 概率的稳定表示与边界

概率乘积以**对数空间之和**保存（`Label.log_success`），避免大量连乘下溢。
边界直接处理，不调用会抛异常的 `log(0)`：

- `p = 0 → log = -inf`，路径成功率 0.0；
- `p = 1 → log = 0.0`，重复经过 p=1 的组/环不会“提高”成功率；
- 最终成功率由 `exp(log)` 还原，并夹到 `[0,1]`。

## 算法

标签纠正（label-correcting）最佳优先搜索，标签按 `log_success` 从大到小
出队（`riskpath/search.py`）：

- 扩展只进入未访问节点，保证**简单路径**；新风险组才把该组的 `log p`
  累加，已在路径上的共享组**不重复计数**。
- 超过任一预算的标签进入 `state.deferred` 暂存（预算放宽后可复活），
  而不是直接丢弃。
- `mode="optimal"`：第一个出队的、非被支配的终点标签即**已证明最优**；
  `mode="frontier"`：搜索穷尽后返回终点标签的
  `(成本, 时延, 成功率)` Pareto 前沿。

### 精确支配关系

标签 `a` 支配同节点标签 `b` 当且仅当：

```
cost(a) ≤ cost(b)
delay(a) ≤ delay(b)
groups(a) ⊆ groups(b)      # 风险组集合为子集 ⇒ 成功率不更低
visited(a) ⊆ visited(b)    # 已访问节点为子集 ⇒ 不额外堵死任何简单续路
```

两个子集条件缺一不可：组集合不是子集就无法比较成功率；已访问节点不是子集
时，`a` 可能堵死了 `b` 的某条简单续路（资源受限最短路的“元素性”要点）。
前沿支配则只比较 `(cost, delay, log_success)` 三维。

### 最优性与终止条件

1. 图为有限有向图，只生成简单路径 ⇒ 标签数有限 ⇒ 搜索必然终止；
   在 DAG 上不存在环，简单路径约束自然满足。
2. 所有 `p ≤ 1`，扩展路径只会保持或降低 `log_success`（复用同组时保持，
   进入新组时下降），所以按 `log_success` 最佳优先出队时，第一个可行的
   终点标签成功率不低于任何尚未出队的前缀及其完整路径，即全局最优。
3. 支配剪枝安全：`a` 支配 `b` 时，`b` 的每条简单续路都对应一条 `a` 的
   简单续路，且成本、时延、风险组集合均不更差，因此最优值与 Pareto 前沿
   都不会被剪掉。

### 三种结论严格分开

- `Status.OPTIMAL`：已找到可行最优（队列顺序给出证明）；
- `Status.INFEASIBLE`：队列穷尽且无可行路径，与“没算完”无关；
- `Status.UNDECIDED`：`max_expansions` 搜索工作量预算用尽，尚无结论。

### 可恢复搜索状态

`SearchResult.state`（`SearchState`）保留优先队列、各节点非支配标签集和
`deferred` 标签：

- 提高 `max_expansions` 后传入 `state=` 可原地继续，已完成扩展不重算；
- 放宽预算后恢复会自动复活变为可行的 `deferred` 标签。

## 运行样例

```
 $ python3.11 -m riskpath
=== Demo 1: dual budgets, independent + shared risk groups ===
status: optimal, label expansions: 15
Pareto frontier (cost, delay, success):
  cost=2 delay=6 success=0.8000  s -> t
  cost=4 delay=4 success=0.9801  s -> c -> t
optimal within budgets: success=0.9801  s -> c -> t

=== Demo 2: multiplication, not addition ===
two independent 0.9 edges -> 0.8100 (0.9 * 0.9 = 0.81)

=== Demo 3: shared risk group counted once ===
two edges in one shared group G -> 0.9000 (common failure event, counted once)

=== Demo 4: undecided -> resumable -> optimal; relaxed-budget resume ===
cost budget 1: infeasible
resume with relaxed cost budget 2: optimal, success=0.9000
expansion cap 1: undecided
resumed to completion: optimal, frontier size 2
```

Demo 1 中 `s -> a -> t` 两条边同属 `weather` 组（成功率 0.9，按一次计），
其 `(4, 5, 0.9)` 被 `s -> c -> t` 的 `(4, 4, 0.9801)` 前沿支配，故不在
前沿中。

## 测试结果

```
 $ python3.11 -m unittest discover -v
... (省略逐行名称)
test_two_independent_edges_multiply_to_0_81 ............... ok
test_probabilities_are_never_added ........................ ok
test_shared_group_event_counted_once ...................... ok
test_zero_probability_edge_gives_zero_success_and_neg_inf  ok
test_one_probability_edges_give_unit_success .............. ok
test_invalid_probability_rejected ......................... ok
test_shared_group_with_conflicting_probability_rejected ... ok
test_dominance_requires_group_and_visited_subsets ......... ok
test_extra_group_blocks_dominance ......................... ok
test_extra_visited_node_blocks_dominance .................. ok
test_infeasible_distinct_from_undecided ................... ok
test_delay_budget_alone_makes_infeasible .................. ok
test_undecided_then_resume_to_optimal ..................... ok
test_resume_after_budget_relaxation ....................... ok
test_source_equals_target_is_unit_success ................. ok
test_zero_cost_cycle_terminates_and_path_is_simple ........ ok
test_cycle_cannot_increase_probability .................... ok
test_rational_budget_is_exact ............................. ok
test_pareto_frontier_keeps_incomparable_tradeoffs ......... ok
test_search_matches_full_enumeration_on_random_graphs ..... ok

Ran 20 tests in 0.009s

OK
```

其中 `test_search_matches_full_enumeration_on_random_graphs` 对 120 个
固定随机种子的小图，用 DFS 全简单路径枚举（`riskpath/enumerate.py`，
独立实现、不做支配剪枝）作为对照，逐一比较：

- 搜索前沿三元组集合与枚举前沿完全一致；
- 最优 `log_success` 一致；
- 无解时搜索返回 `INFEASIBLE`、前沿为空。

## 文件

- `riskpath/model.py` — 图、边、风险组、`Fraction` 约束数据、组对数概率；
- `riskpath/search.py` — 标签、精确支配、最佳优先搜索、前沿、可恢复状态；
- `riskpath/enumerate.py` — 全简单路径枚举（测试对照 oracle）；
- `riskpath/__main__.py` — 运行演示；
- `tests/test_riskpath.py` — 20 个 unittest 用例。
