"""整数精确求解测试：唯一解、解格、矛盾证书、整除失败、无浮点。"""
import unittest
from fractions import Fraction

from unitchk import (Registry, Leaf, Mul, Div, Pow, Equation,
                     build_system, solve_integer_system, Solution, Infeasible,
                     solution_unit_vectors, verify)


def make_registry():
    reg = Registry(["L", "M", "T", "Θ"])
    reg.define("m", {"L": 1})
    reg.define("s", {"T": 1})
    reg.define("kg", {"M": 1})
    reg.define("mpss", {"L": 1, "T": -2})   # 加速度
    return reg


def force_system():
    """F = kg * m/s^2，求未知单位 F 的指数向量。"""
    reg = make_registry()
    eqs = [Equation(Leaf("F"), Mul(Leaf("kg"), Leaf("mpss")), "F=ma")]
    return reg, build_system(eqs, reg, {"F"})


class TestUniqueSolution(unittest.TestCase):
    def test_force_from_ma(self):
        reg, system = force_system()
        sol = solve_integer_system(system.A, system.b,
                                   system.var_names, system.row_labels)
        self.assertIsInstance(sol, Solution)
        vectors = solution_unit_vectors(system, sol)
        base, frees = vectors["F"]
        self.assertEqual(base, (1, 1, -2, 0))   # L M T^-2
        self.assertEqual(frees, [])
        self.assertEqual(verify.verify_substitution(
            system, {v: sol.particular[k]
                     for k, v in enumerate(system.variables)}), [])
        self.assertEqual(verify.verify_kernel(system, sol), [])

    def test_solution_is_exact_integers(self):
        reg, system = force_system()
        sol = solve_integer_system(system.A, system.b,
                                   system.var_names, system.row_labels)
        for v in sol.particular:
            self.assertIsInstance(v, int)
            self.assertNotIsInstance(v, float)
        for vec in sol.lattice:
            for v in vec:
                self.assertIsInstance(v, int)

    def test_steps_have_domains(self):
        reg, system = force_system()
        sol = solve_integer_system(system.A, system.b,
                                   system.var_names, system.row_labels)
        self.assertTrue(len(sol.steps) > 0)
        self.assertTrue(all(st.domain for st in sol.steps))


class TestSolutionLattice(unittest.TestCase):
    def test_underdetermined_gives_lattice(self):
        """X * Y = m：4 个方程 8 个变量，应得 4 维解格与自由维度。"""
        reg = make_registry()
        eqs = [Equation(Mul(Leaf("X"), Leaf("Y")), Leaf("m"), "X*Y=m")]
        system = build_system(eqs, reg, {"X", "Y"})
        sol = solve_integer_system(system.A, system.b,
                                   system.var_names, system.row_labels)
        self.assertIsInstance(sol, Solution)
        self.assertEqual(len(sol.lattice), 4)
        self.assertEqual(len(sol.free_variables), 4)
        self.assertEqual(verify.verify_kernel(system, sol), [])
        # 格上任意整数点都满足原始约束
        assign = sol.assignment(params=[2, -3, 5, 7])
        keyed = {v: assign[system.var_names[k]]
                 for k, v in enumerate(system.variables)}
        self.assertEqual(verify.verify_substitution(system, keyed), [])

    def test_free_dimensions_reported_per_unit(self):
        reg = make_registry()
        eqs = [Equation(Mul(Leaf("X"), Leaf("Y")), Leaf("m"), "X*Y=m")]
        system = build_system(eqs, reg, {"X", "Y"})
        sol = solve_integer_system(system.A, system.b,
                                   system.var_names, system.row_labels)
        vectors = solution_unit_vectors(system, sol)
        self.assertEqual(vectors["X"][0], (1, 0, 0, 0))  # 特解：X 取长度
        self.assertTrue(len(vectors["X"][1]) > 0)        # 仍有自由方向


