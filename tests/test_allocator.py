import unittest
from fractions import Fraction

from wmmf import Demand, water_fill


class TestWaterFill(unittest.TestCase):
    def test_equal_split_single_link(self):
        alloc = water_fill(
            {"A": 10},
            {
                "d1": Demand("d1", weight=1, cap=10, links=("A",)),
                "d2": Demand("d2", weight=1, cap=10, links=("A",)),
            },
        )
        self.assertEqual(alloc.rates["d1"], Fraction(5))
        self.assertEqual(alloc.rates["d2"], Fraction(5))
        self.assertEqual(alloc.freeze_order(), [("d1", "d2")])
        self.assertEqual(alloc.bottleneck_links(), ["A"])

    def test_weighted_split(self):
        alloc = water_fill(
            {"A": 10},
            {
                "d1": Demand("d1", weight=1, cap=10, links=("A",)),
                "d2": Demand("d2", weight=3, cap=30, links=("A",)),
            },
        )
        self.assertEqual(alloc.rates["d1"], Fraction(5, 2))
        self.assertEqual(alloc.rates["d2"], Fraction(15, 2))
        self.assertEqual(
            alloc.normalized_rate("d1"), alloc.normalized_rate("d2")
        )

    def test_cap_freezes_before_link_saturation(self):
        alloc = water_fill(
            {"A": 10},
            {
                "d1": Demand("d1", weight=1, cap=2, links=("A",)),
                "d2": Demand("d2", weight=1, cap=10, links=("A",)),
            },
        )
        self.assertEqual(alloc.rates["d1"], Fraction(2))
        self.assertEqual(alloc.rates["d2"], Fraction(8))
        # d1 第 0 轮因上限冻结，d2 第 1 轮因链路饱和冻结
        self.assertEqual(alloc.freeze_order(), [("d1",), ("d2",)])
        self.assertEqual(alloc.events[0].capped, ("d1",))
        self.assertEqual(alloc.events[0].bottleneck_links, ())
        self.assertEqual(alloc.events[1].bottleneck_links, ("A",))

    def test_capped_demand_gets_no_more(self):
        alloc = water_fill(
            {"A": 100},
            {"d1": Demand("d1", weight=1, cap=3, links=("A",))},
        )
        self.assertEqual(alloc.rates["d1"], Fraction(3))
        self.assertTrue(alloc.is_satisfied("d1"))

    def test_multi_link_demand_not_double_counted(self):
        # d2 同时经过 A、B 两条链路：只获配一个速率，在每条链路上各占一次
        alloc = water_fill(
            {"A": 10, "B": 4},
            {
                "d1": Demand("d1", weight=1, cap=10, links=("A",)),
                "d2": Demand("d2", weight=1, cap=10, links=("A", "B")),
            },
        )
        self.assertEqual(alloc.rates["d2"], Fraction(4))   # 受 B 限制
        self.assertEqual(alloc.rates["d1"], Fraction(6))
        self.assertEqual(alloc.link_loads["A"], Fraction(10))  # 6 + 4，只算一次
        self.assertEqual(alloc.link_loads["B"], Fraction(4))
        self.assertEqual(alloc.events[0].bottleneck_links, ("B",))
        self.assertEqual(alloc.events[1].bottleneck_links, ("A",))

    def test_capacity_never_exceeded(self):
        alloc = water_fill(
            {"A": 7, "B": 5, "C": 3},
            {
                "d1": Demand("d1", weight=2, cap=9, links=("A", "B")),
                "d2": Demand("d2", weight=1, cap=9, links=("B", "C")),
                "d3": Demand("d3", weight=1, cap=9, links=("A",)),
                "d4": Demand("d4", weight=3, cap=9, links=("C",)),
            },
        )
        for link, load in alloc.link_loads.items():
            self.assertLessEqual(load, alloc.link_caps[link])
        for name, rate in alloc.rates.items():
            self.assertLessEqual(rate, alloc.demands[name].cap)
            self.assertGreaterEqual(rate, 0)

    def test_unknown_link_rejected(self):
        with self.assertRaises(ValueError):
            water_fill({"A": 1}, {"d": Demand("d", cap=1, links=("Z",))})

    def test_unbounded_rejected(self):
        with self.assertRaises(ValueError):
            water_fill({"A": 1}, {"d": Demand("d", cap=None, links=())})


if __name__ == "__main__":
    unittest.main()
