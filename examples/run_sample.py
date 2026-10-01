"""运行样例。

覆盖：贪心启发式、全子集枚举精确参考、可恢复分支定界（含剪枝理由）、
启发式与精确解的实测差距、动态删集合后的见证重验。

用法：python3 examples/run_sample.py
"""
import json
import sys
from fractions import Fraction
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parents[1]))

from budget_coverage import (
    BranchBoundSolver,
    Problem,
    exact_solve,
    greedy_cover,
    make_witness,
    optimality_gap,
    validate_witness,
)


def build_main_problem() -> Problem:
    return Problem.create(
        element_weights=[5, 4, 4, 3, 3, 2, 2, 1],
        set_ids=list("ABCDEFG"),
        set_costs=[0, 3, 4, 2, Fraction(1, 2), 2, 2],
        set_elements=[{0, 1}, {1, 2, 3}, {2, 3, 4, 5}, {5, 6}, {6, 7},
                      set(), {3, 4}],
        budget=7,
        dependencies={"G": ["B"]},      # 选 G 必须选 B
        mutex_groups=[["C", "D"]],      # C 与 D 互斥
        min_coverage={2: 2},            # 元素 2 至少被覆盖 2 次
    )


def build_gap_problem() -> Problem:
    return Problem.create(
        element_weights=[6, 5, 5],
        set_ids=["S1", "S2", "S3"],
        set_costs=[3, 3, 4],
        set_elements=[{0}, {1}, {1, 2}],
        budget=6,
    )


def main():
    print("=" * 72)
    print("1) 主实例：贪心启发式（不声称最优）")
    problem = build_main_problem()
    g = greedy_cover(problem)
    print(f"   状态={g.status.value}  信息={g.message}")

    print("=" * 72)
    print("2) 主实例：全子集枚举精确参考")
    exact = exact_solve(problem)
    print(f"   状态={exact.status.value}  选择={exact.selected}  "
          f"收益={exact.profit}  枚举节点={exact.nodes_explored}")

    print("=" * 72)
    print("3) 可恢复分支定界：先用 5 个节点的搜索预算")
    solver = BranchBoundSolver(problem)
    partial = solver.search(max_nodes=5)
    print(f"   状态={partial.status.value}  信息={partial.message}")
    snap = solver.snapshot()
    blob = json.dumps(snap, ensure_ascii=False)
    print(f"   快照(JSON)={len(blob)} 字节，已探索节点={solver.nodes_explored}")
    print("   从快照恢复并继续搜索 …")
    solver2 = BranchBoundSolver.restore(problem, json.loads(blob))
    final = solver2.search()
    print(f"   状态={final.status.value}  选择={final.selected}  "
          f"收益={final.profit}  上界={final.upper_bound}  "
          f"总节点={final.nodes_explored}")
    print("   剪枝记录（每条都带界限理由，仅显示前 6 条）：")
    for pr in final.prunes[:6]:
        path = "".join(map(str, pr.path)) or "根"
        print(f"     路径{path:<9} 原因={pr.reason:<12} {pr.detail}")
    print(f"   … 共 {len(final.prunes)} 次剪枝")

    print("=" * 72)
    print("4) 启发式 vs 精确：实测差距（不构成近似保证）")
    gp = build_gap_problem()
    gg = greedy_cover(gp)
    ge = exact_solve(gp)
    abs_gap, rel_gap = optimality_gap(gg.profit, ge.profit)
    print(f"   贪心: {gg.selected} 收益 {gg.profit}；"
          f"精确: {ge.selected} 收益 {ge.profit}")
    print(f"   实测绝对差距 {abs_gap}，相对差距 {rel_gap} "
          f"（约 {float(rel_gap):.1%}）——仅为本次实测，本库不提供近似保证")

    print("=" * 72)
    print("5) 动态删集合后的见证重验")
    witness = make_witness(problem, final, "branch_bound")
    print(f"   删除前见证校验: ok={validate_witness(problem, witness).ok}")
    problem.delete_set("F")  # F 是空集，不在最优解中
    rep = validate_witness(problem, witness)
    print(f"   删除空集 F 后旧见证: ok={rep.ok} stale={rep.stale}")
    for msg in rep.problems:
        print(f"     - {msg}")
    exact2 = exact_solve(problem)
    print(f"   重新求解: 状态={exact2.status.value} 收益={exact2.profit}"
          "（空集 F 本就不影响收益）")
    problem.delete_set("C")  # C 在最优解中
    rep2 = validate_witness(problem, witness)
    print(f"   再删除 C 后旧见证: ok={rep2.ok}")
    for msg in rep2.problems:
        print(f"     - {msg}")
    exact3 = exact_solve(problem)
    print(f"   重新求解: 状态={exact3.status.value}"
          "（元素 2 无法再被覆盖 2 次 -> 不可行）")


if __name__ == "__main__":
    main()
