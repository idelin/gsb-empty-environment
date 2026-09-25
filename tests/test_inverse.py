"""Inverse queries: monotone points, non-monotone multi-solutions,
whole-segment platforms, interval targets, and error-band intervals."""

from __future__ import annotations

from pwlin import CurveTable, Measurement, Node

from tests.util import PwlinTestCase


def exact_table(pairs):
    table = CurveTable(["a"])
    for x, v in pairs:
        table.insert(Node(x=x, version=1, priority=0,
                          values={"a": Measurement.exact(v)}))
    return table


class InverseTest(PwlinTestCase):
    def test_monotone_point_solution(self):
        table = exact_table([(0.0, 0.0), (2.0, 2.0)])
        ev = table.invert("a", 0.5)
        self.assertEqual(len(ev.solutions), 1)
        sol = ev.solutions[0]
        self.assertFalse(sol.is_interval)
        self.assertAlmostEqual(sol.x_lo, 0.5)
        self.assertEqual([n.x for n in sol.sources], [0.0, 2.0])

    def test_non_monotone_multiple_solutions(self):
        table = exact_table([(0.0, 0.0), (1.0, 2.0), (2.0, 0.0), (3.0, 2.0)])
        ev = table.invert("a", 1.0)
        xs = [s.x_lo for s in ev.solutions]
        self.assertEqual(len(xs), 3)
        for got, want in zip(xs, [0.5, 1.5, 2.5]):
            self.assertAlmostEqual(got, want)

    def test_platform_returns_whole_interval(self):
        table = exact_table([(0.0, 1.0), (1.0, 1.0), (2.0, 0.0)])
        ev = table.invert("a", 1.0)
        self.assertEqual(len(ev.solutions), 1)
        sol = ev.solutions[0]
        self.assertTrue(sol.is_interval)
        self.assertAlmostEqual(sol.x_lo, 0.0)
        self.assertAlmostEqual(sol.x_hi, 1.0)

    def test_multi_segment_platform_merges(self):
        table = exact_table([(0.0, 5.0), (1.0, 5.0), (2.0, 5.0), (3.0, 0.0)])
        ev = table.invert("a", 5.0)
        self.assertEqual(len(ev.solutions), 1)
        self.assertAlmostEqual(ev.solutions[0].x_lo, 0.0)
        self.assertAlmostEqual(ev.solutions[0].x_hi, 2.0)

    def test_error_band_widens_solution_to_interval(self):
        table = CurveTable(["a"])
        table.insert(Node(x=0.0, version=1, priority=0,
                          values={"a": Measurement(0.0, 0.4)}))
        table.insert(Node(x=2.0, version=1, priority=0,
                          values={"a": Measurement(2.0, 2.4)}))
        ev = table.invert("a", 1.0)
        self.assertEqual(len(ev.solutions), 1)
        sol = ev.solutions[0]
        self.assertTrue(sol.is_interval)
        self.assertAlmostEqual(sol.x_lo, 0.6)   # hi(x) >= 1 -> x >= 0.6
        self.assertAlmostEqual(sol.x_hi, 1.0)   # lo(x) <= 1 -> x <= 1.0

    def test_interval_target(self):
        table = exact_table([(0.0, 0.0), (2.0, 2.0)])
        ev = table.invert("a", (0.9, 1.1))
        self.assertEqual(len(ev.solutions), 1)
        self.assertAlmostEqual(ev.solutions[0].x_lo, 0.9)
        self.assertAlmostEqual(ev.solutions[0].x_hi, 1.1)

    def test_no_solution(self):
        table = exact_table([(0.0, 0.0), (2.0, 2.0)])
        ev = table.invert("a", 5.0)
        self.assertEqual(ev.solutions, ())

    def test_single_node_inverse(self):
        table = exact_table([(1.5, 3.0)])
        ev = table.invert("a", 3.0)
        self.assertEqual(len(ev.solutions), 1)
        self.assertAlmostEqual(ev.solutions[0].x_lo, 1.5)
        self.assertEqual(table.invert("a", 4.0).solutions, ())
