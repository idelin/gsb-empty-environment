"""symbreak 的 unittest 测试套件。运行: python3.11 -m unittest discover -s tests -v"""
from __future__ import annotations

import os
import sys
import unittest

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
    orbit,
    recover_solutions,
    solve_all,
    solve_one,
    verify_orbit_coverage,
    verify_permutation,
)


def make_symmetric_problem(with_objective: bool = True) -> Problem:
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
    objective = (lambda a: sum(a[n] ** 2 for n in names)) if with_objective else None
    return Problem(variables=variables, constraints=constraints, objective=objective)


class TestPermutation(unittest.TestCase):
    def test_non_bijection_rejected(self):
        with self.assertRaises(ValueError):
            Permutation({"x": "y", "z": "y"}, ["x", "y", "z"])

    def test_unknown_variable_rejected(self):
        with self.assertRaises(ValueError):
            Permutation({"ghost": "x"}, ["x", "y"])

    def test_compose_inverse_apply(self):
        names = ["a", "b", "c"]
        p = Permutation({"a": "b", "b": "c", "c": "a"}, names)
        q = p.inverse()
        self.assertEqual(p.compose(q), Permutation.identity(names))
        a = {"a": 1, "b": 2, "c": 3}
        self.assertEqual(p.apply(a), {"b": 1, "c": 2, "a": 3})
        self.assertEqual(q.apply(p.apply(a)), a)


class TestVerifyPermutation(unittest.TestCase):
    def test_valid_automorphism_accepted(self):
        problem = make_symmetric_problem()
        swap = Permutation.transposition(problem.names, "x1", "x3")
        self.assertTrue(verify_permutation(problem, swap).ok)

    def test_wrong_permutation_minimal_counterexample(self):
        variables = tuple(Variable(n, (0, 1, 2)) for n in ("x1", "x2"))
        problem = Problem(
            variables=variables,
            constraints=(Constraint("x1<x2", ("x1", "x2"), lambda a, b: a < b),),
        )
        swap = Permutation.transposition(problem.names, "x1", "x2")
        res = verify_permutation(problem, swap)
        self.assertFalse(res.ok)
        cex = res.counterexample
        self.assertEqual(cex.kind, "constraint")
        self.assertEqual(cex.constraint, "x1<x2")
        self.assertEqual(cex.assignment, {"x1": 0, "x2": 1})
        # 最小性：按 (非默认取值个数, 域下标元组) 序，它应是最小的反例
        c = problem.constraints[0]
        def order_key(a):
            return ((a["x1"] != 0) + (a["x2"] != 0), (a["x1"], a["x2"]))
        differing = []
        for v1 in (0, 1, 2):
            for v2 in (0, 1, 2):
                a = {"x1": v1, "x2": v2}
                if c.satisfied(a) != c.satisfied(swap.apply(a)):
                    differing.append(a)
        self.assertTrue(differing)
        self.assertEqual(min(differing, key=order_key), cex.assignment)

    def test_special_role_not_swappable(self):
        problem = Problem(
            variables=(
                Variable("boss", (0, 1), special=True),
                Variable("w1", (0, 1)),
            ),
            constraints=(Constraint("t", ("w1",), lambda x: True),),
        )
        perm = Permutation({"boss": "w1", "w1": "boss"}, problem.names)
        res = verify_permutation(problem, perm)
        self.assertFalse(res.ok)
        self.assertEqual(res.counterexample.kind, "special")

    def test_different_domains_rejected(self):
        problem = Problem(
            variables=(Variable("a", (0, 1)), Variable("b", (0, 1, 2))),
            constraints=(Constraint("t", ("a",), lambda x: True),),
        )
        perm = Permutation({"a": "b", "b": "a"}, problem.names)
        res = verify_permutation(problem, perm)
        self.assertFalse(res.ok)
        self.assertEqual(res.counterexample.kind, "domain")

    def test_objective_not_preserved_rejected(self):
        problem = Problem(
            variables=(Variable("x1", (0, 1)), Variable("x2", (0, 1))),
            constraints=(Constraint("t", ("x1",), lambda x: True),),
            objective=lambda a: a["x1"],
        )
        swap = Permutation.transposition(problem.names, "x1", "x2")
        res = verify_permutation(problem, swap)
        self.assertFalse(res.ok)
        self.assertEqual(res.counterexample.kind, "objective")
        self.assertEqual(res.counterexample.assignment, {"x1": 0, "x2": 1})

    def test_constraint_distinguishing_variables_rejected(self):
        # 约束只涉及 x1：交换 x1,x2 后约束语义改变 -> 拒绝
        problem = Problem(
            variables=(Variable("x1", (0, 1)), Variable("x2", (0, 1))),
            constraints=(Constraint("x1==1", ("x1",), lambda a: a == 1),),
        )
        swap = Permutation.transposition(problem.names, "x1", "x2")
        res = verify_permutation(problem, swap)
        self.assertFalse(res.ok)
        self.assertEqual(res.counterexample.kind, "constraint")

    def test_verify_budget_exceeded(self):
        problem = make_symmetric_problem()
        swap = Permutation.transposition(problem.names, "x1", "x2")
        with self.assertRaises(BudgetExceeded):
            verify_permutation(problem, swap, budget=1)


