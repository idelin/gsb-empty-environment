"""运行样例：python3.11 -m replica_planner

演示四种结果类别：最优（OPTIMAL）、可行未证优（FEASIBLE）、
预算未决（BUDGET_UNDECIDED）、不可行附容量诊断（INFEASIBLE）。
"""
from fractions import Fraction

from .migration import build_migration_plan, simulate_plan
from .model import Node, ObjectSpec
from .solver import solve


def print_result(title, result):
    print(f"=== {title} ===")
    print(f"状态: {result.status.value}")
    print(f"说明: {result.message}")
    if result.placement is not None:
        print(f"目标放置: {list(result.placement)}")
    if result.cost is not None:
        print(f"总传输成本: {result.cost} (下界 {result.lower_bound})")
    if result.budget is not None:
        verdict = {True: "预算内", False: "超预算", None: "未决"}[result.within_budget]
        print(f"预算: {result.budget} -> {verdict}")
    print(f"扩展节点数: {result.expansions}")
    if result.diagnostics is not None:
        print(result.diagnostics.render())
    print()


def main():
    # 场景 1：d1 已下线（容量 0），其副本需迁出；保留 a1、b1 成本为 0
    nodes = [
        Node("a1", "rackA", 1, 5),
        Node("a2", "rackA", 1, 7),
        Node("b1", "rackB", 1, Fraction(3, 2)),
        Node("b2", "rackB", 0, 2),
        Node("c1", "rackC", 1, 4),
        Node("c2", "rackC", 1, Fraction(9, 2)),
        Node("d1", "rackD", 0, 6),
    ]
    spec = ObjectSpec("obj-1", replicas=3, min_replicas=2, current=("a1", "b1", "d1"))
    result = solve(nodes, spec, budget=Fraction(9, 2))
    print_result("场景 1: 最优解（d1 下线需迁移，预算 9/2）", result)
    plan = build_migration_plan(spec.current, result.placement, spec.min_replicas)
    counts = simulate_plan(plan, len(spec.current))
    for op, count in zip(plan, counts):
        print(f"  {op.action:6s} {op.node_id}  -> 副本数 {count} (最低要求 {spec.min_replicas})")
    print()

    # 场景 2：扩展预算只允许找到可行解，最优性未证明
    spec2 = ObjectSpec("obj-2", replicas=2, min_replicas=2, current=())
    nodes2 = [
        Node("x1", "rackX", 1, 4),
        Node("y1", "rackY", 1, 5),
        Node("z1", "rackZ", 1, 1),
    ]
    result2 = solve(nodes2, spec2, max_expansions=2)
    print_result("场景 2: 可行解（搜索预算 2 次扩展，最优性未证明）", result2)

    # 场景 3：扩展预算过小，尚无可行解，预算未决
    result3 = solve(nodes2, spec2, budget=3, max_expansions=1)
    print_result("场景 3: 预算未决（搜索预算 1 次扩展，预算 3）", result3)

    # 场景 4：需要 4 个机架但只有 3 个有容量，输出缺容量诊断
    spec4 = ObjectSpec("obj-4", replicas=4, min_replicas=3, current=())
    nodes4 = nodes2 + [Node("w1", "rackW", 0, 1)]
    result4 = solve(nodes4, spec4)
    print_result("场景 4: 不可行（机架数不足，附容量诊断）", result4)


if __name__ == "__main__":
    main()
