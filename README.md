# 最小费用整数流（容量上下界 + 单位费用，含负费用）

纯 Python 3.11 标准库实现（`fractions` + `unittest`），**不调用任何求解器**。
有向边带容量下界 `lower`、上界 `upper` 和单位费用 `cost`；节点用平衡量
`balance` 描述供需：正数为供给、负数为需求、0 为中转。求满足所有节点平衡的
**最小费用整数流**，支持负费用边，并在不可行时给出可核验的证据。

## 文件

- `mincost_flow.py`：求解器（核心算法与数据结构）。
- `demo.py`：5 个运行样例。
- `test_mincost_flow.py`：21 个 `unittest` 测试。

## 算法

1. **总供需校验**：`Σ 供给 != Σ 需求` 时直接拒绝，证据是总量不等。
2. **下界预处理**：每条边先令流量 `x = lower`（强制占用），下界流按
   `bal[tail] -= lower`、`bal[head] += lower` 改写节点平衡；残量容量为
   `upper - lower`。
3. **可行性（超源/超汇 + Dinic 最大流）**：对仍需净流出 `b > 0` 的节点连
   超源边 `ss → v`（容量 `b`），对仍需净流入 `b < 0` 的节点连
   `v → tt`（容量 `-b`）。超源边全部饱和才有可行流。不可行时取最大流停止后
   的残量可达集 `S`（含 `ss`）作为最小割证据：
   `Σ_{v∈S} bal[v]  >  Σ_{tail∈S,head∉S} (upper − lower)`，
   并列出穿出 `S` 的饱和边与无法回退的穿入边。
4. **最小费用（消圈法）**：在干净的残量网络（正向弧容量 `upper − x`，
   反向弧容量 `x − lower`，反向弧费用取负）上，用 **Bellman-Ford**
   （全点距离初始化为 0，等价于虚设零源）找负费用环，沿环上最小残量容量
   有界增广，直至无负环（此时为最优）。所有容量有界，负环不可能无限增广；
   迭代上限内未消完则标记“可行但未证最优”。
5. **精确运算与整数性**：内部全程使用 `Fraction`。当全部下界/上界/平衡量为
   整数时，残量容量为整数，Dinic 与消圈增广量均为整数（整流定理），得到的
   一定是整数流；含有理数参数时产出精确分数流。

## 三个结论分开给

`SolveResult` 把三件事分别报告，互不混淆：

- `feasibility`：`FEASIBLE` / `INFEASIBLE`（后者附 `InfeasibleEvidence`）。
- `optimality`：`OPTIMAL`（残量网络无负环，已证明）/ `NOT_PROVEN`
  （有可行流但迭代上限内未证最优）/ `N/A`（不可行）。
- `budget`：调用方可给费用预算；`WITHIN_BUDGET`（已证最优且费用 ≤ 预算）/
  `OVER_BUDGET`（已证最优且费用 > 预算）/ `UNDECIDED`（未给预算，或未证最优——
  继续消圈只会降费用，故未证最优时不能断言超预算）。

## 用法

```python
from mincost_flow import MinCostFlow

mcf = MinCostFlow()
mcf.add_node("s", 3)      # 供给 3
mcf.add_node("t", -3)     # 需求 3
mcf.add_edge("s", "t", lower=0, upper=5, cost=2, label="direct")
result = mcf.solve(budget=None)
print(result.render())
```

也可用便捷入口 `solve_min_cost_flow(balances, edges)`，其中
`edges` 为 `(tail, head, lower, upper, cost)` 序列。整数与有理数
（`int` / `fractions.Fraction` / `"2/3"` 字符串）均可作为参数。

## 运行

```bash
python3.11 demo.py
python3.11 -m unittest -v test_mincost_flow
```

## 样例运行结果（Python 3.11.15）

```
========================================================================
样例 1：可行 + 可达负费用循环（消圈法求最优，20 -> 10）
------------------------------------------------------------------------
可行性   : FEASIBLE
最优性   : OPTIMAL
预算判定 : UNDECIDED（未提供预算）
总费用   : 10
整数流   : 是
边流量：
  su       s -> u: flow=0 in [0, 2], 单位费用 10, 小计 0
  sv       s -> v: flow=2 in [0, 2], 单位费用 0, 小计 0
  ut       u -> t: flow=2 in [0, 2], 单位费用 0, 小计 0
  vt       v -> t: flow=0 in [0, 2], 单位费用 10, 小计 0
  uv       u -> v: flow=0 in [0, 2], 单位费用 -6, 小计 0
  vu       v -> u: flow=2 in [0, 2], 单位费用 5, 小计 10
>> 断言通过：已证最优，最优费用 10，整数流，共消除负环 1 个
========================================================================
样例 2：可行性 / 最优性 / 预算判定三者分开（最优费用 = 10）
------------------------------------------------------------------------
宽松预算 12:
  可行性=FEASIBLE, 最优性=OPTIMAL, 预算=WITHIN_BUDGET, 费用=10
------------------------------------------------------------------------
过紧预算 8:
  可行性=FEASIBLE, 最优性=OPTIMAL, 预算=OVER_BUDGET, 费用=10
------------------------------------------------------------------------
不提供预算:
  可行性=FEASIBLE, 最优性=OPTIMAL, 预算=UNDECIDED, 费用=10
>> 断言通过：WITHIN_BUDGET / OVER_BUDGET / UNDECIDED 三种预算结论正确
========================================================================
样例 3：总供需不等（供给 4，需求 3），直接拒绝
------------------------------------------------------------------------
可行性   : INFEASIBLE
最优性   : N/A
预算判定 : UNDECIDED
不可行证据（supply_demand_mismatch）：总供给 4 与总需求 3 不相等，任何流都无法同时满足所有节点平衡
  总供给 = 4，总需求 = 3
>> 断言通过：不可行；最优性 N/A；预算未决；证据为供需总量不等
========================================================================
样例 4：供需相等但容量瓶颈（上界 2 < 需求 3）
------------------------------------------------------------------------
可行性   : INFEASIBLE
最优性   : N/A
预算判定 : UNDECIDED
不可行证据（cut）：超源最大流只能推送 2 / 3，尚缺 1；下界占用后可达集 S 内节点仍需净流出 3，而穿出 S 的边容量合计仅 2。S 内供给节点 ['s'] 无法把流量送出，S 外需求节点 ['t'] 无法得到满足
  最小割可达集 S = ['s']
  穿割残量容量合计 = 2 < 必须注入的量 = 3
    正向穿出: s -> t [cut-edge] 容量(上界-下界)=2（已到上界）
>> 断言通过：以最小割可达集与穿割边为不可行证据
========================================================================
样例 5：下界先占用流量并改写平衡 + 有理数单位费用（1/3）
------------------------------------------------------------------------
可行性   : FEASIBLE
最优性   : OPTIMAL
预算判定 : UNDECIDED（未提供预算）
总费用   : 2/3
整数流   : 否（含有理参数，得到精确分数流）
边流量：
  lo       s -> t: flow=2 in [1, 5], 单位费用 1/3, 小计 2/3
  hi       s -> t: flow=0 in [0, 5], 单位费用 2, 小计 0
>> 断言通过：下界占用后剩余 1 单位仍走最便宜边，费用 = 2/3
========================================================================
全部样例运行结束。
```

