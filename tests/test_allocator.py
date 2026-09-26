import unittest
from fractions import Fraction

from wmmf import Demand, Network, allocate


def make_demo_network():
    return Network(
        capacities={"L1": 10, "L2": 4, "L3": 9, "L4": 5},
        demands={
            "A": Demand("A", weight=1, cap=100, links={"L1"}),
            "B": Demand("B", weight=1, cap=100, links={"L1", "L2"}),
            "C": Demand("C", weight=1, cap=100, links={"L2"}),
            "D": Demand.from_paths("D", weight=2, cap=6, paths=[["L3", "L4"], ["L4"]]),
        },
    )


class TestWaterFilling(unittest.TestCase):
    def test_basic_two_demands_one_link(self):
        net = Network(
            {"L1": 10},
            {
                "A": Demand("A", 1, 100, {"L1"}),
                "B": Demand("B", 1, 100, {"L1"}),
            },
        )
        alloc = allocate(net)
        self.assertEqual(alloc.rates, {"A": Fraction(5), "B": Fraction(5)})

    def test_weights_split_proportionally(self):
        net = Network(
            {"L1": 9},
            {
                "A": Demand("A", 1, 100, {"L1"}),
                "B": Demand("B", 2, 100, {"L1"}),
            },
        )
        alloc = allocate(net)
        self.assertEqual(alloc.rates["A"], Fraction(3))
        self.assertEqual(alloc.rates["B"], Fraction(6))
        norms = alloc.normalized_rates(net)
        self.assertEqual(norms["A"], norms["B"])

    def test_capped_demand_stops_others_continue(self):
        net = Network(
            {"L1": 10},
            {
                "A": Demand("A", 1, 3, {"L1"}),
                "B": Demand("B", 1, 100, {"L1"}),
            },
        )
        alloc = allocate(net)
        self.assertEqual(alloc.rates["A"], Fraction(3))  # exactly at cap
        self.assertEqual(alloc.rates["B"], Fraction(7))  # takes the remainder
        self.assertIsNone(alloc.bottleneck_of["A"])  # stopped at cap, not a link
        self.assertEqual(alloc.bottleneck_of["B"], "L1")

    def test_freeze_order_and_bottlenecks(self):
        net = make_demo_network()
        alloc = allocate(net)
        self.assertEqual(
            alloc.rates,
            {"A": Fraction(8), "B": Fraction(2), "C": Fraction(2), "D": Fraction(5)},
        )
        events = alloc.freeze_order
        self.assertEqual(len(events), 3)
        self.assertEqual(events[0].demands, ("B", "C"))
        self.assertEqual(events[0].bottleneck_links, ("L2",))
        self.assertEqual(events[0].level, Fraction(2))
        self.assertEqual(events[1].demands, ("D",))
        self.assertEqual(events[1].bottleneck_links, ("L4",))
        self.assertEqual(events[2].demands, ("A",))
        self.assertEqual(events[2].bottleneck_links, ("L1",))
        self.assertEqual(alloc.bottleneck_of["B"], "L2")
        self.assertEqual(alloc.bottleneck_of["D"], "L4")
        self.assertEqual(alloc.bottleneck_of["A"], "L1")

    def test_multi_link_demand_counted_once_per_link(self):
        net = Network(
            {"L1": 5, "L2": 5},
            {"D": Demand("D", 1, 100, {"L1", "L2"})},
        )
        alloc = allocate(net)
        self.assertEqual(alloc.rates["D"], Fraction(5))
        loads = net.link_loads(alloc.rates)
        self.assertEqual(loads, {"L1": Fraction(5), "L2": Fraction(5)})

    def test_multi_path_shared_link_not_double_counted(self):
        # Two paths share L2; the rate must be charged once on L2.
        net = Network(
            {"L1": 5, "L2": 5, "L3": 5},
            {"D": Demand.from_paths("D", 1, 100, [["L1", "L2"], ["L2", "L3"]])},
        )
        alloc = allocate(net)
        self.assertEqual(alloc.rates["D"], Fraction(5))
        loads = net.link_loads(alloc.rates)
        self.assertEqual(loads["L2"], Fraction(5))  # not 10

    def test_rational_inputs_exact(self):
        net = Network(
            {"L1": Fraction(7, 2)},
            {
                "A": Demand("A", Fraction(1, 2), 10, {"L1"}),
                "B": Demand("B", Fraction(3, 2), 10, {"L1"}),
            },
        )
        alloc = allocate(net)
        self.assertEqual(alloc.rates["A"], Fraction(7, 8))
        self.assertEqual(alloc.rates["B"], Fraction(21, 8))
        norms = alloc.normalized_rates(net)
        self.assertEqual(norms["A"], norms["B"])
        self.assertEqual(norms["A"], Fraction(7, 4))

    def test_zero_capacity_link_freezes_at_zero(self):
        net = Network(
            {"L1": 10, "L2": 0},
            {
                "A": Demand("A", 1, 100, {"L1"}),
                "B": Demand("B", 1, 100, {"L1", "L2"}),
            },
        )
        alloc = allocate(net)
        self.assertEqual(alloc.rates["B"], Fraction(0))
        self.assertEqual(alloc.rates["A"], Fraction(10))

    def test_capacity_never_exceeded_and_caps_respected(self):
        scenarios = [
            make_demo_network(),
            Network(
                {"L1": 10},
                {"A": Demand("A", 1, 3, {"L1"}), "B": Demand("B", 2, 4, {"L1"})},
            ),
            Network(
                {"L1": Fraction(1, 3), "L2": Fraction(2, 3)},
                {
                    "A": Demand("A", 3, Fraction(1, 2), {"L1", "L2"}),
                    "B": Demand("B", 1, Fraction(5, 6), {"L2"}),
                },
            ),
        ]
        for net in scenarios:
            alloc = allocate(net)
            loads = net.link_loads(alloc.rates)
            for link, cap in net.capacities.items():
                self.assertLessEqual(loads[link], cap, f"link {link} overloaded")
            for demand_id, rate in alloc.rates.items():
                self.assertLessEqual(rate, net.demands[demand_id].cap)
                self.assertGreaterEqual(rate, 0)


if __name__ == "__main__":
    unittest.main()
