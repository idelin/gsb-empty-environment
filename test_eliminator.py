import unittest
from fractions import Fraction

from linear import LinearForm, solve_for, solve_linear_system, substitute
from parser import parse_equation
from eliminator import solve_text


def main_branch(branches):
    return branches[0].solution


class LinearFormTest(unittest.TestCase):
    def test_arithmetic_exact(self):
        f = LinearForm.variable("x").scale(Fraction(2, 3)) + LinearForm.constant(
            Fraction(1, 6)
        )
        self.assertEqual(f.terms["x"], Fraction(2, 3))
        self.assertEqual(f.const, Fraction(1, 6))

    def test_solve_for(self):
        eq = LinearForm({"x": 2, "y": -3}, -1)  # 2x - 3y - 1 = 0
        g = solve_for(eq, "x")
        self.assertEqual(g, LinearForm({"y": Fraction(3, 2)}, Fraction(1, 2)))

    def test_substitute(self):
        form = LinearForm({"x": 1, "y": 1})
        out = substitute(form, {"x": LinearForm({"z": 2}, 1)})
        self.assertEqual(out, LinearForm({"z": 2, "y": 1}, 1))


class ParserTest(unittest.TestCase):
    def test_linear_equation(self):
        form, conds, _ = parse_equation("x = y + 1")
        self.assertEqual(form, LinearForm({"x": 1, "y": -1}, -1))
        self.assertEqual(conds, [])

    def test_division_records_condition(self):
        form, conds, _ = parse_equation("x / (y - 1) = 2")
        self.assertEqual(form, LinearForm({"x": 1, "y": -2}, 2))
        self.assertEqual(conds, [LinearForm({"y": 1}, -1)])

    def test_constant_denominator_no_branch(self):
        _, conds, notes = parse_equation("x / 2 = 1")
        self.assertEqual(conds, [])
        self.assertTrue(any("无需分支" in n for n in notes))

    def test_nonlinear_rejected(self):
        with self.assertRaises(ValueError):
            parse_equation("x * y = 1")

    def test_zero_denominator_rejected(self):
        with self.assertRaises(ValueError):
            parse_equation("x / (y - y) = 1")


class EliminatorTest(unittest.TestCase):
    def test_free_family_not_contradiction(self):
        sol = main_branch(solve_text(["x = y + 1", "y = x - 1"]))
        self.assertEqual(sol.status, "consistent")
        self.assertEqual(sol.free_vars, ["y"])
        self.assertEqual(sol.assignments["x"], LinearForm({"y": 1}, 1))
        kinds = [s.kind for s in sol.log]
        self.assertIn("cycle", kinds)  # 循环走线性代数而非无限替换
        self.assertIn("gauss", kinds)

    def test_contradiction(self):
        sol = main_branch(solve_text(["x = y + 1", "y = x"]))
        self.assertEqual(sol.status, "contradiction")

    def test_acyclic_chain(self):
        sol = main_branch(solve_text(["x = y + 1", "y = z + 2"]))
        self.assertEqual(sol.status, "consistent")
        self.assertEqual(sol.assignments["x"], LinearForm({"z": 1}, 3))
        self.assertEqual(sol.assignments["y"], LinearForm({"z": 1}, 2))
        self.assertEqual(sol.free_vars, ["z"])
        self.assertNotIn("cycle", [s.kind for s in sol.log])

    def test_division_branches(self):
        branches = solve_text(["x / (y - 1) = 2", "y = z + 3"])
        self.assertEqual(len(branches), 2)
        main = branches[0].solution
        self.assertEqual(main.status, "consistent")
        self.assertEqual(main.assignments["x"], LinearForm({"z": 2}, 4))
        self.assertEqual(main.assignments["y"], LinearForm({"z": 1}, 3))
        self.assertEqual(main.conditions, [LinearForm({"z": 1}, 2)])
        self.assertIsNone(branches[1].solution)
        self.assertIn("舍弃", branches[1].note)

    def test_exact_rationals(self):
        sol = main_branch(solve_text(["2*x = 3*y", "y = 1/4"]))
        self.assertEqual(sol.status, "consistent")
        self.assertEqual(sol.assignments["x"], LinearForm.constant(Fraction(3, 8)))
        self.assertEqual(sol.assignments["y"], LinearForm.constant(Fraction(1, 4)))
        self.assertEqual(sol.free_vars, [])

    def test_conflicting_definitions(self):
        sol = main_branch(solve_text(["x = y + 1", "x = y + 2"]))
        self.assertEqual(sol.status, "contradiction")

    def test_infeasible_condition(self):
        sol = main_branch(solve_text(["y = 0", "x / y = 1"]))
        self.assertEqual(sol.status, "infeasible")


class GaussTest(unittest.TestCase):
    def test_inconsistent_system(self):
        eqs = [
            LinearForm({"x": 1, "y": -1}, -1),
            LinearForm({"x": -1, "y": 1}, 0),
        ]
        consistent, _, _ = solve_linear_system(eqs, [])
        self.assertFalse(consistent)

    def test_rank_and_free_vars(self):
        eqs = [
            LinearForm({"x": 1, "y": -1}, -1),
            LinearForm({"x": -1, "y": 1}, 1),
        ]
        consistent, assignments, free = solve_linear_system(eqs, [])
        self.assertTrue(consistent)
        self.assertEqual(free, ["y"])
        self.assertEqual(assignments["x"], LinearForm({"y": 1}, 1))


if __name__ == "__main__":
    unittest.main()
