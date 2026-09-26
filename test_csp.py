import unittest

from csp import CSP, Constraint, Solver, check_solution


def make_queens(n=4):
    domains = {f"q{r}": set(range(n)) for r in range(n)}
    constraints = []
    for i in range(n):
        for j in range(i + 1, n):
            constraints.append(
                Constraint(
                    (f"q{i}", f"q{j}"),
                    lambda a, b, d=j - i: a != b and abs(a - b) != d,
                    name=f"attack_{i}_{j}",
                )
            )
    return CSP(domains, constraints)


def make_small():
    # x<y, y<z，域均为 {1,2,3,4}；基础层 GAC 后 x∈{1,2}, y∈{2,3}, z∈{3,4}
    domains = {"x": {1, 2, 3, 4}, "y": {1, 2, 3, 4}, "z": {1, 2, 3, 4}}
    constraints = [
        Constraint(("x", "y"), lambda a, b: a < b, name="x<y"),
        Constraint(("y", "z"), lambda a, b: a < b, name="y<z"),
    ]
    return CSP(domains, constraints)


class TestBasicSolve(unittest.TestCase):
    def test_solve_and_verify(self):
        csp = make_queens(4)
        solution = Solver(csp).solve()
        self.assertIsNotNone(solution)
        self.assertTrue(check_solution(csp, solution))

    def test_unsat_returns_none(self):
        csp = CSP(
            {"a": {1}, "b": {1}},
            [Constraint(("a", "b"), lambda x, y: x != y, name="neq")],
        )
        self.assertIsNone(Solver(csp).solve())

    def test_base_constraints_unchanged_after_solve(self):
        csp = make_queens(4)
        before = {v: set(d) for v, d in csp.domains.items()}
        Solver(csp).solve()
        self.assertEqual({v: set(d) for v, d in csp.domains.items()}, before)
        for d in csp.domains.values():
            self.assertIsInstance(d, frozenset)


class TestNestedScopes(unittest.TestCase):
    def test_inner_conflict_outer_still_solvable(self):
        csp = make_queens(4)
        s = Solver(csp)
        self.assertTrue(s.push({"q0": 1}))     # 层1
        self.assertFalse(s.push({"q1": 1}))    # 层2 同列冲突，自动弹回
        self.assertEqual(s.layer, 1)
        self.assertTrue(s.push({"q1": 3}))     # 外层换个假设继续
        solution = s.solve()
        self.assertIsNotNone(solution)
        self.assertTrue(check_solution(csp, solution))

    def test_deep_nesting(self):
        csp = make_small()
        s = Solver(csp)
        self.assertTrue(s.push({"x": 1}))      # 层1
        self.assertTrue(s.push({"y": 2}))      # 层2
        self.assertFalse(s.push({"z": 1}))     # 层3 冲突
        self.assertEqual(s.layer, 2)
        self.assertTrue(s.push({"z": 3}))      # 层3 换值
        self.assertEqual(s.solve(), {"x": 1, "y": 2, "z": 3})

    def test_repeated_assumptions_count_layers(self):
        csp = make_small()
        s = Solver(csp)
        s.push({"x": 1})
        s.push({"x": 1})  # 同一假设重复压入，仍计一层
        self.assertEqual(s.layer, 2)
        s.pop()
        self.assertEqual(s.layer, 1)
        self.assertEqual(s.domains["x"], {1})  # 外层假设仍生效
        s.pop()
        self.assertEqual(s.layer, 0)
        self.assertEqual(s.domains["x"], {1, 2})  # 恢复基础层传播后的域

    def test_pop_leaves_no_pollution(self):
        csp = make_queens(4)
        s = Solver(csp)
        snapshot = {v: set(d) for v, d in s.domains.items()}
        s.push({"q0": 1})
        s.push({"q1": 3})
        s.push({"q2": 0})
        s.pop(); s.pop(); s.pop()
        self.assertEqual(s.domains, snapshot)
        self.assertEqual(s.trail, [])
        self.assertEqual(s.layer, 0)

    def test_failed_push_leaves_no_pollution(self):
        csp = make_small()
        s = Solver(csp)
        snapshot = {v: set(d) for v, d in s.domains.items()}
        self.assertFalse(s.push({"x": 3}))  # x=3 不在基础层域内，立即冲突
        self.assertEqual(s.domains, snapshot)
        self.assertEqual(s.layer, 0)


class TestLayerAttribution(unittest.TestCase):
    def test_trail_entries_carry_correct_layer(self):
        csp = make_small()
        s = Solver(csp)
        base_trail_len = len(s.trail)  # 基础层传播记录归属层 0
        self.assertTrue(all(l == 0 for _, _, l in s.trail))
        s.push({"x": 1})           # 层1：删 x 的 2
        s.push({"y": 2})           # 层2：删 y 的 3
        layers = {entry[2] for entry in s.trail}
        self.assertEqual(layers, {0, 1, 2})
        layer1_vars = {v for v, _, l in s.trail if l == 1}
        layer2_vars = {v for v, _, l in s.trail if l == 2}
        self.assertEqual(layer1_vars, {"x"})
        self.assertEqual(layer2_vars, {"y"})
        s.pop()
        self.assertTrue(all(l in (0, 1) for _, _, l in s.trail))  # 层2记录已撤销
        s.pop()
        self.assertEqual(len(s.trail), base_trail_len)  # 只剩基础层记录
        self.assertTrue(all(l == 0 for _, _, l in s.trail))


class TestIndependentChecker(unittest.TestCase):
    def test_checker_rejects_wrong_assignment(self):
        csp = make_queens(4)
        bad = {"q0": 0, "q1": 0, "q2": 2, "q3": 3}
        self.assertFalse(check_solution(csp, bad))

    def test_checker_rejects_out_of_domain(self):
        csp = make_small()
        self.assertFalse(check_solution(csp, {"x": 1, "y": 2, "z": 99}))
        self.assertFalse(check_solution(csp, {"x": 1, "y": 2}))  # 缺变量

    def test_checker_accepts_real_solution(self):
        csp = make_queens(4)
        self.assertTrue(check_solution(csp, {"q0": 1, "q1": 3, "q2": 0, "q3": 2}))


if __name__ == "__main__":
    unittest.main()
