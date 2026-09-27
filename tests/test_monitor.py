import unittest
from fractions import Fraction

from monitor import DistributionMonitor


class MonitorTvTest(unittest.TestCase):
    def setUp(self):
        self.mon = DistributionMonitor(metric="tv", threshold=Fraction(1, 4))
        # 合成数据：两个等价类，敏感值为疾病类别
        for v in ["flu", "flu", "cold"]:
            self.mon.add("c1", v)
        for v in ["flu", "cold", "cold"]:
            self.mon.add("c2", v)

    def test_incremental_matches_batch(self):
        ev = self.mon.evaluate("c1")
        # c1: flu 2/3, cold 1/3；总体: flu 1/2, cold 1/2
        self.assertEqual(ev.distance, Fraction(1, 6))
        self.assertEqual(ev.class_size, 3)
        self.assertEqual(ev.population_size, 6)
        self.assertTrue(ev.within_threshold)
        self.assertEqual(ev.margin, Fraction(1, 4) - Fraction(1, 6))

    def test_withdrawal_updates_both(self):
        self.mon.remove("c1", "flu")
        ev = self.mon.evaluate("c1")
        # c1: flu 1/2, cold 1/2；总体: flu 2/5, cold 3/5
        self.assertEqual(ev.distance, Fraction(1, 10))
        self.assertEqual(ev.class_size, 2)
        self.assertEqual(ev.population_size, 5)

    def test_overwithdrawal_rejected(self):
        with self.assertRaises(ValueError):
            self.mon.remove("c1", "flu", 99)
        with self.assertRaises(KeyError):
            self.mon.remove("nope", "flu")

    def test_version_change_makes_all_stale(self):
        self.mon.evaluate_all()
        self.assertEqual(self.mon.stale_classes(), [])
        self.mon.remove("c2", "cold")  # 只动 c2，但总体基线变了
        self.assertEqual(sorted(self.mon.stale_classes()), ["c1", "c2"])
        self.mon.evaluate_all()
        self.assertEqual(self.mon.stale_classes(), [])

    def test_pending_classes(self):
        self.mon.evaluate("c1")
        self.assertEqual(self.mon.pending_classes(), ["c2"])

    def test_empty_class_no_division_by_zero(self):
        for v in ["flu", "flu", "cold"]:
            self.mon.remove("c1", v)
        ev = self.mon.evaluate("c1")
        self.assertIsNone(ev.distance)
        self.assertIsNone(ev.within_threshold)
        self.assertIsNone(ev.margin)
        self.assertEqual(ev.class_size, 0)

    def test_threshold_evidence_exact(self):
        mon = DistributionMonitor(metric="tv", threshold=Fraction(1, 6))
        mon.add("c", "a"); mon.add("c", "a"); mon.add("c", "b")
        mon.add("d", "a"); mon.add("d", "b"); mon.add("d", "b")
        ev = mon.evaluate("c")
        self.assertEqual(ev.distance, Fraction(1, 6))
        self.assertTrue(ev.within_threshold)   # 距离恰等于阈值也算达标
        self.assertEqual(ev.margin, 0)


class MonitorOrderedTest(unittest.TestCase):
    def test_ordered_metric_with_flow(self):
        mon = DistributionMonitor(metric="ordered", ordered_values=[0, 1, 2, 3])
        mon.add("c", 0); mon.add("c", 2)
        mon.add("d", 1); mon.add("d", 3)
        ev = mon.evaluate("c")
        # c: {0:1/2, 2:1/2}；总体: {0,1,2,3 各 1/4} => W1 = 1/4 + 0 + 1/4
        self.assertEqual(ev.distance, Fraction(1, 2))
        self.assertIsNotNone(ev.flow)
        self.assertEqual(sum(e.amount for e in ev.flow), 1)
        # 区间贡献之和等于距离
        self.assertEqual(sum(i.contribution for i in ev.contributions), ev.distance)


class MonitorGeneralTest(unittest.TestCase):
    def test_general_metric_uses_min_cost_flow(self):
        costs = {("x", "x"): 0, ("x", "y"): 2, ("y", "x"): 5, ("y", "y"): 1}
        mon = DistributionMonitor(metric="general", cost_matrix=costs)
        mon.add("c", "x"); mon.add("c", "x"); mon.add("c", "y")
        mon.add("d", "y"); mon.add("d", "y"); mon.add("d", "y")
        ev = mon.evaluate("c")
        # c: {x:2, y:1}；总体: {x:2, y:4}。scale=18：
        # 最优 x->x 6 (0), x->y 6 (12), y->y 6 (6) => 18/18 = 1
        self.assertEqual(ev.distance, Fraction(1))


if __name__ == "__main__":
    unittest.main()
