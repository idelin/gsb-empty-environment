import os
import sys
import unittest
from fractions import Fraction

sys.path.insert(0, os.path.dirname(os.path.dirname(os.path.abspath(__file__))))

from monitor import (
    STATUS_EMPTY_BASELINE,
    STATUS_EMPTY_CLASS,
    STATUS_OK,
    DistributionMonitor,
)

SUPPORT = ["a", "b", "c", "d"]


class MonitorBasicTests(unittest.TestCase):
    def test_evaluation_counts_and_bases(self):
        monitor = DistributionMonitor(SUPPORT)
        for record_id, key, value in [
            ("r1", "x", "a"), ("r2", "x", "b"),
            ("r3", "y", "c"), ("r4", "y", "d"),
        ]:
            monitor.add_record(record_id, key, value)
        evaluation = monitor.evaluate("x")
        self.assertEqual(evaluation.status, STATUS_OK)
        self.assertEqual(evaluation.n_class, 2)
        self.assertEqual(evaluation.n_baseline, 4)
        self.assertEqual(monitor.baseline_version, 4)
        self.assertIsInstance(evaluation.tv, Fraction)
        self.assertEqual(
            sum(evaluation.tv_bucket_contribs.values(), Fraction(0)),
            evaluation.tv,
        )

    def test_identical_class_and_baseline_are_zero(self):
        monitor = DistributionMonitor(SUPPORT)
        for i, value in enumerate(["a", "b", "c"]):
            monitor.add_record(f"b{i}", "base", value)
        monitor.add_record("x0", "x", "a")
        monitor.add_record("x1", "x", "b")
        monitor.add_record("x2", "x", "c")
        evaluation = monitor.evaluate("x")
        self.assertEqual(evaluation.tv, 0)
        self.assertEqual(evaluation.emd, 0)

    def test_withdrawal_updates_counts_and_versions(self):
        monitor = DistributionMonitor(SUPPORT)
        monitor.add_record("r1", "x", "a")
        monitor.add_record("r2", "x", "a")
        before = monitor.evaluate("x")
        self.assertEqual(before.baseline_version, 2)
        monitor.withdraw_record("r2")
        self.assertEqual(monitor.class_counts("x"), [1, 0, 0, 0])
        self.assertEqual(monitor.baseline_counts, [1, 0, 0, 0])
        self.assertEqual(monitor.stale_classes(), ["x"])
        after = monitor.evaluate("x")
        self.assertEqual(after.tv, 0)
        self.assertEqual(after.emd, 0)
        self.assertEqual(monitor.stale_classes(), [])

    def test_baseline_change_forces_all_classes_to_be_stale(self):
        monitor = DistributionMonitor(SUPPORT)
        monitor.add_record("r1", "x", "a")
        monitor.add_record("r2", "y", "b")
        monitor.evaluate("x")
        monitor.evaluate("y")
        # Any withdrawal changes the overall baseline version.
        monitor.withdraw_record("r2")
        self.assertEqual(set(monitor.stale_classes()), {"x", "y"})
        monitor.evaluate_all()
        self.assertEqual(monitor.stale_classes(), [])

    def test_empty_class_after_withdrawal_is_skipped(self):
        monitor = DistributionMonitor(SUPPORT)
        monitor.add_record("r1", "x", "a")
        monitor.add_record("r2", "y", "b")
        monitor.withdraw_record("r1")
        evaluation = monitor.evaluate("x")
        self.assertEqual(evaluation.status, STATUS_EMPTY_CLASS)
        self.assertIsNone(evaluation.tv)
        self.assertIsNone(evaluation.emd)
        self.assertEqual(evaluation.n_class, 0)
        self.assertEqual(evaluation.n_baseline, 1)

    def test_empty_baseline_is_skipped(self):
        monitor = DistributionMonitor(SUPPORT)
        monitor.add_record("r1", "x", "a")
        monitor.withdraw_record("r1")
        evaluation = monitor.evaluate("x")
        self.assertEqual(evaluation.status, STATUS_EMPTY_BASELINE)
        self.assertIsNone(evaluation.tv)
        self.assertIsNone(evaluation.emd)

    def test_unknown_record_withdrawal_raises(self):
        monitor = DistributionMonitor(SUPPORT)
        with self.assertRaises(KeyError):
            monitor.withdraw_record("nope")

    def test_duplicate_record_id_rejected(self):
        monitor = DistributionMonitor(SUPPORT)
        monitor.add_record("r1", "x", "a")
        with self.assertRaises(ValueError):
            monitor.add_record("r1", "x", "b")


class ThresholdEvidenceTests(unittest.TestCase):
    def test_tv_breach_evidence(self):
        # x lives entirely in bucket a; baseline uniform over four buckets.
        monitor = DistributionMonitor(
            SUPPORT, tv_threshold=Fraction(1, 4)
        )
        monitor.add_record("x0", "x", "a")
        monitor.add_record("x1", "x", "a")
        monitor.add_record("b0", "base", "b")
        monitor.add_record("b1", "base", "c")
        monitor.add_record("b2", "base", "d")
        evaluation = monitor.evaluate("x")
        evidence = evaluation.tv_evidence
        self.assertTrue(evidence["exceeded"])
        self.assertEqual(evidence["threshold"], Fraction(1, 4))
        # The top bucket alone proves the breach: TV = 3/5 here.
        self.assertEqual(evidence["contributions"][0]["bucket"], "a")
        self.assertGreater(
            evidence["contributions"][0]["cumulative"], evidence["threshold"]
        )

    def test_no_breach_evidence_stays_below_threshold(self):
        monitor = DistributionMonitor(SUPPORT, tv_threshold=1)
        monitor.add_record("x0", "x", "a")
        monitor.add_record("b0", "base", "b")
        evaluation = monitor.evaluate("x")
        evidence = evaluation.tv_evidence
        self.assertFalse(evidence["exceeded"])
        self.assertLessEqual(
            evidence["contributions"][-1]["cumulative"], evidence["threshold"]
        )


class GeneralCostMatrixMonitorTests(unittest.TestCase):
    def test_monitor_uses_min_cost_flow_mode(self):
        squared = [[0, 1, 4], [1, 0, 1], [4, 1, 0]]
        monitor = DistributionMonitor(
            ["a", "b", "c"], cost_matrix=squared, emd_threshold=Fraction(1, 2)
        )
        monitor.add_record("x0", "x", "a")
        monitor.add_record("x1", "x", "a")
        monitor.add_record("b0", "base", "c")
        monitor.add_record("b1", "base", "c")
        evaluation = monitor.evaluate("x")
        self.assertEqual(evaluation.status, STATUS_OK)
        self.assertEqual(evaluation.emd_mode, "general_cost_matrix")
        # P = (1, 0, 0), Q = (1/2, 0, 1/2): half a unit crosses cost 4 -> 2.
        self.assertEqual(evaluation.emd, 2)
        self.assertTrue(evaluation.emd_evidence["exceeded"])
        self.assertGreater(
            sum(evaluation.emd_contribs.values(), Fraction(0)), 0
        )
        monitor.withdraw_record("b1")
        self.assertIn("x", monitor.stale_classes())


if __name__ == "__main__":
    unittest.main(verbosity=2)
