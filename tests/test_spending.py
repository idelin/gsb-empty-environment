import unittest

from binomial_power import LookPlan, plan_looks


class TestLookPlan(unittest.TestCase):
    def test_budget_sums_to_total(self):
        plan = plan_looks(max_trials=10_000, n_looks=4, total_alpha=0.05, rho=2.0)
        self.assertAlmostEqual(sum(plan.errors), 0.05, places=12)
        self.assertEqual(plan.looks[-1], 10_000)
        self.assertEqual(len(plan.looks), len(plan.errors))

    def test_spending_is_conservative_early(self):
        plan = plan_looks(max_trials=1_000, n_looks=4, total_alpha=0.05, rho=2.0)
        # t**2 spending: first look spends only 1/16 of the budget
        self.assertAlmostEqual(plan.errors[0], 0.05 / 16, places=12)

    def test_single_look_spends_everything(self):
        plan = plan_looks(max_trials=500, n_looks=1, total_alpha=0.05)
        self.assertEqual(plan.looks, (500,))
        self.assertAlmostEqual(plan.errors[0], 0.05, places=12)

    def test_invalid_plans_rejected(self):
        with self.assertRaises(ValueError):
            LookPlan(looks=(), errors=(), total_alpha=0.05)
        with self.assertRaises(ValueError):
            LookPlan(looks=(10, 5), errors=(0.02, 0.03), total_alpha=0.05)
        with self.assertRaises(ValueError):
            LookPlan(looks=(10, 20), errors=(0.02, 0.02), total_alpha=0.05)
        with self.assertRaises(ValueError):
            plan_looks(max_trials=0, n_looks=1)

    def test_more_looks_than_trials(self):
        plan = plan_looks(max_trials=3, n_looks=10, total_alpha=0.05)
        self.assertEqual(plan.looks, (1, 2, 3))
        self.assertAlmostEqual(sum(plan.errors), 0.05, places=12)


if __name__ == "__main__":
    unittest.main()
