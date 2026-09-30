import unittest
from fractions import Fraction

from dpproj import (ConsistencyConstraint, NoiseLedger, NoisedTarget,
                    PublicTarget, ReleaseConfig, run_release,
                    verify_release)
from dpproj.qp import InfeasibleError


def make_config(**overrides):
    base = dict(
        release_id="rel-1",
        buckets=("north", "south", "east", "west"),
        epsilon=Fraction(1),
        l1_sensitivity=Fraction(2),
        total=PublicTarget(Fraction(40)),
    )
    base.update(overrides)
    return ReleaseConfig(**base)


COUNTS = {"north": 12, "south": 8, "east": 15, "west": 5}


class TestRelease(unittest.TestCase):
    def test_release_ok_and_kkt_verifies(self):
        cfg = make_config()
        res = run_release(cfg, NoiseLedger(11), COUNTS)
        self.assertEqual(res.status, "ok")
        self.assertTrue(res.converged)
        self.assertTrue(verify_release(cfg, res))
        self.assertEqual(sum(res.projected.values()), 40)
        self.assertTrue(all(v >= 0 for v in res.projected.values()))

    def test_noisy_and_projected_kept_separate(self):
        cfg = make_config()
        res = run_release(cfg, NoiseLedger(11), COUNTS)
        self.assertEqual(set(res.noisy_counts), set(res.projected))
        # Noisy values are floats (raw mechanism output); projection is
        # exact rational post-processing.
        self.assertTrue(all(isinstance(v, float)
                            for v in res.noisy_counts.values()))
        self.assertTrue(all(isinstance(v, Fraction)
                            for v in res.projected.values()))

    def test_repeat_release_never_redraws_noise(self):
        cfg = make_config()
        ledger = NoiseLedger(11)
        res1 = run_release(cfg, ledger, COUNTS)
        draws_after_first = dict(ledger.draws)
        res2 = run_release(cfg, ledger, COUNTS)
        self.assertEqual(res1.noisy_counts, res2.noisy_counts)
        self.assertEqual(res1.projected, res2.projected)
        self.assertEqual(draws_after_first, ledger.draws)

    def test_full_recompute_from_seed(self):
        cfg = make_config()
        res1 = run_release(cfg, NoiseLedger(11), COUNTS)
        res2 = run_release(cfg, NoiseLedger(11), COUNTS)
        self.assertEqual(res1.noisy_counts, res2.noisy_counts)
        self.assertEqual(res1.projected, res2.projected)

    def test_negative_total_explicitly_rejected(self):
        cfg = make_config(total=PublicTarget(Fraction(-3)))
        with self.assertRaises(InfeasibleError):
            run_release(cfg, NoiseLedger(11), COUNTS)

    def test_noised_total_from_separate_source(self):
        cfg = make_config(total=NoisedTarget(epsilon=Fraction(1, 2)))
        ledger = NoiseLedger(11)
        res = run_release(cfg, ledger, COUNTS)
        # The total got its own ledger key, separate from bucket noise.
        self.assertIn("rel-1:total", ledger.draws)
        self.assertEqual(sum(res.projected.values()), res.total_target)
        self.assertTrue(res.converged)

    def test_iteration_budget_marks_not_converged(self):
        cfg = make_config(max_iterations=1)
        res = run_release(cfg, NoiseLedger(11), COUNTS)
        self.assertFalse(res.converged)
        self.assertEqual(res.status, "not_converged")
        self.assertIsNone(res.kkt)

    def test_zero_counts_with_negative_noise(self):
        cfg = make_config(total=PublicTarget(Fraction(1)))
        res = run_release(cfg, NoiseLedger(3),
                          {name: 0 for name in cfg.buckets})
        self.assertTrue(res.converged)
        self.assertEqual(sum(res.projected.values()), 1)
        self.assertTrue(all(v >= 0 for v in res.projected.values()))

    def test_overlapping_group_constraints(self):
        # Two overlapping region sums published with public targets.
        cons = (
            ConsistencyConstraint(
                coeffs={"north": Fraction(1), "south": Fraction(1)},
                target=PublicTarget(Fraction(20)), name="ns"),
            ConsistencyConstraint(
                coeffs={"south": Fraction(1), "east": Fraction(1)},
                target=PublicTarget(Fraction(23)), name="se"),
        )
        cfg = make_config(constraints=cons)
        res = run_release(cfg, NoiseLedger(11), COUNTS)
        self.assertTrue(res.converged)
        p = res.projected
        self.assertEqual(p["north"] + p["south"], 20)
        self.assertEqual(p["south"] + p["east"], 23)
        self.assertEqual(sum(p.values()), 40)
        self.assertTrue(verify_release(cfg, res))

    def test_counts_must_match_public_buckets(self):
        cfg = make_config()
        with self.assertRaises(ValueError):
            run_release(cfg, NoiseLedger(1), {"north": 1})


if __name__ == "__main__":
    unittest.main()
