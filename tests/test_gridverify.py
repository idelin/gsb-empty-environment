import unittest
from fractions import Fraction

from wmmf import (
    Demand,
    Network,
    Status,
    allocate,
    suggest_step,
    verify_feasible,
    verify_optimal,
)


def two_demand_network():
    return Network(
        {"L1": 4},
        {"A": Demand("A", 1, 4, {"L1"}), "B": Demand("B", 1, 4, {"L1"})},
    )


class TestGridVerify(unittest.TestCase):
    def test_optimal_on_small_grid(self):
        net = two_demand_network()
        alloc = allocate(net)
        self.assertEqual(alloc.rates, {"A": Fraction(2), "B": Fraction(2)})
        step = suggest_step(net, alloc.rates)
        self.assertEqual(step, Fraction(1))
        result = verify_optimal(net, alloc.rates, step=step, budget=100_000)
        self.assertEqual(result.status, Status.OPTIMAL)
        self.assertEqual(result.candidate_key, result.best_grid_key)

    def test_weighted_optimal_on_grid(self):
        net = Network(
            {"L1": 3},
            {"A": Demand("A", 1, 3, {"L1"}), "B": Demand("B", 2, 3, {"L1"})},
        )
        alloc = allocate(net)
        self.assertEqual(alloc.rates, {"A": Fraction(1), "B": Fraction(2)})
        result = verify_optimal(net, alloc.rates, step=Fraction(1), budget=100_000)
        self.assertEqual(result.status, Status.OPTIMAL)

    def test_infeasible_candidate(self):
        net = two_demand_network()
        bad = {"A": Fraction(3), "B": Fraction(3)}  # load 6 > capacity 4
        self.assertFalse(verify_feasible(net, bad))
        result = verify_optimal(net, bad, step=Fraction(1), budget=100_000)
        self.assertEqual(result.status, Status.INFEASIBLE)

    def test_not_optimal_candidate(self):
        net = two_demand_network()
        suboptimal = {"A": Fraction(1), "B": Fraction(3)}
        self.assertTrue(verify_feasible(net, suboptimal))
        result = verify_optimal(net, suboptimal, step=Fraction(1), budget=100_000)
        self.assertEqual(result.status, Status.NOT_OPTIMAL)
        self.assertEqual(result.best_grid_key, (Fraction(2), Fraction(2)))
        self.assertIsNotNone(result.witness)

    def test_budget_undecided(self):
        net = two_demand_network()
        alloc = allocate(net)
        result = verify_optimal(net, alloc.rates, step=Fraction(1), budget=1)
        self.assertEqual(result.status, Status.BUDGET_UNDECIDED)

    def test_fractional_grid_step(self):
        net = Network(
            {"L1": Fraction(1, 2)},
            {
                "A": Demand("A", 1, 1, {"L1"}),
                "B": Demand("B", 1, 1, {"L1"}),
            },
        )
        alloc = allocate(net)
        self.assertEqual(alloc.rates, {"A": Fraction(1, 4), "B": Fraction(1, 4)})
        step = suggest_step(net, alloc.rates)
        self.assertEqual(step, Fraction(1, 4))
        result = verify_optimal(net, alloc.rates, step=step, budget=100_000)
        self.assertEqual(result.status, Status.OPTIMAL)

    def test_multi_link_grid_verification(self):
        net = Network(
            {"L1": 10, "L2": 4},
            {
                "A": Demand("A", 1, 100, {"L1"}),
                "B": Demand("B", 1, 100, {"L1", "L2"}),
                "C": Demand("C", 1, 100, {"L2"}),
            },
        )
        alloc = allocate(net)
        step = suggest_step(net, alloc.rates)
        result = verify_optimal(net, alloc.rates, step=step, budget=2_000_000)
        self.assertEqual(result.status, Status.OPTIMAL)
        self.assertEqual(
            result.candidate_key, (Fraction(2), Fraction(2), Fraction(8))
        )


if __name__ == "__main__":
    unittest.main()
