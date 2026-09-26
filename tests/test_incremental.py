import unittest
from fractions import Fraction

from wmmf import Demand, FairNetwork, check_feasible


def build_net():
    net = FairNetwork()
    net.add_link("A", 10)
    net.add_link("B", 6)
    net.add_demand(Demand("d1", weight=1, cap=10, links=("A",)))
    net.add_demand(Demand("d2", weight=1, cap=10, links=("A", "B")))
    return net


class TestIncremental(unittest.TestCase):
    def test_withdrawal_unfreezes_demand(self):
        net = build_net()
        first = net.allocate()
        self.assertEqual(first.rates["d2"], Fraction(5))  # 被 A 冻结在 5
        self.assertEqual(first.saturated_links(), frozenset({"A"}))

        net.withdraw_demand("d1")
        report = net.reallocate()
        # 解冻重算：d2 升到 B 的容量 6；只扣剩余量的做法会把它留在 5
        self.assertEqual(report.current.rates["d2"], Fraction(6))
        self.assertIn("d2", report.unfrozen)
        self.assertEqual(report.increased["d2"], (Fraction(5), Fraction(6)))
        self.assertTrue(report.saturation_set_changed)
        self.assertEqual(report.saturated_after, frozenset({"B"}))
        self.assertEqual(check_feasible(report.current), [])

    def test_link_failure_reallocates(self):
        net = build_net()
        net.allocate()
        net.fail_link("B")
        report = net.reallocate()
        self.assertEqual(report.current.rates["d2"], Fraction(0))
        self.assertEqual(report.current.rates["d1"], Fraction(10))
        self.assertEqual(check_feasible(report.current), [])

    def test_capacity_shrink_unfreezes_and_rebalances(self):
        net = FairNetwork()
        net.add_link("A", 10)
        net.add_demand(Demand("d1", weight=1, cap=10, links=("A",)))
        net.add_demand(Demand("d2", weight=2, cap=20, links=("A",)))
        first = net.allocate()
        self.assertEqual(first.rates["d1"], Fraction(10, 3))
        self.assertEqual(first.rates["d2"], Fraction(20, 3))

        net.set_link_capacity("A", 6)
        report = net.reallocate()
        self.assertEqual(report.current.rates["d1"], Fraction(2))
        self.assertEqual(report.current.rates["d2"], Fraction(4))
        self.assertIn("d1", report.decreased)
        self.assertEqual(check_feasible(report.current), [])

    def test_capped_demand_not_continued_after_change(self):
        net = FairNetwork()
        net.add_link("A", 10)
        net.add_demand(Demand("d1", weight=1, cap=3, links=("A",)))
        net.add_demand(Demand("d2", weight=1, cap=10, links=("A",)))
        net.allocate()
        net.withdraw_demand("d2")
        report = net.reallocate()
        # 已达上限者即使链路空闲也不再继续分配
        self.assertEqual(report.current.rates["d1"], Fraction(3))
        self.assertNotIn("d1", report.increased)

    def test_capacity_still_respected_after_failure(self):
        net = FairNetwork()
        net.add_link("A", 8)
        net.add_link("B", 8)
        net.add_demand(Demand("d1", weight=1, cap=8, links=("A",)))
        net.add_demand(Demand("d2", weight=1, cap=8, links=("A", "B")))
        net.allocate()
        net.fail_link("A")
        report = net.reallocate()
        for link, load in report.current.link_loads.items():
            self.assertLessEqual(load, report.current.link_caps[link])


if __name__ == "__main__":
    unittest.main()