class TestExchangeableGroup(unittest.TestCase):
    def test_chain_constraints_added_only_after_verification(self):
        problem = make_symmetric_problem()
        result = declare_exchangeable(problem, ["x1", "x2", "x3", "x4"])
        self.assertTrue(result.ok)
        self.assertEqual(
            [c.name for c in problem.lex_constraints],
            ["lex:x1<=x2", "lex:x2<=x3", "lex:x3<=x4"],
        )
        self.assertEqual(len(result.group), 24)  # |S4|

    def test_group_with_different_domains_rejected(self):
        problem = Problem(
            variables=(Variable("a", (0, 1)), Variable("b", (0, 1, 2))),
            constraints=(Constraint("t", ("a",), lambda x: True),),
        )
        result = declare_exchangeable(problem, ["a", "b"])
        self.assertFalse(result.ok)
        self.assertEqual(problem.lex_constraints, [])  # 不追加任何约束

    def test_group_with_special_role_rejected(self):
        problem = Problem(
            variables=(
                Variable("boss", (0, 1), special=True),
                Variable("w1", (0, 1)),
            ),
            constraints=(Constraint("t", ("w1",), lambda x: True),),
        )
        result = declare_exchangeable(problem, ["boss", "w1"])
        self.assertFalse(result.ok)
        self.assertEqual(problem.lex_constraints, [])

    def test_group_violating_constraint_rejected_with_counterexample(self):
        variables = tuple(Variable(n, (0, 1, 2)) for n in ("x1", "x2"))
        problem = Problem(
            variables=variables,
            constraints=(Constraint("x1<x2", ("x1", "x2"), lambda a, b: a < b),),
        )
        result = declare_exchangeable(problem, ["x1", "x2"])
        self.assertFalse(result.ok)
        self.assertIsNotNone(result.counterexample)
        self.assertEqual(result.counterexample.assignment, {"x1": 0, "x2": 1})
        self.assertEqual(problem.lex_constraints, [])

    def test_lex_leader_requires_proven_symmetry(self):
        problem = make_symmetric_problem()
        problem.objective = lambda a: a["x1"]  # 非对称目标
        bad = Permutation.transposition(problem.names, "x1", "x2")
        with self.assertRaises(ValueError):
            lex_leader_constraint(problem, bad)

    def test_lex_leader_constraint_for_proven_permutation(self):
        problem = make_symmetric_problem()
        c = lex_leader_constraint(
            problem, Permutation.transposition(problem.names, "x2", "x4")
        )
        self.assertEqual(c.scope, problem.names)
        # x <=lex p(x)：x1..x4 = (0,2,0,1) 置换 x2,x4 后为 (0,1,0,2)，前者更大
        self.assertFalse(c.satisfied({"x1": 0, "x2": 2, "x3": 0, "x4": 1}))
        self.assertTrue(c.satisfied({"x1": 0, "x2": 1, "x3": 0, "x4": 2}))


class TestGroupClosure(unittest.TestCase):
    def test_closure_size_and_budget(self):
        problem = make_symmetric_problem()
        gens = [
            Permutation.transposition(problem.names, a, b)
            for a, b in (("x1", "x2"), ("x2", "x3"), ("x3", "x4"))
        ]
        with self.assertRaises(BudgetExceeded):
            group_closure(gens, problem.names, budget=10)
        closure = group_closure(gens, problem.names, budget=100)
        self.assertEqual(len(closure), 24)


