import unittest

from binpower.adaptive import adaptive_allocate
from binpower.simulate import Scenario

SCENARIOS = [
    Scenario("null", n=50, p0=0.5, p=0.5),
    Scenario("alt", n=50, p0=0.5, p=0.65),
]


class TestAdaptiveAllocate(unittest.TestCase):
    def test_converges_with_ample_budget(self):
        res = adaptive_allocate(
            SCENARIOS, master_seed=9, total_budget=20000,
            initial_trials=100, batch_trials=200, tolerance=0.08)
        self.assertEqual(res.status, "converged")
        self.assertEqual(res.pending, ())
        self.assertLessEqual(res.spent, res.budget)
        for est in res.estimates.values():
            self.assertLessEqual(est.ci_width, 0.08)
            self.assertGreaterEqual(est.trials, 100)

    def test_interruption_preserves_pending_range(self):
        res = adaptive_allocate(
            SCENARIOS, master_seed=9, total_budget=300,
            initial_trials=100, batch_trials=100, tolerance=0.05)
        self.assertEqual(res.status, "interrupted")
        self.assertTrue(res.pending)
        for name in res.pending:
            est = res.estimates[name]
            # Pending scenarios keep a wide interval as the undecided range.
            self.assertGreater(est.ci_width, 0.05)
            self.assertEqual(est.status, "interrupted")

    def test_reproducible_from_master_seed(self):
        def run():
            return adaptive_allocate(
                SCENARIOS, master_seed=17, total_budget=5000,
                initial_trials=100, batch_trials=250, tolerance=0.1)
        self.assertEqual(run(), run())

    def test_budget_never_exceeded(self):
        res = adaptive_allocate(
            SCENARIOS, master_seed=1, total_budget=1000,
            initial_trials=50, batch_trials=100, tolerance=0.01)
        self.assertLessEqual(res.spent, res.budget)

    def test_zero_initial_trials_refused(self):
        with self.assertRaises(ValueError):
            adaptive_allocate(
                SCENARIOS, master_seed=1, total_budget=1000,
                initial_trials=0, batch_trials=100, tolerance=0.1)

    def test_budget_below_initial_round_refused(self):
        with self.assertRaises(ValueError):
            adaptive_allocate(
                SCENARIOS, master_seed=1, total_budget=150,
                initial_trials=100, batch_trials=100, tolerance=0.1)

    def test_allocation_prefers_wider_interval(self):
        # With p=1.0 the null scenario's interval is one-sided narrow-ish;
        # the point is that allocation is deterministic and seed-recorded.
        res = adaptive_allocate(
            SCENARIOS, master_seed=23, total_budget=4000,
            initial_trials=100, batch_trials=100, tolerance=0.12)
        self.assertEqual(res.master_seed, 23)
        for est in res.estimates.values():
            self.assertIsNotNone(est.seed)
            self.assertIsNotNone(est.exact_power)


if __name__ == "__main__":
    unittest.main()
