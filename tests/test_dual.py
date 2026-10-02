"""Core dual-number arithmetic, tags, budgets, and tensor tests."""
import math
import unittest

import dualtag as dt
from dualtag import dual


class TestDualArithmetic(unittest.TestCase):
    def test_basic_ops_match_hand_derivatives(self):
        tag = dt.fresh_tag(1)
        x = dt.Dual(tag, 2.0, [1.0])
        y = (x * x + 3.0) / x  # f = x + 3/x, f' = 1 - 3/x^2
        self.assertAlmostEqual(dt.primal_value(y), 3.5)
        self.assertAlmostEqual(y.tangent[0], 1.0 - 3.0 / 4.0)

    def test_elementary_functions(self):
        tag = dt.fresh_tag(1)
        x = dt.Dual(tag, 0.7, [1.0])
        self.assertAlmostEqual(dt.sin(x).tangent[0], math.cos(0.7))
        self.assertAlmostEqual(dt.cos(x).tangent[0], -math.sin(0.7))
        self.assertAlmostEqual(dt.exp(x).tangent[0], math.exp(0.7))
        self.assertAlmostEqual(dt.log(x).tangent[0], 1.0 / 0.7)
        self.assertAlmostEqual(dt.sqrt(x).tangent[0], 0.5 / math.sqrt(0.7))
        self.assertAlmostEqual(dt.tan(x).tangent[0], 1.0 / math.cos(0.7) ** 2)

    def test_integer_power_including_zero_base(self):
        tag = dt.fresh_tag(1)
        x = dt.Dual(tag, 0.0, [1.0])
        self.assertEqual(dt.power(x, 1).tangent[0], 1.0)   # d/dx x = 1
        self.assertEqual(dt.power(x, 2).tangent[0], 0.0)   # d/dx x^2 at 0
        self.assertEqual(dt.power(x, 3).primal, 0.0)

    def test_domain_errors(self):
        with self.assertRaises(dt.DomainError):
            dt.log(-1.0)
        with self.assertRaises(dt.DomainError):
            dt.sqrt(-0.5)
        with self.assertRaises(dt.DomainError):
            dt.power(-2.0, 0.5)

    def test_nonsmooth_branches_flagged(self):
        tag = dt.fresh_tag(1)
        with self.assertRaises(dt.NonDifferentiableError):
            dt.abs_(dt.Dual(tag, 0.0, [1.0]))
        with self.assertRaises(dt.NonDifferentiableError):
            dt.sqrt(dt.Dual(tag, 0.0, [1.0]))
        with self.assertRaises(dt.NonDifferentiableError):
            dt.min_(dt.Dual(tag, 1.0, [1.0]), 1.0)
        with self.assertRaises(dt.NonDifferentiableError):
            dt.max_(1.5, dt.Dual(tag, 1.5, [1.0]))
        # Smooth sides still work and carry the right sign.
        self.assertAlmostEqual(dt.abs_(dt.Dual(tag, -2.0, [1.0])).tangent[0], -1.0)
        self.assertAlmostEqual(dt.min_(dt.Dual(tag, 1.0, [1.0]), 2.0).tangent[0], 1.0)


class TestTagsAndBudgets(unittest.TestCase):
    def test_tag_ids_are_monotonic_and_never_reused(self):
        before = dt.tags_issued()
        t1 = dt.fresh_tag()
        t2 = dt.fresh_tag()
        self.assertGreater(t1.ident, before)
        self.assertGreater(t2.ident, t1.ident)

    def test_step_budget_exceeded(self):
        budget = dt.Budget(max_steps=0, max_allocs=10**9)
        with self.assertRaises(dt.BudgetExceeded):
            dt.derivative(lambda x: x ** 3, 2.0, budget=budget)

    def test_memory_budget_exceeded(self):
        budget = dt.Budget(max_steps=10**9, max_allocs=1)
        with self.assertRaises(dt.BudgetExceeded):
            dt.derivative(lambda x: x ** 3, 2.0, budget=budget)

    def test_cancellation_restores_state(self):
        # A computation cancelled mid-way (budget) must not leak tag or
        # budget state into later computations.
        budget = dt.Budget(max_steps=0, max_allocs=10**9)
        with self.assertRaises(dt.BudgetExceeded):
            dt.derivative(lambda x: x ** 3, 2.0, budget=budget)
        self.assertIs(dt.get_budget().__class__, dt.Budget)
        value, deriv = dt.derivative(lambda x: x ** 3, 2.0)
        self.assertAlmostEqual(value, 8.0)
        self.assertAlmostEqual(deriv, 12.0)

    def test_user_exception_inside_differentiation_does_not_corrupt(self):
        def boom(x):
            raise RuntimeError("cancelled")
        with self.assertRaises(RuntimeError):
            dt.derivative(boom, 1.0)
        _, deriv = dt.derivative(lambda x: x ** 3, 2.0)
        self.assertAlmostEqual(deriv, 12.0)

    def test_budget_scope_restores_on_exception(self):
        outer = dt.get_budget()
        try:
            with dt.budget_scope(dt.Budget(max_steps=5)):
                raise RuntimeError("boom")
        except RuntimeError:
            pass
        self.assertIs(dt.get_budget(), outer)


class TestTensor(unittest.TestCase):
    def test_matmul(self):
        a = dt.Tensor.from_rows([[1, 2], [3, 4]])
        b = dt.Tensor.from_rows([[5, 6], [7, 8]])
        self.assertTrue(a.matmul(b).almost_equal(
            dt.Tensor.from_rows([[19, 22], [43, 50]])))

    def test_add_sub_scale_norm(self):
        a = dt.Tensor.from_rows([[1, 2], [3, 4]])
        b = dt.Tensor.identity(2)
        self.assertTrue((a - b).almost_equal(dt.Tensor.from_rows([[0, 2], [3, 3]])))
        self.assertAlmostEqual(dt.Tensor([3.0, 4.0], (2,)).norm(), 5.0)

    def test_solve(self):
        x = dt.solve([[2.0, 1.0], [1.0, 3.0]], [1.0, 2.0])
        self.assertAlmostEqual(x[0], 0.2)
        self.assertAlmostEqual(x[1], 0.6)

    def test_solve_singular_raises(self):
        with self.assertRaises(dt.SingularJacobianError):
            dt.solve([[1.0, 2.0], [2.0, 4.0]], [1.0, 2.0])
        self.assertTrue(dt.is_singular([[1.0, 2.0], [2.0, 4.0]]))
        self.assertFalse(dt.is_singular([[2.0, 1.0], [1.0, 3.0]]))


if __name__ == "__main__":
    unittest.main()
