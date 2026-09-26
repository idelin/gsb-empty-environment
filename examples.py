"""Runnable examples for setsolver (Python 3.11, stdlib only)."""

from setsolver import Conflict, Solver, check_solution


def example_1_cardinality_conflict():
    print("=" * 64)
    print("例 1：必含 3 个元素但最大基数为 2 —— 立即冲突")
    print("=" * 64)
    s = Solver()
    s.add_var("X", universe={1, 2, 3, 4}, required={1, 2, 3}, max_card=2)
    try:
        s.propagate()
        print("意外：未检测到冲突")
    except Conflict as exc:
        print(f"检测到冲突: {exc}")
    print(f"solve() 返回: {s.solve()}")


def example_2_intersection_bounds():
    print("=" * 64)
    print("例 2：交集的下界与上界分别计算；可能成员 ≠ 必含成员")
    print("=" * 64)
    s = Solver()
    s.add_var("A", universe={1, 2, 3, 4}, required={1}, possible={1, 2, 3})
    s.add_var("B", universe={1, 2, 3, 4}, required={2}, possible={1, 2})
    s.add_var("C", universe={1, 2, 3, 4})
    s.add_intersection("C", "A", "B")
    s.propagate()
    A, B, C = s.vars["A"], s.vars["B"], s.vars["C"]
    print(f"A: lb={sorted(A.lb)}, ub={sorted(A.ub)}")
    print(f"B: lb={sorted(B.lb)}, ub={sorted(B.ub)}")
    print(f"C = A ∩ B 传播后: lb={sorted(C.lb)}, ub={sorted(C.ub)}")
    print("说明: lb(C)=lb(A)∩lb(B)=∅；元素 1、2 在两边都只是'可能'，")
    print("      因此它们在 C 中也只是可能成员，绝不能当作必含。")
    assert C.lb == set() and C.ub == {1, 2}


def example_3_union_subset_solve():
    print("=" * 64)
    print("例 3：并集 + 子集 + 基数约束，求解并用独立检查器验证")
    print("=" * 64)
    s = Solver()
    s.add_var("X", universe={1, 2, 3, 4}, possible={1, 3, 4})  # 2 被禁止
    s.add_var("Y", universe={1, 2, 3, 4}, max_card=2)
    s.add_var("Z", universe={1, 2, 3, 4}, required={1, 2}, max_card=3)
    s.add_union("Z", "X", "Y")     # Z = X ∪ Y
    s.add_subset("X", "Z")         # X ⊆ Z（冗余于并集，仅演示）
    s.propagate()
    Y = s.vars["Y"]
    print(f"传播后 Y: lb={sorted(Y.lb)}, ub={sorted(Y.ub)}")
    print("说明: 2 在 Z 中必含而在 X 中不可能 => 传播迫使 2 ∈ lb(Y)。")
    sol = s.solve()
    print(f"solve() 得到解: {sol}")
    print(f"独立检查器验证原约束: {check_solution(s, sol)}")
    bad = dict(sol, Y=frozenset({1, 2, 3}))  # 篡改：|Y|=3 超过 max_card=2
    print(f"篡改后的解 {bad} 被检查器接受? {check_solution(s, bad)}")


def example_4_intersection_conflict():
    print("=" * 64)
    print("例 4：交集下界传播引发冲突（C 必含 1 但 max_card(C)=0）")
    print("=" * 64)
    s = Solver()
    s.add_var("A", universe={1, 2}, required={1})
    s.add_var("B", universe={1, 2}, required={1})
    s.add_var("C", universe={1, 2}, max_card=0)
    s.add_intersection("C", "A", "B")
    try:
        s.propagate()
        print("意外：未检测到冲突")
    except Conflict as exc:
        print(f"检测到冲突: {exc}")
    print(f"solve() 返回: {s.solve()}")


if __name__ == "__main__":
    example_1_cardinality_conflict()
    example_2_intersection_bounds()
    example_3_union_subset_solve()
    example_4_intersection_conflict()
