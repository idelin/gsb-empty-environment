"""运行样例：共享风险组 + 成本/时延双预算的最大成功概率路径。

用法: python3.11 examples/run_sample.py
"""
import sys
from fractions import Fraction
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent.parent))

from riskpath import Graph, SearchState, Status, evaluate, solve


def fmt(prob: Fraction) -> str:
    return f"{prob} (~{float(prob):.4f})"


def build_graph() -> Graph:
    g = Graph()
    # 共享风险组：同组边由同一失效事件控制
    g.add_group("backbone", Fraction(9, 10))   # 骨干链路，多条边共享
    g.add_group("metro-a", Fraction(4, 5))
    g.add_group("metro-b", Fraction(9, 10))
    g.add_group("backup", Fraction(7, 10))
    g.add_group("decommissioned", 0)           # 必失效组，演示 p=0 边界
    # 上路：两条边共享 backbone -> 成功率 0.9（只计一次）
    g.add_edge("s", "a", 3, 4, "backbone")
    g.add_edge("a", "t", 3, 4, "backbone")
    # 中路：两个独立组 -> 0.8 * 0.9 = 0.72
    g.add_edge("s", "b", 2, 2, "metro-a")
    g.add_edge("b", "t", 2, 2, "metro-b")
    # 下路：单条慢速备份链路 -> 0.7
    g.add_edge("s", "t", 8, 9, "backup")
    # 含必失效边的支路，应被自动剪枝
    g.add_edge("b", "c", 1, 1, "decommissioned")
    g.add_edge("c", "t", 1, 1, "metro-b")
    return g


def main() -> None:
    g = build_graph()
    max_cost, max_delay = 10, 10

    print("== 一次性求解（成本预算 %s, 时延预算 %s）==" % (max_cost, max_delay))
    res = solve(g, "s", "t", max_cost, max_delay)
    print("状态:", res.status.value)
    print("Pareto 前沿 (成本, 时延, 成功率):")
    for label in sorted(res.frontier, key=lambda x: (x.cost, x.delay)):
        print("  cost=%-2s delay=%-2s success=%-12s path=%s"
              % (label.cost, label.delay, fmt(res.success_of(label)),
                 "->".join(res.path_nodes(label))))
    print("最优路径:", "->".join(res.path_nodes(res.best)),
          "成功率 =", fmt(res.best_success))
    print("（两条 backbone 边共享同一风险组，成功率是 0.9 而非 0.81）")

    print("\n== 小图全路径枚举对照 ==")
    enum = evaluate(g, "s", "t", max_cost, max_delay)
    print("枚举简单路径数:", len(enum.all_paths),
          "| 预算内可行:", len(enum.feasible))
    same = {x.signature() for x in res.frontier} == {
        (p.cost, p.delay, p.groups) for p in enum.frontier}
    print("搜索前沿与枚举前沿一致:", same,
          "| 最优成功率一致:", res.best_success == enum.best.success)

    print("\n== 可恢复搜索：预算未决 -> 继续 -> 最优 ==")
    state = SearchState(g, "s", "t", max_cost, max_delay)
    status = state.run(max_expansions=1)
    print("截断后状态:", status.value, "(扩展次数 =", state.expansions, ")")
    assert status is Status.UNDECIDED
    status = state.run()  # 从保存的堆与标签继续
    print("恢复后状态:", status.value, "(总扩展次数 =", state.expansions, ")")
    print("恢复后最优成功率:", fmt(g.success_of(state.best().groups)))

    print("\n== 无解（预算过紧）与未决的区分 ==")
    tight = solve(g, "s", "t", 1, 1)
    print("过紧预算状态:", tight.status.value, "| 前沿:", tight.frontier)


if __name__ == "__main__":
    main()
