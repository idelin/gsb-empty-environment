import random
import unittest

from logmerge import (
    Event,
    InfeasibleError,
    OffsetInterval,
    OffsetTable,
    WallClock,
    linear_extensions,
    merge,
    sampled_orders,
    verify_markers,
)
from tests.test_merger import EVENTS, TABLE


class TestEnumeration(unittest.TestCase):
    def test_linear_extensions_count(self):
        exts = linear_extensions(["a", "b", "c"], [])
        self.assertEqual(len(exts), 6)
        exts = linear_extensions(["a", "b", "c"], [("a", "b")])
        self.assertEqual(len(exts), 3)

    def test_markers_verified_on_main_scenario(self):
        result = merge(EVENTS, TABLE)
        self.assertEqual(verify_markers(result), [])

    def test_sampled_orders_respect_necessary_pairs(self):
        result = merge(EVENTS, TABLE)
        orders = sampled_orders(result.events, TABLE)
        self.assertIsNotNone(orders)
        self.assertGreaterEqual(len(orders), 2)  # w2/c1 really can swap
        for order in orders:
            pos = {eid: i for i, eid in enumerate(order)}
            for a, b in result.necessary_pairs:
                self.assertLess(pos[a], pos[b], f"{a}<{b} violated in {order}")
        # both orders of the uncertain pair show up in the samples
        orderings = {(o.index("w2") < o.index("c1")) for o in orders}
        self.assertEqual(orderings, {True, False})


class TestRandomScenarios(unittest.TestCase):
    def test_random_small_scenarios(self):
        rng = random.Random(20260926)
        checked = 0
        for _ in range(200):
            sources = ["s0", "s1", "s2"]
            table = OffsetTable({
                s: OffsetInterval(rng.randint(-8, 0), rng.randint(0, 8))
                for s in sources
            })
            events = []
            serial = 0
            for s in sources:
                wall = rng.randint(0, 30)
                for _ in range(rng.randint(1, 2)):
                    wall += rng.randint(0, 12)
                    events.append(Event(
                        f"e{serial}", s, WallClock(wall, s), "payload"))
                    serial += 1
            rng.shuffle(events)
            # acyclic causal edges by construction (index order)
            for i in range(len(events)):
                for j in range(i + 1, len(events)):
                    if rng.random() < 0.12:
                        e = events[j]
                        events[j] = Event(e.id, e.source, e.wall, e.payload,
                                          causes=e.causes + (events[i].id,))
            try:
                result = merge(events, table)
            except InfeasibleError:
                continue
            self.assertEqual(verify_markers(result), [])
            orders = sampled_orders(result.events, table)
            self.assertIsNotNone(orders)
            for order in orders:
                pos = {eid: i for i, eid in enumerate(order)}
                for a, b in result.necessary_pairs:
                    self.assertLess(pos[a], pos[b])
            checked += 1
        self.assertGreater(checked, 50)  # enough feasible samples actually ran


if __name__ == "__main__":
    unittest.main()
