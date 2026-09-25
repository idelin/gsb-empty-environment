"""Randomised differential test: the index vs. a direct-sorted reference.

Random sequences of ingests (including late/out-of-order arrivals),
corrections and retractions are applied to both the ``EventStore`` and a
brute-force reference (a plainly re-sorted list).  After every step the
range counts, k-th element and quantiles must agree.
"""

import math
import random
import unittest

from bucketindex import EventStore, FineBuckets, Instant


class Reference:
    """Brute-force model: dict of live events, sorted on demand."""

    def __init__(self):
        self.live = {}  # event_id -> (ticks, version)

    def ordered(self):
        return sorted((t, eid, v) for eid, (t, v) in self.live.items())

    def count_range(self, lo, hi):
        return sum(1 for t, _ in self.live.values() if lo <= t < hi)


class TestRandomizedAgainstReference(unittest.TestCase):
    def run_trace(self, seed, steps=400):
        rng = random.Random(seed)
        store = EventStore(FineBuckets(origin=0, width=10))
        ref = Reference()
        versions = {}  # event_id -> last version used
        counter = 0

        for _ in range(steps):
            live_ids = list(ref.live)
            op = rng.random()
            if op < 0.45 or not live_ids:
                counter += 1
                eid = f"e{counter}"
                t = rng.randint(-500, 500)
                store.ingest(eid, Instant(t))
                ref.live[eid] = (t, 1)
                versions[eid] = 1
            elif op < 0.75:
                eid = rng.choice(live_ids)
                t = rng.randint(-500, 500)
                versions[eid] += 1
                store.correct(eid, Instant(t), version=versions[eid])
                ref.live[eid] = (t, versions[eid])
            else:
                eid = rng.choice(live_ids)
                store.retract(eid)
                del ref.live[eid]

            # Total and k-th / quantile checks against the sorted reference.
            ordered = ref.ordered()
            self.assertEqual(store.total(), len(ordered))
            if ordered:
                k = rng.randrange(len(ordered))
                self.assertEqual(store.kth(k), ordered[k])
                q = rng.random()
                kq = max(0, min(len(ordered) - 1, math.ceil(q * len(ordered)) - 1))
                self.assertEqual(store.quantile(q), Instant(ordered[kq][0]))

            # Exact range counts on random (possibly negative) ranges.
            a, b = sorted(rng.randint(-550, 550) for _ in range(2))
            result = store.count_range(Instant(a), Instant(b))
            self.assertEqual(result.total, ref.count_range(a, b))
            self.assertEqual(result.total, result.internal + len(result.edge_items))

        return store, ref

    def test_multiple_seeds(self):
        for seed in (1, 7, 42, 2024):
            with self.subTest(seed=seed):
                self.run_trace(seed)

    def test_business_counts_match_reference(self):
        store, ref = self.run_trace(99, steps=200)
        rule = store.rules.register((-300, -100, 0, 100, 300))
        counts = store.business_counts(rule)
        expected = [0] * rule.bucket_count
        for t, _ in ref.live.values():
            expected[rule.locate(t)] += 1
        self.assertEqual(counts, expected)
        self.assertEqual(sum(counts), len(ref.live))


if __name__ == "__main__":
    unittest.main()