class TestSolverAndChecker(unittest.TestCase):
    def test_independent_checker(self):
        problem = make_symmetric_problem()
        sol = solve_one(problem, use_lex=False)
        self.assertIsNotNone(sol)
        self.assertTrue(check_solution(problem, sol))
        bad = dict(sol)
        bad["x1"] = (bad["x1"] + 1) % 3
        if check_solution(problem, bad):  # 改动后仍满足则再改一个变量
            bad["x2"] = (bad["x2"] + 1) % 3
        self.assertFalse(check_solution(problem, bad))
        self.assertFalse(check_solution(problem, {"x1": 0}))  # 不完整
        out_of_domain = dict(sol)
        out_of_domain["x1"] = 99
        self.assertFalse(check_solution(problem, out_of_domain))

    def test_no_sat_smt_libraries_used(self):
        import symbreak.solver as solver_module

        self.assertNotIn("z3", solver_module.__dict__)
        for name in ("z3", "pysat", "ortools"):
            self.assertNotIn(name, sys.modules)


class TestOrbitCoverage(unittest.TestCase):
    def test_every_orbit_keeps_at_least_one_solution(self):
        problem = make_symmetric_problem()
        result = declare_exchangeable(problem, ["x1", "x2", "x3", "x4"])
        self.assertTrue(result.ok)
        report = verify_orbit_coverage(problem, result.group)
        self.assertTrue(report.ok)
        self.assertEqual(report.uncovered, [])
        self.assertEqual(report.unrecoverable, [])
        self.assertEqual(report.invalid_retained, [])
        # 全对称群 + 链式字典序：每轨道恰好保留一个代表
        self.assertEqual(report.retained, report.orbits)
        self.assertEqual(report.total_solutions, 13)
        self.assertEqual(report.retained, 2)

    def test_deleted_solutions_recoverable(self):
        problem = make_symmetric_problem()
        result = declare_exchangeable(problem, ["x1", "x2", "x3", "x4"])
        all_solutions = solve_all(problem, use_lex=False)
        retained = solve_all(problem, use_lex=True)
        key = lambda s: tuple(sorted(s.items()))
        retained_keys = {key(r) for r in retained}
        for sol in all_solutions:
            if key(sol) not in retained_keys:
                witness = find_recovery(result.group, sol, retained)
                self.assertIsNotNone(witness)
                g, r = witness
                self.assertEqual(key(g.apply(r)), key(sol))
        # recover_solutions 恢复完整解集
        recovered = recover_solutions(result.group, retained)
        self.assertEqual({key(s) for s in recovered}, {key(s) for s in all_solutions})

    def test_orbit_of_solution(self):
        problem = make_symmetric_problem()
        result = declare_exchangeable(problem, ["x1", "x2", "x3", "x4"])
        orb = orbit(result.group, {"x1": 0, "x2": 1, "x3": 1, "x4": 2})
        self.assertEqual(len(orb), 12)  # 4! / 2!


class TestUndeclaredVariables(unittest.TestCase):
    def test_undeclared_variables_not_ordered(self):
        variables = tuple(Variable(n, (0, 1, 2)) for n in ("x1", "x2", "y"))
        problem = Problem(
            variables=variables,
            constraints=(
                Constraint("sum==2", ("x1", "x2", "y"), lambda a, b, c: a + b + c == 2),
            ),
        )
        result = declare_exchangeable(problem, ["x1", "x2"])
        self.assertTrue(result.ok)
        # 字典序约束只涉及已声明变量
        for c in problem.lex_constraints:
            self.assertNotIn("y", c.scope)
        sols = solve_all(problem, use_lex=True)
        # y 不受任何排序限制：存在 y 大于/小于 x1 的保留解
        self.assertTrue(any(s["y"] > s["x1"] for s in sols))
        self.assertTrue(any(s["y"] < s["x1"] for s in sols))
        # 原始问题的每个解仍可通过交换 x1,x2 恢复
        all_solutions = solve_all(problem, use_lex=False)
        recovered = recover_solutions(result.group, sols)
        key = lambda s: tuple(sorted(s.items()))
        self.assertEqual({key(s) for s in recovered}, {key(s) for s in all_solutions})


class TestSmallDomainExhaustion(unittest.TestCase):
    def test_exhaustive_check_loses_no_class(self):
        # 小域穷举：3 变量域 {0,1}，约束 sum>=1，验证不丢等价类
        names = ["a", "b", "c"]
        problem = Problem(
            variables=tuple(Variable(n, (0, 1)) for n in names),
            constraints=(
                Constraint("sum>=1", tuple(names), lambda a, b, c: a + b + c >= 1),
            ),
        )
        result = declare_exchangeable(problem, names)
        self.assertTrue(result.ok)
        report = verify_orbit_coverage(problem, result.group)
        self.assertTrue(report.ok)
        self.assertEqual(report.total_solutions, 7)  # 2^3 - 1
        self.assertEqual(report.retained, 3)  # 重量 1、2、3 各一个代表
        self.assertEqual(report.orbits, 3)


if __name__ == "__main__":
    unittest.main()
