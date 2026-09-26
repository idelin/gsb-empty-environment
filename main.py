"""运行样例：python3.11 main.py"""
from fractions import Fraction

from scheduler import (
    BranchAndBoundSolver,
    Job,
    SearchBudget,
    SetupMatrix,
    Status,
)


def print_schedule(title, result):
    print(f"--- {title} ---")
    print(f"状态: {result.status.value}  (已证明最优: {result.proven_optimal})")
    print(f"总迟延(上界): {result.objective}   全局下界: {result.lower_bound}   "
          f"间隙: {result.gap}   展开节点: {result.nodes_explored}")
    print(f"{'任务':<6}{'类型':<6}{'切换':>4}{'空闲':>6}{'开工':>6}{'完工':>6}{'截止':>6}{'迟延':>6}")
    for e in result.schedule.entries:
        j = e.job
        print(f"{str(j.job_id):<6}{str(j.job_type):<6}{e.setup!s:>4}{e.idle!s:>6}"
              f"{e.start!s:>6}{e.completion!s:>6}{j.due!s:>6}{e.tardiness!s:>6}")
    print()


def demo_idle_benefit():
    """空闲可能优于立即执行：t=0 时只有 A 可加工，但空等 B 释放更优。"""
    setups = SetupMatrix.symmetric({("X", "Y"): 1})
    jobs = [
        Job("A", "X", release=0, processing=10, due=10),
        Job("B", "Y", release=3, processing=2, due=5),
    ]
    result = BranchAndBoundSolver(jobs, setups).solve()
    print_schedule("样例 1：空闲优于立即执行（最优次序先等 B）", result)


def demo_budget():
    """含整数与有理数数据的 8 任务实例：预算受限 vs 不限预算。"""
    setups = SetupMatrix.symmetric({("a", "b"): 2, ("a", "c"): 1, ("b", "c"): 3})
    jobs = [
        Job("J1", "a", release=0, processing=3, due=6),
        Job("J2", "b", release=1, processing=Fraction(5, 2), due=8),
        Job("J3", "a", release=2, processing=2, due=7),
        Job("J4", "c", release=0, processing=Fraction(3, 2), due=10),
        Job("J5", "b", release=4, processing=1, due=9),
        Job("J6", "c", release=3, processing=2, due=12),
        Job("J7", "a", release=5, processing=Fraction(1, 2), due=11),
        Job("J8", "b", release=2, processing=2, due=14),
    ]
    limited = BranchAndBoundSolver(jobs, setups, SearchBudget(max_nodes=5)).solve()
    print_schedule("样例 2a：搜索预算 max_nodes=5（预算未决，返回最佳可行解）", limited)

    full = BranchAndBoundSolver(jobs, setups).solve()
    print_schedule("样例 2b：不限预算（证明最优）", full)


if __name__ == "__main__":
    demo_idle_benefit()
    demo_budget()
