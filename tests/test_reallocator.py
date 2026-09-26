import unittest
from fractions import Fraction

from wmmf import Demand, Network, Reallocator, allocate, build_certificate, check_feasible, verify_certificate


def demo_network():
    return Network(
        capacities={"L1": 10, "L2": 4, "L3": 9, "L4": 5},
        demands={
            "A": Demand("A", 1, 100, {"L1"}),
            "B": Demand("B", 1, 100, {"L1", "L2"}),
            "C": Demand("C", 1, 100, {"L2"}),
            "D": Demand.from_paths("D", 2, 6, [["L3", "L4"], ["L4"]]),
        },
    )


class TestReallocator(unittest.TestCase):
    def assert_fair_and_feasible(self, net, alloc):
        self.assertEqual(check_feasible(net, alloc.rates), [])
        cert = build_certificate(net, alloc.rates)
        self.assertTrue(verify_certificate(net, alloc.rates, cert))

    def test_withdrawal_unfreezes_previously_frozen_demands(self):
        re = Reallocator(demo_network())
        self.assertEqual(re.allocation.rates["B"], Fraction(2))  # frozen by L2
        result = re.withdraw_demand("C")
        rates = result.allocation.rates
        # Saturated set changed: B is unfrozen and rises; A yields room.
        self.assertEqual(rates["B"], Fraction(4))
        self.assertEqual(rates["A"], Fraction(6))
        self.assertEqual(rates["D"], Fraction(5))
        self.assertEqual(result.changed["B"], (Fraction(2), Fraction(4)))
        self.assertEqual(result.changed["A"], (Fraction(8), Fraction(6)))
        self.assertNotIn("D", result.changed)
        self.assertEqual(result.withdrawn, ("C",))
        self.assertNotIn("C", rates)
        self.assert_fair_and_feasible(re.network, result.allocation)

    def test_naive_residual_deduction_would_be_wrong(self):
        # Continuing to deduct stale residuals keeps B frozen at 2 forever;
        # the correct incremental answer after C leaves is B = 4.
        re = Reallocator(demo_network())
        result = re.withdraw_demand("C")
        self.assertGreater(result.allocation.rates["B"], Fraction(2))

    def test_link_failure_zeroes_dependent_demand(self):
        re = Reallocator(demo_network())
        re.withdraw_demand("C")
        result = re.fail_link("L2")
        rates = result.allocation.rates
        self.assertEqual(rates["B"], Fraction(0))
        self.assertEqual(rates["A"], Fraction(10))
        self.assertEqual(rates["D"], Fraction(5))
        self.assertEqual(result.changed["B"], (Fraction(4), Fraction(0)))
        self.assert_fair_and_feasible(re.network, result.allocation)

    def test_capacity_recovery_raises_rates_again(self):
        re = Reallocator(demo_network())
        re.withdraw_demand("C")
        re.fail_link("L2")
        result = re.set_capacity("L2", 3)
        rates = result.allocation.rates
        self.assertEqual(rates["B"], Fraction(3))
        self.assertEqual(rates["A"], Fraction(7))
        self.assertEqual(rates["D"], Fraction(5))
        self.assert_fair_and_feasible(re.network, result.allocation)

    def test_capacity_reduction_stays_feasible(self):
        re = Reallocator(demo_network())
        result = re.set_capacity("L1", 5)
        rates = result.allocation.rates
        loads = re.network.link_loads(rates)
        self.assertLessEqual(loads["L1"], Fraction(5))
        self.assert_fair_and_feasible(re.network, result.allocation)

    def test_add_demand_reallocates(self):
        re = Reallocator(demo_network())
        result = re.add_demand(Demand("E", 1, 100, {"L1"}))
        rates = result.allocation.rates
        self.assertIn("E", rates)
        # L1=10 is now shared by A, B, E; L2=4 still caps B and C at 2.
        self.assertEqual(rates["B"], Fraction(2))
        self.assertEqual(rates["C"], Fraction(2))
        self.assertEqual(rates["A"], Fraction(4))
        self.assertEqual(rates["E"], Fraction(4))
        self.assert_fair_and_feasible(re.network, result.allocation)

    def test_unknown_operations_raise(self):
        re = Reallocator(demo_network())
        with self.assertRaises(KeyError):
            re.fail_link("nope")
        with self.assertRaises(KeyError):
            re.withdraw_demand("nope")
        with self.assertRaises(KeyError):
            re.set_capacity("nope", 1)


if __name__ == "__main__":
    unittest.main()
