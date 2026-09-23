import unittest

from rational_elimination import (
    NonLinearError,
    ZeroDivisorError,
    format_result,
    prepare_equation,
    solve_equations,
)


class RationalEliminationTest(unittest.TestCase):
    def test_equivalent_circular_equations_are_free_family(self):
        result = solve_equations(["x=y+1", "y=x-1"])

        self.assertEqual(result.status, "free_family")
        self.assertEqual(result.pivot_variables, ("x",))
        self.assertEqual(result.free_variables, ("y",))
        self.assertEqual(result.pivot_expressions, ("x = y + 1",))
        self.assertTrue(any("0 = 0" in step.after for step in result.steps))
        self.assertTrue(any("循环等式通过整行线性组合处理" in step.description for step in result.steps))
        self.assertLessEqual(len(result.steps), 8)

    def test_mutually_exclusive_equations_are_contradiction(self):
        result = solve_equations(["x=y+1", "y=x"])

        self.assertEqual(result.status, "inconsistent")
        self.assertEqual(result.pivot_expressions, ())
        self.assertEqual(result.free_variables, ())
        self.assertTrue(any("0 = 1" in step.description for step in result.steps))
        self.assertIn("矛盾，无解", format_result(result))

    def test_exact_rational_solution_without_floating_point(self):
        result = solve_equations(["2x + 4y = 1/2", "x - y = 1/3"])

        self.assertEqual(result.status, "unique")
        self.assertEqual(result.pivot_expressions, ("x = 11/36", "y = -1/36"))
        flattened = [value for row in result.matrix_after for value in row]
        self.assertTrue(all("/" in value or value in {"0", "1"} for value in flattened))
        self.assertNotIn(".", "".join(value for row in result.matrix_after for value in row))

    def test_symbolic_divisor_creates_nonzero_and_zero_branches(self):
        prepared = prepare_equation("x/(a-1)=2")
        result = solve_equations(["x/(a-1)=2"])

        self.assertEqual(prepared.constraints, ("a - 1 ≠ 0",))
        self.assertEqual(len(prepared.branches), 2)
        self.assertEqual(prepared.branches[0].condition, "a - 1 ≠ 0")
        self.assertEqual(prepared.branches[0].result, "x - 2a + 2 = 0")
        self.assertEqual(prepared.branches[1].condition, "a - 1 = 0")
        self.assertEqual(prepared.branches[1].result, "原除法未定义；无解")
        self.assertTrue(any(branch.kind == "无法消去分支" for branch in result.branches))
        self.assertEqual(result.status, "free_family")
        self.assertEqual(result.free_variables, ("a",))
        self.assertEqual(result.pivot_expressions, ("x = 2a - 2",))
        self.assertTrue(all("a - 1 ≠ 0" in step.domain for step in result.steps[1:]))


    def test_negative_top_level_symbolic_divisor(self):
        prepared = prepare_equation("-x/(a-1)=2")

        self.assertEqual(prepared.constraints, ("a - 1 ≠ 0",))
        self.assertEqual(str(prepared.lhs), "-x - 2a + 2")

    def test_constant_division_uses_exact_rational(self):
        prepared = prepare_equation("x/2 + y/4 = 1/3")

        self.assertEqual(prepared.constraints, ())
        self.assertEqual(str(prepared.lhs), "1/2x + 1/4y - 1/3")

    def test_variable_times_variable_is_rejected_as_nonlinear(self):
        with self.assertRaises(NonLinearError):
            prepare_equation("x*y = 1")

    def test_static_zero_divisor_is_rejected(self):
        with self.assertRaises(ZeroDivisorError):
            prepare_equation("x/(1-1) = 2")

    def test_zero_pivot_does_not_divide(self):
        result = solve_equations(["0*x + y = 2", "y = 2", "z = 1"])
        rendered = format_result(result)

        self.assertEqual(result.status, "free_family")
        self.assertEqual(result.pivot_variables, ("y", "z"))
        self.assertEqual(result.free_variables, ("x",))
        self.assertIn("零系数分支", rendered)
        self.assertIn("不执行除法", rendered)
        self.assertFalse(any(step.name == "归一化主元" and " x " in step.description for step in result.steps))


if __name__ == "__main__":
    unittest.main(verbosity=2)
