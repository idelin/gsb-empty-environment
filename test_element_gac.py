"""element_gac 的单元测试。

重点：index 自引用（别名）、重复元素变量、越界剔除与回溯撤回、
GAC 与暴力枚举投影的一致性、独立检查器验证。
"""

import unittest

from element_gac import (
    CSP,
    ElementConstraint,
    NotEqualConstraint,
    Variable,
    brute_force_solutions,
    check_element,
    check_solution,
    solve,
)


class TestOutOfBoundsAndEmpty(unittest.TestCase):

    def test_out_of_bounds_index_pruned_with_reason(self):
        csp = CSP()
        I = csp.add_variable(Variable("I", {-1, 0, 1, 5}))
        V = csp.add_variable(Variable("V", {10, 20}))
        csp.add_constraint(ElementConstraint(I, [10, 20], V))
        self.assertTrue(csp.propagate())
        self.assertEqual(I.domain, {0, 1})
        self.assertEqual(V.domain, {10, 20})
        reasons = {r.value: r.reason for r in csp.explain("I")}
        self.assertIn("越界", reasons[-1])
        self.assertIn("越界", reasons[5])
        self.assertIn("0..1", reasons[5])

    def test_empty_array_unsatisfiable(self):
        csp = CSP()
        I = csp.add_variable(Variable("I", {0, 1}))
        V = csp.add_variable(Variable("V", {1}))
        csp.add_constraint(ElementConstraint(I, [], V))
        self.assertFalse(csp.propagate())
        self.assertEqual(I.domain, set())
        self.assertTrue(any("数组为空" in r.reason for r in csp.explain("I")))

    def test_empty_array_solve_returns_none(self):
        csp = CSP()
        I = csp.add_variable(Variable("I", {0}))
        csp.add_variable(Variable("V", {1}))
        csp.add_constraint(ElementConstraint(I, [], Variable("V2", {1})))
        self.assertIsNone(solve(csp))


class TestPropagationDirection(unittest.TestCase):

    def test_unique_index_bidirectional(self):
        csp = CSP()
        I = csp.add_variable(Variable("I", {1}))
        A = csp.add_variable(Variable("A", {1, 2, 3}))
        B = csp.add_variable(Variable("B", {1, 2, 3}))
        V = csp.add_variable(Variable("V", {2, 3, 4}))
        csp.add_constraint(ElementConstraint(I, [A, B], V))
        self.assertTrue(csp.propagate())
        self.assertEqual(B.domain, {2, 3})      # value -> 被选元素
        self.assertEqual(V.domain, {2, 3})      # 被选元素 -> value
        self.assertEqual(A.domain, {1, 2, 3})   # 未被指到的元素不受影响

    def test_multiple_candidates_do_not_force_all_elements_equal(self):
        csp = CSP()
        I = csp.add_variable(Variable("I", {0, 1}))
        A = csp.add_variable(Variable("A", {1}))
        B = csp.add_variable(Variable("B", {2}))
        V = csp.add_variable(Variable("V", {1, 2}))
        csp.add_constraint(ElementConstraint(I, [A, B], V))
        self.assertTrue(csp.propagate())
        # index 有两个候选时，绝不能把 A、B 都强制等于 value
        self.assertEqual(A.domain, {1})
        self.assertEqual(B.domain, {2})
        self.assertEqual(V.domain, {1, 2})
        self.assertEqual(I.domain, {0, 1})
        self.assertEqual(csp.explain(), [])

    def test_constants_in_array(self):
        csp = CSP()
        I = csp.add_variable(Variable("I", {0, 1, 2, 3}))
        V = csp.add_variable(Variable("V", {5, 20, 30, 40}))
        csp.add_constraint(ElementConstraint(I, [10, 20, 30], V))
        self.assertTrue(csp.propagate())
        # I=0 被剔除：array[0]=10 不在 V 的域中；I=3 越界
        self.assertEqual(I.domain, {1, 2})
        self.assertEqual(V.domain, {20, 30})
        v_reasons = {r.value: r.reason for r in csp.explain("V")}
        self.assertIn("无支持", v_reasons[5])
        self.assertIn("无支持", v_reasons[40])


