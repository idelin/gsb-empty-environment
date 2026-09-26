"""运行样例：最优匹配 + 预算判定、必选冲突预诊断、无满匹配时的部分匹配与缺额。

运行：python3.11 demo.py
"""

from fractions import Fraction

from matching import (
    BUDGET_EXCEEDED,
    BUDGET_UNDECIDED,
    BUDGET_WITHIN,
    MandatoryConflictError,
    solve,
)

BUDGET_TEXT = {
    BUDGET_WITHIN: "可行（最优成本 <= 预算）",
    BUDGET_EXCEEDED: "不可行（最优成本 > 预算）",
    BUDGET_UNDECIDED: "未决（未提供预算，不作判定）",
}


def show(title, result):
    print("=" * 64)
    print(title)
    print("-" * 64)
    pairs = ", ".join(f"{p}->{r}" for p, r in result.matching.items())
    print(f"匹配 ({result.matched_count} 对): {pairs or '(空)'}")
    print(f"必选边        : {dict(result.mandatory_matching) or '无'}")
    print(f"总成本        : {result.total_cost}")
    print(f"满匹配规模    : {result.expected_count}，缺额: {result.deficit}"
          f"{'（满匹配）' if result.is_perfect else '（部分匹配）'}")
    print(f"未匹配的人    : {result.unmatched_people or '无'}")
    print(f"未匹配的资源  : {result.unmatched_resources or '无'}")
    print(f"预算          : {result.budget} -> {BUDGET_TEXT[result.budget_status]}")
    print()


def main():
    # ---- 样例 1：整数成本，含禁止边、必选边，分预算可行 / 超支两种判定 ----
    people = ["alice", "bob", "carol", "dave"]
    resources = ["x", "y", "z", "w"]
    edges = [
        ("alice", "x", 2), ("alice", "y", 5), ("alice", "z", 1), ("alice", "w", 9),
        ("bob",   "x", 6), ("bob",   "y", 1), ("bob",   "z", 3), ("bob",   "w", 4),
        ("carol", "x", 4), ("carol", "y", 2), ("carol", "z", 8), ("carol", "w", 1),
        ("dave",  "x", 3), ("dave",  "y", 7), ("dave",  "z", 2), ("dave",  "w", 5),
    ]
    forbidden = {("alice", "x")}       # 禁止边，即便便宜也不能用
    mandatory = {("bob", "y")}         # 必选边，必须固定

    result = solve(people, resources, edges, mandatory=mandatory,
                   forbidden=forbidden, budget=8)
    show("样例 1：最大匹配 + 最小成本（预算=8）", result)

    result = solve(people, resources, edges, mandatory=mandatory,
                   forbidden=forbidden, budget=4)
    show("样例 1b：同一问题，预算=4（低于最优成本）", result)

    result = solve(people, resources, edges, mandatory=mandatory,
                   forbidden=forbidden)
    show("样例 1c：同一问题，不给预算", result)

    # ---- 样例 2：必选边冲突——共享资源 + 共享人，预诊断后直接拒绝 ----
    print("=" * 64)
    print("样例 2：必选边冲突预诊断（不静默丢弃任何一条）")
    print("-" * 64)
    bad_mandatory = {("alice", "x"), ("bob", "x"), ("alice", "z")}
    try:
        solve(people, resources, edges, mandatory=bad_mandatory)
    except MandatoryConflictError as exc:
        for d in exc.diagnostics:
            print(f"[{d.kind}] {d.message}")
    print()

    # 必选边同时是禁止边，也会被诊断出来
    print("-" * 64)
    try:
        solve(people, resources, edges, mandatory={("alice", "x")},
              forbidden=forbidden)
    except MandatoryConflictError as exc:
        for d in exc.diagnostics:
            print(f"[{d.kind}] {d.message}")
    print()

    # ---- 样例 3：人多资源少 + 有理数成本，不存在满匹配，返回最大部分匹配与缺额 ----
    people3 = ["p1", "p2", "p3", "p4"]
    resources3 = ["r1", "r2"]
    edges3 = [
        ("p1", "r1", Fraction(1, 3)), ("p1", "r2", Fraction(5, 2)),
        ("p2", "r1", Fraction(1, 6)),                              # p2 只能匹配 r1
        ("p3", "r2", Fraction(2, 3)),
        # p4 没有任何边
    ]
    result = solve(people3, resources3, edges3, budget=Fraction(1))
    show("样例 3：4 人 / 2 资源（成本为有理数，预算=1）", result)

    # ---- 样例 4：两侧等规模但不存在满匹配（p3 完全无边），返回最大部分匹配与缺额 ----
    people4 = ["a", "b", "c"]
    resources4 = ["m", "n", "q"]
    edges4 = [
        ("a", "m", 1), ("a", "n", 10),
        ("b", "m", 2), ("b", "n", 3),
        # c 没有任何允许边；q 也没有任何人连接
    ]
    result = solve(people4, resources4, edges4)
    show("样例 4：3 人 / 3 资源但结构上无法满匹配", result)


if __name__ == "__main__":
    main()
