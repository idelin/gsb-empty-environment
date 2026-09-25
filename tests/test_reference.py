"""Cross-checks against the self-written dense reference:

* enumerate small node sets and compare point queries exactly and inverse
  queries against dense sampling;
* sample "truths" inside the measurement error intervals and verify the
  reported intervals always contain them (values and inverse solutions).
"""

from __future__ import annotations

import itertools
import random

from pwlin import CurveTable, Measurement, Node
from pwlin.reference import dense_evaluate, dense_invert

from tests.util import PwlinTestCase

TOL = 1e-6


def build_channel(xs, los, his):
    table = CurveTable(["a"])
    for x, lo, hi in zip(xs, los, his):
        table.insert(Node(x=float(x), version=1, priority=0,
                          values={"a": Measurement(float(lo), float(hi))}))
    return table


def small_node_sets():
    """Exhaustive small enumeration: subsets of positions x values x widths."""
    positions = (0.0, 1.0, 2.0, 3.0)
    levels = (0.0, 1.0, 2.0)
    widths = (0.0, 0.5)
    for n in (2, 3):
        for xs in itertools.combinations(positions, n):
            for vals in itertools.product(levels, repeat=n):
                for ws in itertools.product(widths, repeat=n):
                    los = [v - w for v, w in zip(vals, ws)]
                    his = [v + w for v, w in zip(vals, ws)]
                    yield list(xs), los, his


class EnumeratedComparisonTest(PwlinTestCase):
    def test_evaluate_matches_dense_reference(self):
        checked = 0
        for xs, los, his in small_node_sets():
            n = len(xs)
            table = build_channel(xs, los, his)
            probes = list(xs) + [(a + b) / 2 for a, b in zip(xs, xs[1:])]
            for x in probes:
                want_lo, want_hi = dense_evaluate(xs, los, his, n, x)
                got = table.evaluate("a", x).value
                self.assertAlmostEqual(got.lo, want_lo, places=12)
                self.assertAlmostEqual(got.hi, want_hi, places=12)
                checked += 1
        self.assertGreater(checked, 1000)  # the enumeration is not vacuous

    def test_invert_matches_dense_reference(self):
        checked = 0
        for xs, los, his in small_node_sets():
            n = len(xs)
            table = build_channel(xs, los, his)
            targets = {min(los), max(his), 0.5 * (min(los) + max(his)), los[0]}
            for y in sorted(targets):
                solutions = table.invert("a", y).solutions
                dense = dense_invert(xs, los, his, n, y, y, 150)
                # every dense interval is covered by some indexed solution
                for a, b in dense:
                    self.assertTrue(
                        any(s.x_lo <= a + TOL and s.x_hi >= b - TOL
                            for s in solutions),
                        f"dense ({a}, {b}) uncovered; xs={xs} y={y}")
                for s in solutions:
                    if s.x_hi - s.x_lo > 1e-9:
                        # positive-width solutions are found by dense sampling
                        self.assertTrue(
                            any(a <= s.x_lo + TOL and b >= s.x_hi - TOL
                                for a, b in dense),
                            f"solution ({s.x_lo}, {s.x_hi}) missing; xs={xs} y={y}")
                    else:
                        # point solution: must genuinely satisfy the predicate
                        lo, hi = dense_evaluate(xs, los, his, n, s.x_lo)
                        self.assertLessEqual(lo, y + TOL)
                        self.assertLessEqual(y, hi + TOL)
                checked += 1
        self.assertGreater(checked, 1000)


class TruthContainmentTest(PwlinTestCase):
    """Any truth sampled inside the error intervals must stay contained."""

    def random_tables(self, count=30):
        rng = random.Random(9917)
        for _ in range(count):
            table = CurveTable(["a", "b"])
            for x in sorted(rng.sample(range(10), rng.randint(2, 6))):
                values = {}
                for channel in ("a", "b"):
                    if rng.random() < 0.8:
                        v = rng.uniform(-5, 5)
                        w = rng.choice([0.0, rng.uniform(0, 1)])
                        values[channel] = Measurement(v - w, v + w)
                if values:
                    table.insert(Node(x=float(x), version=1, priority=0,
                                      values=values))
            yield table, rng

    def test_sampled_truths_are_contained(self):
        for table, rng in self.random_tables():
            for channel in ("a", "b"):
                xs = table.breakpoints(channel)
                if not xs:
                    continue
                nodes = [table.active_node(x) for x in xs]
                meas = [nd.values[channel] for nd in nodes]
                truth = [rng.uniform(m.lo, m.hi) for m in meas]
                # value queries contain the sampled truth
                for _ in range(30):
                    x = rng.uniform(xs[0], xs[-1])
                    lo_t, _ = dense_evaluate(list(xs), truth, truth,
                                             len(xs), x)
                    ev = table.evaluate(channel, x)
                    self.assertTrue(
                        ev.value.contains(lo_t, tol=1e-9),
                        f"truth {lo_t} outside {ev.value} at x={x}")
                # inverse queries contain every true solution
                for _ in range(10):
                    y = rng.uniform(min(m.lo for m in meas),
                                    max(m.hi for m in meas))
                    solutions = table.invert(channel, y).solutions
                    for i in range(len(xs) - 1):
                        v0, v1 = truth[i], truth[i + 1]
                        x0, x1 = xs[i], xs[i + 1]
                        if v0 == v1:
                            if v0 == y:  # true platform: sample inside it
                                for k in range(5):
                                    x = x0 + (x1 - x0) * k / 4
                                    self.assertTrue(
                                        any(s.contains(x, tol=1e-6)
                                            for s in solutions),
                                        f"platform x={x} uncovered")
                        elif (y - v0) * (y - v1) <= 0:
                            x = x0 + (y - v0) * (x1 - x0) / (v1 - v0)
                            self.assertTrue(
                                any(s.contains(x, tol=1e-6) for s in solutions),
                                f"true inverse x={x} uncovered for y={y}")