class TestAliasing(unittest.TestCase):

    def test_index_self_reference_alias(self):
        # array[0] 与 index 是同一个变量：J=0 时 array[0]=J=0
        csp = CSP()
        J = csp.add_variable(Variable("J", {0, 1, 2}))
        W = csp.add_variable(Variable("W", {0, 5, 9}))
        con = csp.add_constraint(ElementConstraint(J, [J, 9], W))
        self.assertEqual(con.scope, [J, W])  # 别名只出现一次
        self.assertTrue(csp.propagate())
        self.assertEqual(J.domain, {0, 1})   # J=2 越界被剔除
        self.assertEqual(W.domain, {0, 9})   # W=5 无支持
        sol = solve(csp)
        self.assertIsNotNone(sol)
        self.assertTrue(check_solution(csp.constraints, sol))
        all_sol = brute_force_solutions(con and csp.constraints,
                                        {"J": {0, 1, 2}, "W": {0, 5, 9}})
        self.assertEqual(all_sol, [{"J": 0, "W": 0}, {"J": 1, "W": 9}])

    def test_index_self_reference_single_cell(self):
        # array = [J]，J 既是下标又是唯一元素：只有 J=0, W=0 可行
        csp = CSP()
        J = csp.add_variable(Variable("J", {0, 1}))
        W = csp.add_variable(Variable("W", {0, 1}))
        csp.add_constraint(ElementConstraint(J, [J], W))
        self.assertTrue(csp.propagate())
        self.assertEqual(J.domain, {0})
        self.assertEqual(W.domain, {0})

    def test_duplicate_element_variables(self):
        # 数组两个位置是同一变量：V=2 迫使 A=2，无论 index 取哪个候选
        csp = CSP()
        I = csp.add_variable(Variable("I", {0, 1}))
        A = csp.add_variable(Variable("A", {1, 2}))
        V = csp.add_variable(Variable("V", {2}))
        csp.add_constraint(ElementConstraint(I, [A, A], V))
        self.assertTrue(csp.propagate())
        self.assertEqual(A.domain, {2})
        self.assertEqual(I.domain, {0, 1})

    def test_duplicate_element_variables_unsat(self):
        csp = CSP()
        I = csp.add_variable(Variable("I", {0, 1}))
        A = csp.add_variable(Variable("A", {1, 2}))
        V = csp.add_variable(Variable("V", {3}))
        csp.add_constraint(ElementConstraint(I, [A, A], V))
        self.assertFalse(csp.propagate())


class TestBacktrackingAndUndo(unittest.TestCase):

    def test_mark_undo_restores_domains(self):
        csp = CSP()
        I = csp.add_variable(Variable("I", {0, 1}))
        csp.add_variable(Variable("A", {1}))
        csp.add_variable(Variable("B", {2}))
        V = csp.add_variable(Variable("V", {1, 2}))
        A = csp.variables[1]
        B = csp.variables[2]
        csp.add_constraint(ElementConstraint(I, [A, B], V))
        self.assertTrue(csp.propagate())
        snapshot = {v.name: set(v.domain) for v in csp.variables}
        m = csp.mark()
        csp.assign(I, 0)  # 决策 I=0 -> V 必须为 A=1
        self.assertTrue(csp.propagate())
        self.assertEqual(V.domain, {1})
        csp.undo(m)
        self.assertEqual({v.name: set(v.domain) for v in csp.variables},
                         snapshot)

    def test_backtracking_search_with_out_of_bounds_branch(self):
        # I=0 分支导致 V==A 与 V!=A 冲突而失败，撤回后 I=1 成功
        csp = CSP()
        I = csp.add_variable(Variable("I", {0, 1}))
        A = csp.add_variable(Variable("A", {1, 2}))
        B = csp.add_variable(Variable("B", {1, 2}))
        V = csp.add_variable(Variable("V", {1, 2}))
        csp.add_constraint(ElementConstraint(I, [A, B], V))
        csp.add_constraint(NotEqualConstraint(V, A))
        sol = solve(csp)
        self.assertEqual(sol, {"I": 1, "A": 1, "B": 2, "V": 2})
        self.assertTrue(check_solution(csp.constraints, sol))
        # 撤回确实发生过：存在被标记为已撤回的删除
        self.assertTrue(any(r.undone for r in csp.explain()))
        # 每个被删值（含被撤回的）都有原因
        self.assertTrue(all(r.reason for r in csp.explain()))

    def test_explanation_for_every_removed_value(self):
        csp = CSP()
        I = csp.add_variable(Variable("I", {0, 1, 9}))
        V = csp.add_variable(Variable("V", {1, 2, 3}))
        csp.add_constraint(ElementConstraint(I, [1, 2], V))
        self.assertTrue(csp.propagate())
        expl_v = csp.explain("V")
        self.assertEqual(len(expl_v), 1)
        self.assertEqual(expl_v[0].value, 3)
        self.assertIn("无支持", expl_v[0].reason)
        expl_i = csp.explain("I")
        self.assertEqual(len(expl_i), 1)
        self.assertEqual(expl_i[0].value, 9)
        self.assertIn("越界", expl_i[0].reason)


