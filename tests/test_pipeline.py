import tempfile
import unittest
from fractions import Fraction

from noise import NoiseLedger
from pipeline import (BucketSchema, publish, reconcile, simple_release,
                      synthetic_counts)
from qp import Publication, check_kkt, solve_by_enumeration, solve_iterative
from pipeline import _publications


def make_schema():
    return BucketSchema(
        leaf_names=("a", "b", "c", "d"),
        groups=(("left", (0, 1)), ("right", (2, 3)), ("mid", (1, 2))),
    )


class TestPipeline(unittest.TestCase):
    def setUp(self):
        self.counts = synthetic_counts(num_users=40, n_buckets=4,
                                       max_contribution=2, seed=11)

    def test_end_to_end_overlapping(self):
        schema = make_schema()
        ledger = NoiseLedger(master_seed=5)
        release = publish(schema, self.counts, Fraction(1), ledger,
                          max_contribution=2, total_source="noised")
        res = reconcile(release)
        self.assertEqual(res["status"], "optimal")
        post = res["postprocessed"]
        self.assertTrue(all(v >= 0 for v in post))
        self.assertEqual(sum(post), release.total_value)
        self.assertEqual(res["kkt_violations"], [])
        # Raw noisy values kept separate and may be negative.
        self.assertIn("raw_noisy", res)
        self.assertNotEqual(list(res["raw_noisy"].values()), post)

    def test_repeated_release_reuses_noise(self):
        schema = make_schema()
        with tempfile.TemporaryDirectory() as tmp:
            path = f"{tmp}/ledger.json"
            r1 = publish(schema, self.counts, Fraction(1),
                         NoiseLedger(5, path), max_contribution=2)
            r2 = publish(schema, self.counts, Fraction(1),
                         NoiseLedger(5, path), max_contribution=2)
            noisy1 = {k: m["noisy"] for k, m in r1.measurements.items()}
            noisy2 = {k: m["noisy"] for k, m in r2.measurements.items()}
            self.assertEqual(noisy1, noisy2)
            self.assertTrue(all(m["reused"] for m in r2.measurements.values()))

    def test_infeasible_total_rejected(self):
        schema = BucketSchema(leaf_names=("a", "b"))
        ledger = NoiseLedger(master_seed=3)
        release = publish(schema, [4, 6], Fraction(1), ledger)
        release.total_value = -1  # simulate a badly noised total
        res = reconcile(release)
        self.assertEqual(res["status"], "rejected_infeasible")
        self.assertIsNone(res["postprocessed"])

    def test_iteration_budget_marks_not_converged(self):
        schema = make_schema()
        ledger = NoiseLedger(master_seed=8)
        release = publish(schema, self.counts, Fraction(1), ledger,
                          max_contribution=2)
        res = reconcile(release, max_iterations=0)
        self.assertEqual(res["status"], "not_converged")
        self.assertIsNone(res["postprocessed"])

    def test_pipeline_matches_enumeration_reference(self):
        schema = make_schema()
        ledger = NoiseLedger(master_seed=9)
        release = publish(schema, self.counts, Fraction(1), ledger,
                          max_contribution=2, total_source="noised")
        pubs = _publications(release)
        fast = solve_iterative(pubs, schema.n, release.total_value)
        ref = solve_by_enumeration(pubs, schema.n, release.total_value)
        self.assertEqual(fast.x, ref.x)
        self.assertEqual(check_kkt(pubs, schema.n, release.total_value,
                                   ref.x, ref.lam, ref.mu), [])

    def test_simple_release_zero_and_negative_noise(self):
        # Zero true counts plus negative noise must still project to a
        # non-negative vector summing to the total.
        with tempfile.TemporaryDirectory() as tmp:
            ledger = NoiseLedger(master_seed=13, path=f"{tmp}/l.json")
            res = simple_release([0, 0, 0, 12], Fraction(1), ledger)
            self.assertEqual(res["status"], "optimal")
            post = res["postprocessed"]
            self.assertTrue(all(v >= 0 for v in post))
            self.assertEqual(sum(post), res["total_target"])

    def test_public_total_source(self):
        schema = make_schema()
        ledger = NoiseLedger(master_seed=21)
        release = publish(schema, self.counts, Fraction(1), ledger,
                          max_contribution=2, total_source="public")
        self.assertEqual(release.total_value, sum(self.counts))
        self.assertNotIn("total", release.measurements)
        res = reconcile(release)
        self.assertEqual(res["status"], "optimal")


if __name__ == "__main__":
    unittest.main()
