"""Point-query semantics: exact endpoints, no crossing breakpoints,
derivative intervals, priority rule, and unknown-never-zero."""

from __future__ import annotations

import math

from pwlin import (
    CurveTable,
    Measurement,
    Node,
    NoDataError,
    OutOfDomainError,
    UnknownChannelError,
)

from tests.util import PwlinTestCase


def node(x, values, version=1, priority=0):
    return Node(x=x, version=version, priority=priority,
                values={c: Measurement(*m) for c, m in values.items()})


class EvaluateTest(PwlinTestCase):
    def setUp(self):
        self.table = CurveTable(["a"])
        self.table.insert(node(0.0, {"a": (0.0, 0.2)}))
        self.table.insert(node(2.0, {"a": (2.0, 2.4)}))

    def test_endpoints_are_exact(self):
        ev0 = self.table.evaluate("a", 0.0)
        ev2 = self.table.evaluate("a", 2.0)
        self.assertIntervalAlmostEqual(ev0.value, 0.0, 0.2)
        self.assertIntervalAlmostEqual(ev2.value, 2.0, 2.4)
        self.assertTrue(ev0.at_breakpoint)
        self.assertEqual(len(ev0.sources), 1)
        self.assertEqual(ev0.sources[0].x, 0.0)

    def test_interior_value_and_derivative(self):
        ev = self.table.evaluate("a", 1.0)
        self.assertIntervalAlmostEqual(ev.value, 1.0, 1.3)
        self.assertIntervalAlmostEqual(ev.derivative, 1.0, 1.1)
        self.assertFalse(ev.at_breakpoint)
        self.assertEqual([n.x for n in ev.sources], [0.0, 2.0])

    def test_out_of_domain_raises(self):
        with self.assertRaises(OutOfDomainError):
            self.table.evaluate("a", -0.5)
        with self.assertRaises(OutOfDomainError):
            self.table.evaluate("a", 2.5)

    def test_unknown_channel_raises(self):
        with self.assertRaises(UnknownChannelError):
            self.table.evaluate("nope", 1.0)

    def test_empty_channel_raises_not_zero(self):
        empty = CurveTable(["a"])
        with self.assertRaises(NoDataError):
            empty.evaluate("a", 0.0)
        with self.assertRaises(NoDataError):
            empty.invert("a", 0.0)


class BreakpointTest(PwlinTestCase):
    def setUp(self):
        # tent: 0 -> 10 -> 0 with exact measurements
        self.table = CurveTable(["a"])
        for x, v in [(0.0, 0.0), (1.0, 10.0), (2.0, 0.0)]:
            self.table.insert(node(x, {"a": (v, v)}))

    def test_derivative_hull_at_breakpoint(self):
        ev = self.table.evaluate("a", 1.0)
        self.assertIntervalAlmostEqual(ev.left_derivative, 10.0, 10.0)
        self.assertIntervalAlmostEqual(ev.right_derivative, -10.0, -10.0)
        self.assertIntervalAlmostEqual(ev.derivative, -10.0, 10.0)

    def test_interpolation_does_not_cross_breakpoint(self):
        # just left/right of the peak only the adjacent segment is used
        self.assertIntervalAlmostEqual(self.table.evaluate("a", 0.5).value, 5.0, 5.0)
        self.assertIntervalAlmostEqual(self.table.evaluate("a", 1.5).value, 5.0, 5.0)
        self.assertIntervalAlmostEqual(self.table.evaluate("a", 1.0).value, 10.0, 10.0)

    def test_single_node_derivative_unknown(self):
        table = CurveTable(["a"])
        table.insert(node(1.0, {"a": (3.0, 3.5)}))
        ev = table.evaluate("a", 1.0)
        self.assertEqual(ev.derivative.lo, -math.inf)
        self.assertEqual(ev.derivative.hi, math.inf)


class PriorityRuleTest(PwlinTestCase):
    def setUp(self):
        self.table = CurveTable(["a"])

    def active_value(self):
        return self.table.active_node(1.0).values["a"].lo

    def test_higher_version_wins_at_equal_priority(self):
        self.table.insert(node(1.0, {"a": (10.0, 10.0)}, version=1))
        self.table.insert(node(1.0, {"a": (20.0, 20.0)}, version=2))
        self.assertEqual(self.active_value(), 20.0)

    def test_higher_priority_beats_higher_version(self):
        self.table.insert(node(1.0, {"a": (20.0, 20.0)}, version=9, priority=0))
        self.table.insert(node(1.0, {"a": (99.0, 99.0)}, version=1, priority=5))
        self.assertEqual(self.active_value(), 99.0)

    def test_losing_version_is_not_visible_change(self):
        self.table.insert(node(1.0, {"a": (99.0, 99.0)}, version=1, priority=5))
        epoch = self.table.epoch
        changed = self.table.insert(node(1.0, {"a": (1.0, 1.0)}, version=1, priority=0))
        self.assertFalse(changed)
        self.assertEqual(self.table.epoch, epoch)
        self.assertEqual(self.active_value(), 99.0)

    def test_same_key_reinsert_replaces(self):
        self.table.insert(node(1.0, {"a": (10.0, 10.0)}, version=2, priority=0))
        self.table.insert(node(1.0, {"a": (11.0, 11.0)}, version=2, priority=0))
        self.assertEqual(self.active_value(), 11.0)


class UnknownNeverZeroTest(PwlinTestCase):
    def test_missing_channel_excluded_not_zero(self):
        table = CurveTable(["a", "b"])
        table.insert(node(0.0, {"a": (1.0, 1.0)}))
        table.insert(node(2.0, {"a": (3.0, 3.0), "b": (7.0, 7.0)}))
        # channel b only has the x=2 breakpoint; x=0 is unknown, not 0.0
        self.assertEqual(table.breakpoints("b"), (2.0,))
        with self.assertRaises(OutOfDomainError):
            table.evaluate("b", 0.0)
        ev = table.evaluate("b", 2.0)
        self.assertIntervalAlmostEqual(ev.value, 7.0, 7.0)

    def test_node_with_undeclared_channel_rejected(self):
        table = CurveTable(["a"])
        with self.assertRaises(UnknownChannelError):
            table.insert(node(0.0, {"ghost": (0.0, 0.0)}))
