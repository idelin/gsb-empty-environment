import random
import unittest

from binomial_power import (
    exact_power,
    plan_looks,
    scenario_streams,
    simulate_rejection_rate,
)


def run(seed, **kw):
    args = dict(n=50, p0=0.5, p1=0.65, alpha=0.05, rng=random.Random(seed), seed=seed)
    args.update(kw)
    return simulate_rejection_rate(**args)


class TestSimulation(unittest.TestCase):
    def test_zero_trials_rejected(self):
        with self.assertRaises(ValueError):
            run(1, max_trials=0)

    def test_reproducible_same_seed(self):
        a = run(42, max_trials=2_000)
        b = run(42, max_trials=2_000)
        self.assertEqual(a.rejections, b.rejections)
        self.assertEqual(a.ci, b.ci)

    def test_different_seed_usually_differs(self):
        a = run(1, max_trials=2_000)
        b = run(2, max_trials=2_000)
        self.assertNotEqual(a.rejections, b.rejections)

    def test_estimate_inside_ci_and_near_exact(self):
        res = run(7, max_trials=20_000)
        self.assertLessEqual(res.ci[0], res.estimate)
        self.assertLessEqual(res.estimate, res.ci[1])
        self.assertAlmostEqual(res.estimate, res.exact_power, delta=0.03)
        self.assertAlmostEqual(res.exact_power,
                               exact_power(50, 0.5, 0.65, 0.05)[2], places=15)

    def test_all_rejections_still_reports_interval(self):
        # p1 = 1.0 forces every trial to reject; CI must keep width > 0
        res = run(3, p1=1.0, max_trials=500)
        self.assertEqual(res.rejections, res.trials_done)
        self.assertEqual(res.estimate, 1.0)
        self.assertLess(res.ci[0], 1.0)
        self.assertGreater(res.ci[1] - res.ci[0], 0.0)

    def test_converged_status_and_stop_reason(self):
        plan = plan_looks(max_trials=50_000, n_looks=5, total_alpha=0.05)
        res = run(11, max_trials=50_000, target_half_width=0.02, look_plan=plan)
        self.assertEqual(res.status, "converged")
        self.assertIn("pre-declared look", res.stop_reason)
        self.assertIsNone(res.pending_half_width)
        half = 0.5 * (res.ci[1] - res.ci[0])
        self.assertLessEqual(half, 0.02)

    def test_budget_exhaustion_keeps_pending_range(self):
        res = run(13, max_trials=100, target_half_width=1e-6)
        self.assertEqual(res.status, "budget_exhausted")
        self.assertIsNotNone(res.pending_half_width)
        self.assertGreater(res.pending_half_width, 0.0)
        self.assertEqual(res.trials_done, 100)

    def test_seed_recorded(self):
        res = run(99, max_trials=100)
        self.assertEqual(res.seed, 99)

    def test_fixed_rule_matches_exact_critical(self):
        res = run(5, max_trials=100)
        self.assertEqual(res.critical, exact_power(50, 0.5, 0.65, 0.05)[0])


class TestScenarioStreams(unittest.TestCase):
    def test_independent_and_reproducible(self):
        names = ["a", "b", "c"]
        s1 = scenario_streams(123, names)
        s2 = scenario_streams(123, names)
        seeds = [s1[n][0] for n in names]
        self.assertEqual(seeds, [s2[n][0] for n in names])
        self.assertEqual(len(set(seeds)), 3)
        # replaying one scenario from its recorded child seed reproduces it
        for name in names:
            child_seed, rng = s1[name]
            r1 = simulate_rejection_rate(20, 0.5, 0.7, rng=rng, max_trials=500,
                                         seed=child_seed, scenario=name)
            r2 = simulate_rejection_rate(20, 0.5, 0.7, rng=random.Random(child_seed),
                                         max_trials=500, seed=child_seed, scenario=name)
            self.assertEqual(r1.rejections, r2.rejections)

    def test_scenario_streams_do_not_share_draws(self):
        names = ["x", "y"]
        streams = scenario_streams(5, names)
        draws_x = [streams["x"][1].random() for _ in range(5)]
        draws_y = [streams["y"][1].random() for _ in range(5)]
        self.assertNotEqual(draws_x, draws_y)

    def test_duplicate_names_rejected(self):
        with self.assertRaises(ValueError):
            scenario_streams(1, ["dup", "dup"])


if __name__ == "__main__":
    unittest.main()
