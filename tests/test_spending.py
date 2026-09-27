import unittest

from binpower.simulate import spawn_streams
from binpower.spending import (
    group_sequential_design,
    group_sequential_power,
    obrien_fleming_spending,
    power_spending,
    simulate_group_sequential,
)


class TestSpendingFunctions(unittest.TestCase):
    def test_endpoints_and_monotonicity(self):
        for spend in (obrien_fleming_spending(0.05), power_spending(0.05, 2.0)):
            self.assertEqual(spend(0.0), 0.0)
            self.assertEqual(spend(1.0), 0.05)
            ts = [i / 10 for i in range(1, 11)]
            vals = [spend(t) for t in ts]
            self.assertTrue(all(a <= b for a, b in zip(vals, vals[1:])))

    def test_obf_more_conservative_early(self):
        obf = obrien_fleming_spending(0.05)
        pow2 = power_spending(0.05, 1.0)  # Pocock-like linear spending
        self.assertLess(obf(0.5), pow2(0.5))

    def test_invalid_alpha(self):
        with self.assertRaises(ValueError):
            obrien_fleming_spending(0.0)
        with self.assertRaises(ValueError):
            power_spending(0.05, rho=-1.0)


class TestGroupSequentialDesign(unittest.TestCase):
    def setUp(self):
        self.design = group_sequential_design(
            [50, 100, 150], 0.5, 0.05, obrien_fleming_spending(0.05))

    def test_exact_size_within_alpha(self):
        self.assertLessEqual(self.design.size, 0.05)
        self.assertGreater(self.design.size, 0.0)
        self.assertEqual(self.design.kind, "exact")

    def test_spending_respects_predeclared_budget(self):
        for look in self.design.looks:
            self.assertLessEqual(look.alpha_spent_cum,
                                 look.alpha_budget_cum + 1e-12)

    def test_boundaries_fixed_and_ordered(self):
        looks = self.design.looks
        self.assertEqual([b.n for b in looks], [50, 100, 150])
        self.assertEqual([b.look for b in looks], [1, 2, 3])

    def test_exact_power_exceeds_size_under_alternative(self):
        power = group_sequential_power(self.design, 0.65)
        self.assertGreater(power, self.design.size)
        # Exact power at p0 equals the exact size.
        self.assertAlmostEqual(group_sequential_power(self.design, 0.5),
                               self.design.size, places=12)

    def test_invalid_look_schedule(self):
        with self.assertRaises(ValueError):
            group_sequential_design([100, 50], 0.5, 0.05, power_spending(0.05))
        with self.assertRaises(ValueError):
            group_sequential_design([], 0.5, 0.05, power_spending(0.05))


class TestGroupSequentialSimulation(unittest.TestCase):
    def setUp(self):
        self.design = group_sequential_design(
            [50, 100, 150], 0.5, 0.05, obrien_fleming_spending(0.05))

    def test_reproducible_and_seed_recorded(self):
        def run():
            stream = spawn_streams(77, ["gs"])["gs"]
            return simulate_group_sequential(
                self.design, p=0.65, trials=800, stream=stream)
        first, second = run(), run()
        self.assertEqual(first, second)
        self.assertEqual(first.seed, second.seed)
        self.assertEqual(first.kind, "empirical")

    def test_stopping_conditions_recorded(self):
        stream = spawn_streams(5, ["gs"])["gs"]
        est = simulate_group_sequential(self.design, p=0.65, trials=500, stream=stream)
        total = sum(count for _, count in est.stopping_conditions)
        self.assertEqual(total, 500)
        text = " ".join(cond for cond, _ in est.stopping_conditions)
        self.assertIn("look", text)

    def test_zero_trials_refused(self):
        stream = spawn_streams(5, ["gs"])["gs"]
        with self.assertRaises(ValueError):
            simulate_group_sequential(self.design, p=0.5, trials=0, stream=stream)

    def test_budget_interruption_preserves_pending_range(self):
        stream = spawn_streams(5, ["gs"])["gs"]
        est = simulate_group_sequential(
            self.design, p=0.65, trials=1000, stream=stream, trial_budget=30)
        self.assertEqual(est.status, "interrupted")
        self.assertEqual(est.trials, 30)
        self.assertGreater(est.ci_high - est.ci_low, 0.1)

    def test_empirical_rate_near_exact_power(self):
        stream = spawn_streams(31337, ["gs"])["gs"]
        est = simulate_group_sequential(self.design, p=0.65, trials=4000, stream=stream)
        self.assertLessEqual(est.ci_low, est.exact_power)
        self.assertGreaterEqual(est.ci_high, est.exact_power)


if __name__ == "__main__":
    unittest.main()