## 测试结果（Python 3.11.15）

```
test_lower_greater_than_upper (test_mincost_flow.TestBoundsViolation.test_lower_greater_than_upper) ... ok
test_negative_lower_bound_rejected (test_mincost_flow.TestBoundsViolation.test_negative_lower_bound_rejected) ... ok
test_budget_undecided_when_optimality_not_proven (test_mincost_flow.TestBudget.test_budget_undecided_when_optimality_not_proven) ... ok
test_within_over_and_undecided (test_mincost_flow.TestBudget.test_within_over_and_undecided) ... ok
test_capacity_bottleneck_evidence (test_mincost_flow.TestCutEvidence.test_capacity_bottleneck_evidence) ... ok
test_structural_infeasibility_disconnected_demand (test_mincost_flow.TestCutEvidence.test_structural_infeasibility_disconnected_demand) ... ok
test_Z_normalizes_int_string_fraction (test_mincost_flow.TestInputNormalization.test_Z_normalizes_int_string_fraction) ... ok
test_Z_rejects_bool_and_float (test_mincost_flow.TestInputNormalization.test_Z_rejects_bool_and_float) ... ok
test_lower_bound_creates_supply_at_head (test_mincost_flow.TestLowerBoundPreflow.test_lower_bound_creates_supply_at_head) ... ok
test_lower_bound_exceeding_supply_is_infeasible (test_mincost_flow.TestLowerBoundPreflow.test_lower_bound_exceeding_supply_is_infeasible) ... ok
test_lower_bound_forced_and_balance_shifted (test_mincost_flow.TestLowerBoundPreflow.test_lower_bound_forced_and_balance_shifted) ... ok
test_negative_cost_edge_but_no_negative_cycle (test_mincost_flow.TestNegativeCycles.test_negative_cost_edge_but_no_negative_cycle) ... ok
test_negative_cycle_canceled_to_optimum (test_mincost_flow.TestNegativeCycles.test_negative_cycle_canceled_to_optimum) ... ok
test_negative_cycle_is_bounded_by_capacity (test_mincost_flow.TestNegativeCycles.test_negative_cycle_is_bounded_by_capacity) ... ok
test_pure_circulation_negative_cycle (test_mincost_flow.TestNegativeCycles.test_pure_circulation_negative_cycle) ... ok
test_fractional_capacities_produce_fractional_flow (test_mincost_flow.TestRationalData.test_fractional_capacities_produce_fractional_flow) ... ok
test_fractional_costs_exact (test_mincost_flow.TestRationalData.test_fractional_costs_exact) ... ok
test_single_source_sink_picks_cheap_edge (test_mincost_flow.TestSimpleMinCost.test_single_source_sink_picks_cheap_edge) ... ok
test_zero_supply_zero_demand (test_mincost_flow.TestSimpleMinCost.test_zero_supply_zero_demand) ... ok
test_multiple_supplies_aggregate_check (test_mincost_flow.TestSupplyDemandMismatch.test_multiple_supplies_aggregate_check) ... ok
test_total_supply_not_equal_demand_rejected (test_mincost_flow.TestSupplyDemandMismatch.test_total_supply_not_equal_demand_rejected) ... ok

----------------------------------------------------------------------
Ran 21 tests in 0.004s

OK
```

此外还做过 400 个随机小网络与暴力枚举（枚举全部整数流）的交叉验证，
可行性判定与最优费用全部一致（校验脚本为一次性脚本，未包含在仓库中）。

## 边界与说明

- 下界必须满足 `0 <= lower <= upper`，否则以 `bounds_violation` 拒绝。
- 消圈法是多项式空间、指数时间的经典精确算法，适合合成的中小规模数据；
  可用 `MinCostFlow(max_cycle_iterations=...)` 控制上限。
- 预算是“费用预算”，只影响 `budget` 结论，不改变流本身的最优性。
