"""合成样例数据与演示运行。"""

from __future__ import annotations

from fractions import Fraction

from .model import Group, Instance, Job, Shift
from .solver import Result, Status, solve


def sample_instance() -> Instance:
    """两个停机组、三个班次的合成实例（含有理数分数）。"""
    shifts = (
        Shift("早班", 4),
        Shift("中班", 3),
        Shift("晚班", 4),
    )
    groups = (
        Group("泵组", 1),
        Group("阀组", 2),
    )
    jobs = (
        # 泵组：limit=1，容量紧张
        Job("P1", "泵组", 3, Fraction(9), Fraction(1)),
        Job("P2", "泵组", 5, Fraction(14), Fraction(2)),   # 必跨班次，计额外启动
        Job("P3", "泵组", 2, Fraction(4), Fraction(1)),
        # 阀组：limit=2，较宽松；含分数权重
        Job("V1", "阀组", 2, Fraction(7, 2), Fraction(1, 2)),
        Job("V2", "阀组", 3, Fraction(5), Fraction(1)),
        Job("V3", "阀组", 1, Fraction(3, 2), Fraction(1, 4)),
        Job("V4", "阀组", 4, Fraction(11, 2), Fraction(3)),
    )
    return Instance(shifts=shifts, groups=groups, jobs=jobs)


def long_job_instance() -> Instance:
    """演示中断恢复成本：长作业必须跨班次，净收益可能为负。"""
    shifts = (Shift("白班", 3), Shift("夜班", 4))
    groups = (Group("炉组", 1),)
    jobs = (
        Job("F1", "炉组", 5, Fraction(10), Fraction(3)),  # 跨 2 班：10 - 2*3 = 4
        Job("F2", "炉组", 2, Fraction(5), Fraction(1)),   # 单班：5 - 1 = 4
        Job("F3", "炉组", 4, Fraction(6), Fraction(3)),   # 跨 2 班：6 - 6 = 0，不选
    )
    return Instance(shifts=shifts, groups=groups, jobs=jobs)


def format_result(name: str, result: Result, instance: Instance) -> str:
    lines = [f"[{name}] 状态: {result.status.value} "
             f"(扩展节点 {result.nodes_expanded}"
             + (f"/{result.max_nodes}" if result.max_nodes is not None else "")
             + ")"]
    if result.value is None:
        lines.append("  无可报告解（预算未决）")
        return "\n".join(lines)
    lines.append(f"  净收益: {result.value} (={float(result.value):.4f})")
    shift_names = [s.name for s in instance.shifts]
    for a in result.selections:
        alloc = ", ".join(
            f"{shift_names[k]}+{x}" for k, x in enumerate(a.pieces) if x > 0
        )
        extra = f"，占用 {a.shifts_used} 班" if a.shifts_used > 1 else ""
        lines.append(f"  选中 {a.job_id}: {alloc}{extra}")
    if not result.selections:
        lines.append("  （空选择）")
    return "\n".join(lines)


def run_demo() -> str:
    out = []
    inst = sample_instance()

    out.append("=== 样例 1：预算不限（应证最优） ===")
    out.append(format_result("样例1", solve(inst), inst))

    out.append("")
    out.append("=== 样例 2：节点预算=3（可行但未证最优） ===")
    out.append(format_result("样例2", solve(inst, max_nodes=3), inst))

    out.append("")
    out.append("=== 样例 3：节点预算=0（预算未决） ===")
    r3 = solve(inst, max_nodes=0)
    assert r3.status is Status.BUDGET_UNDECIDED
    out.append(format_result("样例3", r3, inst))

    out.append("")
    out.append("=== 样例 4：中断恢复计额外启动成本 ===")
    inst4 = long_job_instance()
    out.append(format_result("样例4", solve(inst4), inst4))
    return "\n".join(out)
