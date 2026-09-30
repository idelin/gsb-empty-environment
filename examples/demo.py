"""运行样例：对称破缺全流程演示。

用法：python3.11 examples/demo.py
"""
from __future__ import annotations

import os
import sys

sys.path.insert(0, os.path.dirname(os.path.dirname(os.path.abspath(__file__))))

from symbreak import (
    BudgetExceeded,
    Constraint,
    Permutation,
    Problem,
    Variable,
    check_solution,
    declare_exchangeable,
    find_recovery,
    group_closure,
    lex_leader_constraint,
    recover_solutions,
    solve_all,
    solve_one,
    verify_orbit_coverage,
    verify_permutation,
)


def banner(title: str) -> None:
    print("\n" + "=" * 66)
    print(title)
    print("=" * 66)


def build_symmetric_problem() -> Problem:
    """4 个变量，域 {0,1,2}，sum==4 且至多一个变量取 2（均为对称约束）。

    目标：最小化平方和（对称函数）。
    """
    names = ["x1", "x2", "x3", "x4"]
    variables = tuple(Variable(n, (0, 1, 2)) for n in names)
    constraints = (
        Constraint("sum==4", tuple(names), lambda a, b, c, d: a + b + c + d == 4),
        Constraint(
            "at-most-one-2",
            tuple(names),
            lambda a, b, c, d: sum(1 for v in (a, b, c, d) if v == 2) <= 1,
        ),
    )
    return Problem(
        variables=variables,
        constraints=constraints,
        objective=lambda a: sum(a[n] ** 2 for n in names),
        sense="min",
    )


def demo_exchangeable() -> None:
    banner("1. 可交换变量组：验证 -> 字典序链约束 -> 轨道覆盖验证 -> 恢复")
    problem = build_symmetric_problem()
    result = declare_exchangeable(problem, ["x1", "x2", "x3", "x4"])
    assert result.ok, result.reason
    print(f"可交换组验证通过，生成约束: {[c.name for c in result.constraints]}")
    print(f"群闭包大小 |S4| = {len(result.group)}")

    report = verify_orbit_coverage(problem, result.group)
    print(
        f"原始解 {report.total_solutions} 个，保留 {report.retained} 个，"
        f"删除 {report.deleted} 个，轨道数 {report.orbits}"
    )
    print(f"每轨道至少一解且被删解均可恢复: {report.ok}")

    retained = solve_all(problem, use_lex=True)
    print("保留的代表解:")
    for sol in retained:
        print(f"  {sol}  (独立检查器: {check_solution(problem, sol)})")

    all_solutions = solve_all(problem, use_lex=False)
    deleted = [s for s in all_solutions if s not in retained]
    sample = deleted[0]
    witness = find_recovery(result.group, sample, retained)
    print(f"被删解示例: {sample}")
    print(f"  恢复证人: 置换 {witness[0]} 作用于保留解 {witness[1]}")
    recovered = recover_solutions(result.group, retained)
    canon = lambda sols: sorted(tuple(sorted(s.items())) for s in sols)
    print(f"recover_solutions 恢复出 {len(recovered)} 个解，"
          f"与原始解集一致: {canon(recovered) == canon(all_solutions)}")

    best = min(retained, key=problem.objective)
    print(f"最优保留解 {best}，目标值 {problem.objective(best)}，"
          f"独立检查器复核: {check_solution(problem, best)}")


def demo_bad_permutations() -> None:
    banner("2. 错误置换：返回最小不保持约束的反例")
    variables = tuple(Variable(n, (0, 1, 2)) for n in ("x1", "x2"))
    problem = Problem(
        variables=variables,
        constraints=(Constraint("x1<x2", ("x1", "x2"), lambda a, b: a < b),),
    )
    swap = Permutation.transposition(problem.names, "x1", "x2")
    res = verify_permutation(problem, swap)
    print(f"约束 x1<x2 下交换 x1,x2 -> 验证通过? {res.ok}")
    print(f"  最小反例: {res.counterexample.assignment}  (约束 {res.counterexample.constraint})")
    print(f"  说明: {res.counterexample.detail}")

    # 目标不被保持
    problem2 = Problem(
        variables=variables,
        constraints=(Constraint("trivial", ("x1",), lambda a: True),),
        objective=lambda a: a["x1"],
        sense="min",
    )
    res2 = verify_permutation(problem2, swap)
    print(f"目标 f=x1 下交换 x1,x2 -> 验证通过? {res2.ok}")
    print(f"  最小反例: {res2.counterexample.assignment}  (kind={res2.counterexample.kind})")


