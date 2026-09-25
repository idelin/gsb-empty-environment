import unittest
from datetime import date
from fractions import Fraction

from temporal import (
    Duration,
    InfeasibleError,
    OffsetTable,
    TemporalNetwork,
    UnknownError,
    WallClock,
)


class FeasibilityTest(unittest.TestCase):
    def test_simple_feasible_bounds(self):
        net = TemporalNetwork()
        net.set_range("A", 0, 10, cid="rA")
        net.add_constraint("A", "B", lower=3, upper=5, cid="gAB")
        self.assertEqual(net.status, "FEASIBLE")
        self.assertEqual(net.bounds("A"), (Fraction(0), Fraction(10)))
        self.assertEqual(net.bounds("B"), (Fraction(3), Fraction(15)))
        sched = net.schedule()
        self.assertGreaterEqual(sched["B"] - sched["A"], 3)
        self.assertLessEqual(sched["B"] - sched["A"], 5)

    def test_required_infeasible_example(self):
        # B - A in [3, 5] together with A - B >= 0 is infeasible.
        net = TemporalNetwork()
        net.add_constraint("A", "B", lower=3, upper=5, cid="gapAB")
        net.add_constraint("B", "A", lower=0, cid="ordBA")
        self.assertEqual(net.status, "INFEASIBLE")
        nodes, cids, weight = net.contradiction_detail()
        self.assertLess(weight, 0)
        self.assertEqual(set(cids), {"gapAB", "ordBA"})
        self.assertEqual(set(net.contradiction()), {"gapAB", "ordBA"})
        with self.assertRaises(InfeasibleError):
            net.schedule()
        with self.assertRaises(InfeasibleError):
            net.bounds("A")

    def test_exact_rational_conflict_no_float_tolerance(self):
        # Three gaps of exactly 1/3 around a cycle sum to 1 > 0.  Exact
        # rational arithmetic catches this; float epsilons could mask it.
        net = TemporalNetwork()
        third = Fraction(1, 3)
        net.add_constraint("A", "B", lower=third)
        net.add_constraint("B", "C", lower=third)
        net.add_constraint("C", "A", lower=third)
        self.assertEqual(net.status, "INFEASIBLE")
        _nodes, _cids, weight = net.contradiction_detail()
        self.assertEqual(weight, Fraction(-1))

    def test_lower_above_upper_is_infeasible(self):
        net = TemporalNetwork()
        net.add_constraint("A", "B", lower=5, upper=3, cid="bad")
        self.assertEqual(net.status, "INFEASIBLE")
        self.assertEqual(set(net.contradiction()), {"bad"})
        _nodes, _cids, weight = net.contradiction_detail()
        self.assertEqual(weight, Fraction(-2))


class PushPopTest(unittest.TestCase):
    def test_pop_relaxes_unsupported_bounds(self):
        net = TemporalNetwork()
        net.set_range("A", 0, 10, cid="rA")
        net.add_constraint("A", "B", lower=3, upper=5, cid="gAB")
        base_bounds = net.bounds("B")
        self.assertEqual(base_bounds, (Fraction(3), Fraction(15)))
        base_version = net.version

        net.push()
        net.add_constraint("A", "B", upper=4, cid="assume")
        self.assertEqual(net.bounds("B"), (Fraction(3), Fraction(14)))

        removed = net.pop()
        self.assertEqual(removed, ["assume"])
        # the tightened bound lost its support and must be relaxed again
        self.assertEqual(net.bounds("B"), base_bounds)
        # history versions are queryable
        self.assertEqual(net.bounds_at(base_version)["B"], base_bounds)
        versions = [h.version for h in net.history]
        self.assertEqual(versions, sorted(versions))
        self.assertEqual(len(set(versions)), len(versions))

    def test_pop_restores_feasibility(self):
        net = TemporalNetwork()
        net.add_constraint("A", "B", lower=3, upper=5, cid="gAB")
        net.push()
        net.add_constraint("B", "A", lower=0, cid="killer")
        self.assertEqual(net.status, "INFEASIBLE")
        net.pop()
        self.assertEqual(net.status, "FEASIBLE")
        self.assertIsNone(net.contradiction())

    def test_cannot_pop_base_layer(self):
        net = TemporalNetwork()
        with self.assertRaises(ValueError):
            net.pop()


class AnchorTest(unittest.TestCase):
    def test_uniform_shift_freedom_then_anchor(self):
        net = TemporalNetwork()
        net.add_constraint("A", "B", lower=3, upper=5, cid="gap")
        # no absolute anchor: uniform translation is free, bounds unbounded
        self.assertEqual(net.bounds("A"), (None, None))
        self.assertEqual(net.bounds("B"), (None, None))
        # ...but a concrete witness schedule still exists
        sched = net.schedule()
        self.assertGreaterEqual(sched["B"] - sched["A"], 3)
        # anchoring pins the shared translation degree of freedom
        net.set_range("A", 0, 0, cid="pin")
        self.assertEqual(net.bounds("A"), (Fraction(0), Fraction(0)))
        self.assertEqual(net.bounds("B"), (Fraction(3), Fraction(5)))


class UnconstrainedTest(unittest.TestCase):
    def test_unconstrained_events_reported_separately(self):
        net = TemporalNetwork()
        net.set_range("A", 0, 10)
        net.add_event("lonely")
        constrained, unconstrained = net.bounds_all()
        self.assertIn("A", constrained)
        self.assertNotIn("lonely", constrained)
        self.assertEqual(unconstrained, {"lonely": (None, None)})


class BudgetTest(unittest.TestCase):
    def test_budget_interruption_never_returns_pseudo_feasible(self):
        net = TemporalNetwork(budget=40)
        prev = "E0"
        net.add_event(prev)
        for i in range(1, 300):
            cur = f"E{i}"
            net.add_constraint(prev, cur, lower=1, upper=2)
            prev = cur
        # the long chain needs more relaxations than the budget allows
        self.assertEqual(net.status, "UNKNOWN")
        with self.assertRaises(UnknownError):
            net.schedule()
        with self.assertRaises(UnknownError):
            net.bounds("E10")
        self.assertIsNone(net.contradiction())

    def test_adequate_budget_finds_chain_feasible(self):
        net = TemporalNetwork(budget=10_000_000)
        prev = "E0"
        net.add_event(prev)
        net.set_range("E0", 0, 0)  # anchor the chain
        for i in range(1, 300):
            cur = f"E{i}"
            net.add_constraint(prev, cur, lower=1, upper=2)
            prev = cur
        self.assertEqual(net.status, "FEASIBLE")
        self.assertEqual(net.bounds("E299"), (Fraction(299), Fraction(598)))


class WallClockIntegrationTest(unittest.TestCase):
    def test_wall_clock_anchored_range(self):
        tab = OffsetTable()
        tab.define("UTC", Duration.hours(0))
        tab.derive("CST", "UTC", Duration.hours(8))
        start = tab.to_timepoint(WallClock(date(2026, 9, 25), Duration.hours(9), "CST"))
        net = TemporalNetwork()
        t0 = start.offset.as_fraction
        net.set_range("meeting", t0, t0 + 3600, cid="meeting-window")
        net.add_constraint("meeting", "standup", lower=900, upper=1800, cid="gap")
        lo, hi = net.bounds("standup")
        self.assertEqual(lo, t0 + 900)
        self.assertEqual(hi, t0 + 3600 + 1800)


if __name__ == "__main__":
    unittest.main()
