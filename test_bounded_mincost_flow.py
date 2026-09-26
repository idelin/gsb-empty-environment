from fractions import Fraction
import unittest

from bounded_mincost_flow import Edge, FlowProblem, Status


def solve(edges, supplies, **kwargs):
    return FlowProblem(edges, supplies).solve(**kwargs)


class BoundedMinCostFlowTests(unittest.TestCase):
    def test_cheapest_route_is_selected(self):
        result = solve(
            [
                Edge("s", "a", 0, 2, 1, "sa"),
                Edge("s", "b", 0, 2, 3, "sb"),
                Edge("a", "t", 0, 2, 1, "at"),
                Edge("b", "t", 0, 2, 1, "bt"),
            ],
            {"s": 2, "t": -2},
        )
        self.assertEqual(result.status, Status.OPTIMAL)
        self.assertEqual(result.flow, {"sa": 2, "sb": 0, "at": 2, "bt": 0})
        self.assertEqual(result.cost, 4)

    def test_lower_bound_can_force_expensive_edge(self):
        result = solve(
            [
                Edge("s", "t", 0, 1, 1, "cheap"),
                Edge("s", "t", 1, 1, 10, "forced"),
            ],
            {"s": 2, "t": -2},
        )
        self.assertEqual(result.status, Status.OPTIMAL)
        self.assertEqual(result.flow, {"cheap": 1, "forced": 1})
        self.assertEqual(result.cost, 11)

    def test_negative_cycle_is_fully_canceled_up_to_capacity(self):
        result = solve(
            [
                Edge("s", "t", 0, 1, 10, "ship"),
                Edge("a", "b", 0, 2, -10, "ab"),
                Edge("b", "a", 0, 2, 6, "ba"),
            ],
            {"s": 1, "t": -1},
        )
        self.assertEqual(result.status, Status.OPTIMAL)
        self.assertEqual(result.flow["ship"], 1)
        self.assertEqual(result.flow["ab"], 2)
        self.assertEqual(result.flow["ba"], 2)
        self.assertEqual(result.cost, 2)
        self.assertEqual(result.cycles_canceled, 1)

    def test_negative_self_loop_uses_bounded_capacity(self):
        result = solve(
            [Edge("a", "a", 0, 2, -3, "loop")],
            {"a": 0},
        )
        self.assertEqual(result.status, Status.OPTIMAL)
        self.assertEqual(result.flow["loop"], 2)
        self.assertEqual(result.cost, -6)

    def test_budget_undecided_keeps_feasible_flow_and_witness(self):
        result = solve(
            [Edge("a", "a", 0, 2, -3, "loop")],
            {"a": 0},
            max_cycles=0,
        )
        self.assertEqual(result.status, Status.BUDGET_UNDECIDED)
        self.assertEqual(result.flow["loop"], 0)
        self.assertEqual(result.cycle, ("a", "a"))

    def test_feasible_without_optimization_skips_negative_cycle(self):
        result = solve(
            [Edge("a", "a", 0, 2, -3, "loop")],
            {"a": 0},
            prove_optimal=False,
        )
        self.assertEqual(result.status, Status.FEASIBLE)
        self.assertEqual(result.flow["loop"], 0)
        self.assertEqual(result.cost, 0)

    def test_capacity_cut_infeasibility_evidence(self):
        result = solve(
            [Edge("s", "t", 0, 1, 0, "narrow")],
            {"s": 2, "t": -2},
        )
        self.assertEqual(result.status, Status.INFEASIBLE)
        self.assertEqual(result.evidence.kind, "cut_certificate")
        cut = result.evidence.details["cut"]
        self.assertEqual(cut.reachable, ("s",))
        self.assertEqual(cut.unreachable, ("t",))
        self.assertEqual(cut.crossing_forward, (("s", "t", "narrow"),))
        self.assertEqual(cut.upper_bound_on_net_outflow, 1)
        self.assertEqual(cut.required_net_outflow, 2)
        self.assertEqual(cut.deficit, 1)

    def test_lower_bound_cut_infeasibility_evidence(self):
        result = solve(
            [Edge("s", "t", 3, 3, 0, "too_much")],
            {"s": 1, "t": -1},
        )
        self.assertEqual(result.status, Status.INFEASIBLE)
        self.assertEqual(result.evidence.kind, "cut_certificate")
        cut = result.evidence.details["cut"]
        self.assertIn("t", cut.reachable)
        self.assertNotIn("s", cut.reachable)
        self.assertEqual(cut.crossing_reverse, (("s", "t", "too_much"),))
        self.assertEqual(cut.upper_bound_on_net_outflow, -3)
        self.assertEqual(cut.required_net_outflow, 2)
        self.assertEqual(cut.deficit, 5)

    def test_supply_demand_mismatch_is_rejected(self):
        result = solve(
            [Edge("s", "t", 0, 5, 0, "e")],
            {"s": 3, "t": -2},
        )
        self.assertEqual(result.status, Status.INFEASIBLE)
        self.assertEqual(result.evidence.kind, "supply_demand_mismatch")
        self.assertEqual(result.evidence.details["difference"], 1)

    def test_rational_costs_are_exact(self):
        result = solve(
            [Edge("s", "t", 0, 1, Fraction(1, 3), "e")],
            {"s": 1, "t": -1},
        )
        self.assertEqual(result.status, Status.OPTIMAL)
        self.assertEqual(result.flow["e"], 1)
        self.assertEqual(result.cost, Fraction(1, 3))

    def test_rational_bounds_round_to_integer_feasible_values(self):
        result = solve(
            [Edge("s", "t", Fraction(1, 2), Fraction(3, 2), Fraction(1, 2), "e")],
            {"s": 1, "t": -1},
        )
        self.assertEqual(result.status, Status.OPTIMAL)
        self.assertEqual(result.flow["e"], 1)
        self.assertEqual(result.cost, Fraction(1, 2))

    def test_fractional_lower_bound_can_create_infeasible_cut(self):
        result = solve(
            [Edge("s", "t", Fraction(1, 2), Fraction(3, 2), 0, "e")],
            {"s": 0, "t": 0},
        )
        self.assertEqual(result.status, Status.INFEASIBLE)
        self.assertEqual(result.evidence.kind, "cut_certificate")
        cut = result.evidence.details["cut"]
        self.assertEqual(cut.upper_bound_on_net_outflow, -1)
        self.assertEqual(cut.required_net_outflow, 1)
        self.assertEqual(cut.deficit, 2)

    def test_fractional_supply_cannot_have_integer_flow(self):
        result = solve(
            [Edge("s", "t", 0, 1, 0, "e")],
            {"s": Fraction(1, 2), "t": Fraction(-1, 2)},
        )
        self.assertEqual(result.status, Status.INFEASIBLE)
        self.assertEqual(result.evidence.kind, "fractional_required_balance")

    def test_empty_integer_interval_is_infeasible(self):
        result = solve(
            [Edge("s", "t", Fraction(3, 2), Fraction(5, 4), 0, "e")],
            {"s": 0, "t": 0},
        )
        self.assertEqual(result.status, Status.INFEASIBLE)
        self.assertEqual(result.evidence.kind, "empty_integer_interval")

    def test_invalid_construction_raises_value_error(self):
        with self.assertRaises(ValueError):
            FlowProblem([Edge("s", "t", -1, 0, 0)], {})
        with self.assertRaises(ValueError):
            FlowProblem([Edge("s", "t", 0, 1, 0.5)], {})


if __name__ == "__main__":
    unittest.main(verbosity=2)
