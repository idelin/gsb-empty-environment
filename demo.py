"""最小费用整数流运行样例。

运行：python3.11 demo.py
"""

from fractions import Fraction

from mincost_flow import (
    BudgetVerdict,
    Feasibility,
    MinCostFlow,
    Optimality,
)


def build_demo_network() -> MinCostFlow:
    """样例网络：s 供给 2，t 需求 2，u/v 为中转节点。

    边（下界, 上界, 单位费用）：
      su: s -> u [0, 2,  10]
      sv: s -> v [0, 2,   0]
      ut: u -> t [0, 2,   0]
      vt: v -> t [0, 2,  10]
      uv: u -> v [0, 2,  -6]   负费用边
      vu: v -> u [0, 2,   5]

    uv(-6) 与 vu(+5) 构成负费用环 u->v->u（费用 -1），消圈法会沿
    残量容量把可达负环逐个消掉，把费用从最大流给出的初始可行流（20）
    降到最优值 10。
    """
    mcf = MinCostFlow()
    mcf.add_node("s", 2)
    mcf.add_node("u", 0)
    mcf.add_node("v", 0)
    mcf.add_node("t", -2)
    mcf.add_edge("s", "u", 0, 2, 10, "su")
    mcf.add_edge("s", "v", 0, 2, 0, "sv")
    mcf.add_edge("u", "t", 0, 2, 0, "ut")
    mcf.add_edge("v", "t", 0, 2, 10, "vt")
    mcf.add_edge("u", "v", 0, 2, -6, "uv")
    mcf.add_edge("v", "u", 0, 2, 5, "vu")
    return mcf


def case_feasible_with_negative_cycle() -> None:
    print("=" * 72)
    print("样例 1：可行 + 可达负费用循环（消圈法求最优，20 -> 10）")
    print("-" * 72)
    result = build_demo_network().solve()
    print(result.render())
    assert result.feasibility is Feasibility.FEASIBLE
    assert result.optimality is Optimality.OPTIMAL
    assert result.cost == Fraction(10)
    assert result.negative_cycles_canceled >= 1
    assert result.is_integral()
    print(">> 断言通过：已证最优，最优费用 10，整数流，"
          f"共消除负环 {result.negative_cycles_canceled} 个")


def case_budget() -> None:
    print("=" * 72)
    print("样例 2：可行性 / 最优性 / 预算判定三者分开（最优费用 = 10）")
    for limit, note in [(12, "宽松预算 12"), (8, "过紧预算 8"), (None, "不提供预算")]:
        print("-" * 72)
        print(f"{note}:")
        result = build_demo_network().solve(budget=limit)
        print(f"  可行性={result.feasibility.value}, "
              f"最优性={result.optimality.value}, "
              f"预算={result.budget.value}, 费用={result.cost}")
        if limit == 12:
            assert result.budget is BudgetVerdict.WITHIN_BUDGET
        elif limit == 8:
            assert result.budget is BudgetVerdict.OVER_BUDGET
        else:
            assert result.budget is BudgetVerdict.UNDECIDED
    print(">> 断言通过：WITHIN_BUDGET / OVER_BUDGET / UNDECIDED 三种预算结论正确")


def case_supply_demand_mismatch() -> None:
    mcf = MinCostFlow()
    mcf.add_node("s", 4)
    mcf.add_node("t", -3)  # 少 1 个单位需求
    mcf.add_edge("s", "t", 0, 10, 1, "e")

    print("=" * 72)
    print("样例 3：总供需不等（供给 4，需求 3），直接拒绝")
    print("-" * 72)
    result = mcf.solve()
    print(result.render())
    assert result.feasibility is Feasibility.INFEASIBLE
    assert result.optimality is Optimality.NOT_APPLICABLE
    assert result.budget is BudgetVerdict.UNDECIDED
    assert result.evidence is not None
    assert result.evidence.reason == "supply_demand_mismatch"
    print(">> 断言通过：不可行；最优性 N/A；预算未决；证据为供需总量不等")


def case_capacity_bottleneck() -> None:
    mcf = MinCostFlow()
    mcf.add_node("s", 3)
    mcf.add_node("t", -3)
    mcf.add_edge("s", "t", 0, 2, 1, "cut-edge")  # 唯一通道上界只有 2

    print("=" * 72)
    print("样例 4：供需相等但容量瓶颈（上界 2 < 需求 3）")
    print("-" * 72)
    result = mcf.solve()
    print(result.render())
    assert result.feasibility is Feasibility.INFEASIBLE
    assert result.evidence is not None
    assert result.evidence.reason == "cut"
    assert result.evidence.blocked_forward[0][0] == "cut-edge"
    print(">> 断言通过：以最小割可达集与穿割边为不可行证据")


def case_lower_bound() -> None:
    mcf = MinCostFlow()
    mcf.add_node("s", 2)
    mcf.add_node("t", -2)
    mcf.add_edge("s", "t", 1, 5, Fraction(1, 3), "lo")  # 下界 1 先占用
    mcf.add_edge("s", "t", 0, 5, 2, "hi")

    print("=" * 72)
    print("样例 5：下界先占用流量并改写平衡 + 有理数单位费用（1/3）")
    print("-" * 72)
    result = mcf.solve(require_integral=False)
    print(result.render())
    assert result.feasibility is Feasibility.FEASIBLE
    flows = {f.label: f.flow for f in result.flows}
    assert flows["lo"] == 2  # 便宜边（1/3）承担全部 2 个单位
    assert flows["hi"] == 0
    assert result.cost == Fraction(2, 3)
    print(">> 断言通过：下界占用后剩余 1 单位仍走最便宜边，费用 = 2/3")


def main() -> None:
    case_feasible_with_negative_cycle()
    case_budget()
    case_supply_demand_mismatch()
    case_capacity_bottleneck()
    case_lower_bound()
    print("=" * 72)
    print("全部样例运行结束。")


if __name__ == "__main__":
    main()