class TestInfeasibility(unittest.TestCase):
    def test_contradiction_certificate(self):
        """X*s = m 与 X*s = kg 矛盾：给出整数证书并可独立验证。"""
        reg = make_registry()
        eqs = [Equation(Mul(Leaf("X"), Leaf("s")), Leaf("m"), "eq1"),
               Equation(Mul(Leaf("X"), Leaf("s")), Leaf("kg"), "eq2")]
        system = build_system(eqs, reg, {"X"})
        res = solve_integer_system(system.A, system.b,
                                   system.var_names, system.row_labels)
        self.assertIsInstance(res, Infeasible)
        self.assertEqual(res.kind, "contradiction")
        cert = res.evidence["certificate"]
        self.assertTrue(all(isinstance(c, int) for c in cert))
        self.assertNotEqual(res.evidence["certificate_rhs"], 0)
        self.assertTrue(verify.check_infeasibility(system.A, system.b,
                                                   res.evidence))

    def test_divisibility_failure(self):
        """X^2 = m：2*X[L] = 1 在 Z 上无解，给出整除性证据。"""
        reg = make_registry()
        eqs = [Equation(Pow(Leaf("X"), 2), Leaf("m"), "X^2=m")]
        system = build_system(eqs, reg, {"X"})
        res = solve_integer_system(system.A, system.b,
                                   system.var_names, system.row_labels)
        self.assertIsInstance(res, Infeasible)
        self.assertEqual(res.kind, "divisibility")
        self.assertEqual(res.evidence["pivot"], 2)
        self.assertEqual(res.evidence["rhs"], 1)
        self.assertTrue(verify.check_infeasibility(system.A, system.b,
                                                   res.evidence))

    def test_no_float_anywhere(self):
        reg = make_registry()
        eqs = [Equation(Pow(Leaf("X"), 2), Leaf("m"), "X^2=m")]
        system = build_system(eqs, reg, {"X"})
        res = solve_integer_system(system.A, system.b,
                                   system.var_names, system.row_labels)

        def walk(obj):
            self.assertNotIsInstance(obj, float)
            if isinstance(obj, dict):
                for v in obj.values():
                    walk(v)
            elif isinstance(obj, (list, tuple)):
                for v in obj:
                    walk(v)
        walk(res.evidence)

    def test_tampered_evidence_rejected(self):
        reg = make_registry()
        eqs = [Equation(Pow(Leaf("X"), 2), Leaf("m"), "X^2=m")]
        system = build_system(eqs, reg, {"X"})
        res = solve_integer_system(system.A, system.b,
                                   system.var_names, system.row_labels)
        bad = dict(res.evidence)
        bad["rhs"] = 2  # 篡改：2 可被 2 整除
        self.assertFalse(verify.check_infeasibility(system.A, system.b, bad))


class TestSolverEdgeCases(unittest.TestCase):
    def test_empty_system_all_free(self):
        sol = solve_integer_system([], [], ["a", "b"])
        self.assertIsInstance(sol, Solution)
        self.assertEqual(len(sol.lattice), 2)

    def test_zero_variables_inconsistent(self):
        res = solve_integer_system([[], []], [0, 5], [], ["r0", "r1"])
        self.assertIsInstance(res, Infeasible)
        self.assertEqual(res.kind, "contradiction")

    def test_rectangular_wide(self):
        # x + 2y = 4 → 特解 + 1 维格
        sol = solve_integer_system([[1, 2]], [4], ["x", "y"])
        self.assertIsInstance(sol, Solution)
        self.assertEqual(len(sol.lattice), 1)
        x = sol.assignment([3])
        self.assertEqual(x["x"] + 2 * x["y"], 4)

    def test_rectangular_tall_consistent(self):
        # 2x = 4, 3x = 6 → x = 2
        sol = solve_integer_system([[2], [3]], [4, 6], ["x"])
        self.assertIsInstance(sol, Solution)
        self.assertEqual(sol.particular, [2])

    def test_rectangular_tall_inconsistent(self):
        res = solve_integer_system([[2], [3]], [4, 7], ["x"])
        self.assertIsInstance(res, Infeasible)


if __name__ == "__main__":
    unittest.main()