def demo_rejections() -> None:
    banner("3. 拒绝非法对称声明：不同域 / 特殊角色 / 非双射")
    p_diff_domain = Problem(
        variables=(Variable("a", (0, 1)), Variable("b", (0, 1, 2))),
        constraints=(Constraint("t", ("a",), lambda x: True),),
    )
    r = declare_exchangeable(p_diff_domain, ["a", "b"])
    print(f"域不同的组 -> 接受? {r.ok}  原因: {r.reason}")

    p_special = Problem(
        variables=(
            Variable("boss", (0, 1), special=True),
            Variable("w1", (0, 1)),
            Variable("w2", (0, 1)),
        ),
        constraints=(Constraint("t", ("w1",), lambda x: True),),
    )
    r2 = declare_exchangeable(p_special, ["boss", "w1"])
    print(f"含特殊角色的组 -> 接受? {r2.ok}  原因: {r2.reason}")
    perm_moves_special = Permutation({"boss": "w1", "w1": "boss"}, p_special.names)
    v = verify_permutation(p_special, perm_moves_special)
    print(f"置换特殊角色的置换 -> 通过? {v.ok}  反例: {v.counterexample.detail}")

    try:
        Permutation({"x": "y", "z": "y"}, ["x", "y", "z"])
    except ValueError as exc:
        print(f"非双射映射 -> 构造即拒绝: {exc}")


def demo_unproven_and_budget() -> None:
    banner("4. 只使用已证对称 + 闭包预算")
    problem = build_symmetric_problem()
    bad = Permutation({"x1": "x2", "x2": "x1"}, problem.names)
    # 先制造一个该置换不保持目标的场景：改目标为 x1
    problem.objective = lambda a: a["x1"]
    try:
        lex_leader_constraint(problem, bad)
    except ValueError as exc:
        print(f"未证对称的置换 -> 拒绝生成约束:\n  {exc}")

    problem2 = build_symmetric_problem()
    gens = [
        Permutation.transposition(problem2.names, "x1", "x2"),
        Permutation.transposition(problem2.names, "x2", "x3"),
        Permutation.transposition(problem2.names, "x3", "x4"),
    ]
    try:
        group_closure(gens, problem2.names, budget=10)
    except BudgetExceeded as exc:
        print(f"预算 10 -> {exc}")
    full = group_closure(gens, problem2.names, budget=100)
    print(f"预算 100 -> 闭包构造成功，|S4| = {len(full)}")


def demo_undeclared_not_ordered() -> None:
    banner("5. 未声明对称的变量不被强行排序")
    variables = tuple(Variable(n, (0, 1, 2)) for n in ("x1", "x2", "y"))
    problem = Problem(
        variables=variables,
        constraints=(
            Constraint("sum==2", ("x1", "x2", "y"), lambda a, b, c: a + b + c == 2),
        ),
    )
    result = declare_exchangeable(problem, ["x1", "x2"])
    assert result.ok
    print(f"仅对 x1,x2 追加约束: {[c.name for c in problem.lex_constraints]}")
    sols = solve_all(problem, use_lex=True)
    y_unordered = [s for s in sols if s["y"] > s["x1"]]
    print(f"保留解 {len(sols)} 个；其中 y > x1 的解存在: {bool(y_unordered)}"
          "（y 未被排序）")
    print(f"  示例: {y_unordered[0] if y_unordered else None}")


def main() -> None:
    demo_exchangeable()
    demo_bad_permutations()
    demo_rejections()
    demo_unproven_and_budget()
    demo_undeclared_not_ordered()


if __name__ == "__main__":
    main()
