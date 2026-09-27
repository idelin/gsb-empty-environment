import unittest

from binpower.exact import exact_power
from binpower.simulate import (
    Scenario,
    child_seed,
    run_scenarios,
    simulate_rejection_rate,
    spawn_streams,
)

SCENARIOS = [
    Scenario("null", n=50, p0=0.5, p=0.5),
    Scenario("weak", n=50, p0=0.5, p=0.6),
    Scenario("strong", n=50, p0=0.5, p=0.75),
]


class TestStreams(unittest.TestCase):
    def test_child_seed_depends_only_on_master_and_label(self):
        self.assertEqual(child_seed(7, "a"), child_seed(7, "a"))
        self.assertNotEqual(child_seed(7, "a"), child_seed(7, "b"))
        self.assertNotEqual(child_seed(7, "a"), child_seed(8, "a"))

    def test_streams_are_independent_of_siblings(self):
        solo = spawn_streams(42, ["a"])["a"]
        among = spawn_streams(42, ["x", "a", "y"])["a"]
        self.assertEqual(solo.seed, among.seed)
        self.assertEqual([solo.rng.random() for _ in range(5)],
                         [among.rng.random() for _ in range(5)])


class TestSimulateRejectionRate(unittest.TestCase):
    def setUp(self):
        self.stream = spawn_streams(123, ["s"])["s"]

    def test_zero_trials_refused(self):
        with self.assertRaises(ValueError):
            simulate_rejection_rate(
                scenario="s", n=10, critical=8, p=0.5, trials=0, stream=self.stream)

    def test_zero_budget_refused(self):
        with self.assertRaises(ValueError):
            simulate_rejection_rate(
                scenario="s", n=10, critical=8, p=0.5, trials=5,
                stream=self.stream, trial_budget=0)

    def test_seed_and_trials_recorded(self):
        est = simulate_rejection_rate(
            scenario="s", n=10, critical=8, p=0.5, trials=100, stream=self.stream)
        self.assertEqual(est.seed, self.stream.seed)
        self.assertEqual(est.trials, 100)
        self.assertEqual(est.kind, "empirical")

    def test_reproducible_same_seed(self):
        def run():
            stream = spawn_streams(999, ["s"])["s"]
            return simulate_rejection_rate(
                scenario="s", n=30, critical=20, p=0.6, trials=500, stream=stream)
        self.assertEqual(run(), run())

    def test_all_rejections_still_have_interval_width(self):
        est = simulate_rejection_rate(
            scenario="s", n=10, critical=3, p=1.0, trials=50, stream=self.stream)
        self.assertEqual(est.rejections, 50)
        self.assertEqual(est.rate, 1.0)
        self.assertEqual(est.ci_high, 1.0)
        self.assertLess(est.ci_low, 1.0)
        self.assertGreater(est.ci_high - est.ci_low, 0.0)

    def test_budget_interruption_preserves_pending_range(self):
        est = simulate_rejection_rate(
            scenario="s", n=30, critical=20, p=0.6, trials=1000,
            stream=self.stream, trial_budget=40)
        self.assertEqual(est.status, "interrupted")
        self.assertEqual(est.trials, 40)
        # The pending range is the interval: wide, not a point.
        self.assertGreater(est.ci_high - est.ci_low, 0.1)

    def test_empirical_ci_covers_exact_reference(self):
        # Deterministic given the fixed seed; n_trials large enough that the
        # exact power sits comfortably inside the interval.
        stream = spawn_streams(2024, ["cover"])["cover"]
        ref = exact_power(50, 0.5, 0.65, 0.05)
        est = simulate_rejection_rate(
            scenario="cover", n=50, critical=ref.critical, p=0.65,
            trials=5000, stream=stream, exact=ref.power)
        self.assertLessEqual(est.ci_low, est.exact_power)
        self.assertGreaterEqual(est.ci_high, est.exact_power)


class TestRunScenarios(unittest.TestCase):
    def test_independent_reproducible_streams(self):
        first = run_scenarios(SCENARIOS, master_seed=11, trials=400)
        second = run_scenarios(SCENARIOS, master_seed=11, trials=400)
        self.assertEqual(first, second)
        seeds = {est.seed for est in first.values()}
        self.assertEqual(len(seeds), len(SCENARIOS))  # distinct streams

    def test_scenario_result_invariant_to_siblings(self):
        alone = run_scenarios([SCENARIOS[1]], master_seed=11, trials=400)["weak"]
        together = run_scenarios(SCENARIOS, master_seed=11, trials=400)["weak"]
        self.assertEqual(alone, together)

    def test_exact_and_empirical_labeled_separately(self):
        est = run_scenarios(SCENARIOS, master_seed=3, trials=200)["null"]
        self.assertEqual(est.kind, "empirical")
        self.assertIsNotNone(est.exact_power)
        # Null scenario: exact power equals exact size <= alpha.
        self.assertLessEqual(est.exact_power, 0.05)


if __name__ == "__main__":
    unittest.main()