class TestIndependentCheckerAndEnumeration(unittest.TestCase):

    def test_independent_checker(self):
        self.assertTrue(check_element(1, [10, 20], 20))
        self.assertFalse(check_element(2, [10, 20], 20))
        self.assertFalse(check_element(-1, [10, 20], 20))
        self.assertFalse(check_element(0, [], 1))
        self.assertFalse(check_element(0, [10], 11))

    def test_gac_matches_brute_force_projection(self):
        """GAC 后每个变量的域 == 暴力枚举全部解在该变量上的投影。"""

        def scen_constants():
            I = Variable("I", {-1, 0, 1, 2, 3})
            V = Variable("V", {5, 20, 30, 40})
            return [I, V], ElementConstraint(I, [10, 20, 30], V)

        def scen_all_vars():
            I = Variable("I", {0, 1})
            A = Variable("A", {1, 2})
            B = Variable("B", {2, 3})
            V = Variable("V", {1, 2, 3})
            return [I, A, B, V], ElementConstraint(I, [A, B], V)

        def scen_alias_self():
            J = Variable("J", {0, 1, 2})
            W = Variable("W", {0, 5, 9})
            return [J, W], ElementConstraint(J, [J, 9], W)

        def scen_duplicate():
            I = Variable("I", {0, 1})
            A = Variable("A", {1, 2})
            V = Variable("V", {2, 3})
            return [I, A, V], ElementConstraint(I, [A, A], V)

        def scen_empty():
            I = Variable("I", {0, 1})
            V = Variable("V", {1})
            return [I, V], ElementConstraint(I, [], V)

        scenarios = (scen_constants, scen_all_vars, scen_alias_self,
                     scen_duplicate, scen_empty)
        for build in scenarios:
            with self.subTest(scenario=build.__name__):
                variables, con = build()
                domains = {v.name: set(v.domain) for v in variables}
                sols = brute_force_solutions([con], domains)
                csp = CSP()
                for v in variables:
                    csp.add_variable(v)
                csp.add_constraint(con)
                self.assertEqual(csp.propagate(), bool(sols))
                if sols:
                    for v in variables:
                        projection = {s[v.name] for s in sols}
                        self.assertEqual(v.domain, projection)

    def test_solver_solution_in_brute_force_set(self):
        csp = CSP()
        I = csp.add_variable(Variable("I", {0, 1, 2}))
        A = csp.add_variable(Variable("A", {1, 2}))
        B = csp.add_variable(Variable("B", {2, 3}))
        V = csp.add_variable(Variable("V", {1, 2, 3}))
        con = csp.add_constraint(ElementConstraint(I, [A, B, 7], V))
        domains = {v.name: set(v.domain) for v in csp.variables}
        sol = solve(csp)
        self.assertIsNotNone(sol)
        self.assertIn(sol, brute_force_solutions([con], domains))


if __name__ == "__main__":
    unittest.main()
