import gc
import unittest

from dualtag import Budget, active_tags, derivative, jvp
from dualtag.errors import BudgetExceeded


class TestBudget(unittest.TestCase):
    def test_step_budget_exceeded(self):
        with self.assertRaises(BudgetExceeded) as ctx:
            with Budget(max_steps=3):
                derivative(lambda x: (((x * x + x) * x + x) * x + x) * x, 2.0)
        self.assertEqual(ctx.exception.kind, "step")

    def test_step_budget_counts(self):
        with Budget(max_steps=10_000) as budget:
            derivative(lambda x: x * x, 2.0)
        self.assertGreater(budget.steps, 0)

    def test_memory_budget_exceeded(self):
        with self.assertRaises(BudgetExceeded) as ctx:
            with Budget(max_memory=2):
                jvp(lambda x: x * x, 2.0, (1.0, 1.0, 1.0, 1.0))
        self.assertEqual(ctx.exception.kind, "memory")

    def test_memory_released_after_scope(self):
        budget = Budget(max_memory=1000)
        with budget:
            jvp(lambda x: x * x, 2.0, (1.0,) * 8)
        gc.collect()
        self.assertEqual(budget.memory, 0)
        self.assertGreaterEqual(budget.peak_memory, 8)

    def test_recovery_after_budget_exceeded(self):
        with self.assertRaises(BudgetExceeded):
            with Budget(max_steps=1):
                derivative(lambda x: x * x * x, 2.0)
        # tag state must not be corrupted by the aborted computation
        self.assertEqual(active_tags(), ())
        self.assertAlmostEqual(derivative(lambda x: x * x, 3.0), 6.0, places=9)

    def test_nested_budgets_restore(self):
        outer = Budget(max_steps=10_000)
        with outer:
            with Budget(max_steps=10_000):
                derivative(lambda x: x * x, 1.0)
            derivative(lambda x: x * x, 2.0)
        self.assertGreater(outer.steps, 0)


if __name__ == "__main__":
    unittest.main()
