"""Segment index: updates are local and equal a full rebuild."""

from __future__ import annotations

import random

from pwlin import CurveTable, Measurement, Node

from tests.util import PwlinTestCase


def make_node(x, v, version=1):
    return Node(x=x, version=version, priority=0,
                values={"a": Measurement(v - 0.1, v + 0.1)})


def big_table(n=100):
    table = CurveTable(["a"])
    for i in range(n):
        table.insert(make_node(float(i), (i * 7) % 13 - 6.0))
    return table


class LocalityTest(PwlinTestCase):
    def test_insert_rebuilds_only_adjacent_segments(self):
        table = big_table()
        self.assertEqual(len(table.index.channel("a").segs), 99)
        table.index.rebuilt_segments = 0
        table.insert(make_node(50.5, 1.0))
        # 1 spanning segment removed + 2 new ones built
        self.assertLessEqual(table.index.rebuilt_segments, 3)

    def test_delete_rebuilds_only_adjacent_segments(self):
        table = big_table()
        table.index.rebuilt_segments = 0
        table.delete(50.0)
        # 2 segments removed + 1 merged segment built
        self.assertLessEqual(table.index.rebuilt_segments, 3)
        self.assertEqual(len(table.index.channel("a").segs), 98)

    def test_calibrate_rebuilds_two_segments(self):
        table = big_table()
        table.index.rebuilt_segments = 0
        table.calibrate(50.0, "a", 0.0, 0.5, version=2)
        self.assertEqual(table.index.rebuilt_segments, 2)


class ConsistencyTest(PwlinTestCase):
    def test_index_equals_full_rebuild_after_random_ops(self):
        rng = random.Random(20260925)
        table = CurveTable(["a"])
        for x in sorted(rng.sample(range(200), 40)):
            table.insert(make_node(float(x), rng.uniform(-5, 5)))
        for _ in range(15):
            xs = list(table._versions)
            table.delete(rng.choice(xs))
        for x in sorted(rng.sample(range(200), 15)):
            table.insert(make_node(float(x), rng.uniform(-5, 5), version=1))
        for _ in range(5):
            xs = list(table._versions)
            x = rng.choice(xs)
            v = rng.uniform(-5, 5)
            table.calibrate(x, "a", v - 0.2, v + 0.2,
                            version=rng.randint(2, 9))

        fresh = CurveTable(["a"])
        for x in sorted(table._versions):
            fresh.insert(table.active_node(x))

        got = table.index.channel("a")
        want = fresh.index.channel("a")
        self.assertEqual(got.xs, want.xs)
        self.assertEqual(got.segs, want.segs)

    def test_queries_match_after_local_updates(self):
        rng = random.Random(7)
        table = big_table()
        table.insert(make_node(50.5, 3.0))
        table.delete(10.0)
        fresh = CurveTable(["a"])
        for x in sorted(table._versions):
            fresh.insert(table.active_node(x))
        for _ in range(50):
            x = rng.uniform(0, 99)
            self.assertEqual(table.evaluate("a", x).value,
                             fresh.evaluate("a", x).value)
        for y in (-5.0, 0.0, 5.0):
            got = [(s.x_lo, s.x_hi) for s in table.invert("a", y).solutions]
            want = [(s.x_lo, s.x_hi) for s in fresh.invert("a", y).solutions]
            self.assertEqual(got, want)
