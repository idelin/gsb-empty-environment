import random
import unittest

from quantile_window import NaiveQuantileOracle, SlidingQuantileWindow


class SpecCases(unittest.TestCase):
    def test_example_from_spec(self):
        w = SlidingQuantileWindow()
        for i, v in enumerate([1, 1, 9, 20]):
            self.assertTrue(w.ingest("s", i, time=i + 1, value=v))
        self.assertEqual(w.quantile(0.5), 1)  # floor(0.5 * 3) = 1 -> sorted[1]
        self.assertTrue(w.retract("s", 0))    # retract one of the two 1s
        self.assertEqual(w.quantile(0.5), 9)  # [1, 9, 20] -> index 1

    def test_empty_window_returns_none(self):
        w = SlidingQuantileWindow()
        self.assertIsNone(w.quantile(0.5))
        self.assertIsNone(w.quantile(0.0))
        self.assertIsNone(w.quantile(1.0))

    def test_left_open_right_closed(self):
        w = SlidingQuantileWindow()
        w.ingest("s", 1, time=10, value=100)   # on the left edge
        w.ingest("s", 2, time=11, value=200)   # strictly inside
        evicted = w.advance(10)
        self.assertEqual(evicted, 1)           # time == left edge is evicted
        self.assertEqual(len(w), 1)
        self.assertEqual(w.quantile(0.0), 200)
        self.assertEqual(w.quantile(1.0), 200)

    def test_q_zero_and_one(self):
        w = SlidingQuantileWindow()
        for i, v in enumerate([5, 3, 9, 1, 7]):
            w.ingest("s", i, time=i, value=v)
        self.assertEqual(w.quantile(0.0), 1)   # min
        self.assertEqual(w.quantile(1.0), 9)   # max
        with self.assertRaises(ValueError):
            w.quantile(-0.01)
        with self.assertRaises(ValueError):
            w.quantile(1.01)

    def test_duplicate_ingest_deduped_by_identity(self):
        w = SlidingQuantileWindow()
        self.assertTrue(w.ingest("s", 1, time=1, value=42))
        self.assertFalse(w.ingest("s", 1, time=2, value=99))  # same id, ignored
        self.assertEqual(len(w), 1)
        self.assertEqual(w.quantile(0.5), 42)
        # same value under a different identity is a distinct event
        self.assertTrue(w.ingest("s", 2, time=2, value=42))
        self.assertEqual(len(w), 2)

    def test_duplicate_retract_is_noop(self):
        w = SlidingQuantileWindow()
        w.ingest("s", 1, time=1, value=7)
        w.ingest("s", 2, time=2, value=8)
        self.assertTrue(w.retract("s", 1))
        self.assertFalse(w.retract("s", 1))   # already gone
        self.assertFalse(w.retract("s", 999))  # never existed
        self.assertEqual(len(w), 1)
        self.assertEqual(w.quantile(0.5), 8)

    def test_retract_by_identity_not_value(self):
        w = SlidingQuantileWindow()
        w.ingest("a", 1, time=1, value=5)
        w.ingest("b", 1, time=2, value=5)   # same value, different identity
        w.ingest("c", 1, time=3, value=9)
        w.retract("a", 1)                    # removes only the first 5
        self.assertEqual(len(w), 2)
        self.assertEqual(w.quantile(0.0), 5)  # the other 5 survives
        self.assertEqual(w.quantile(1.0), 9)

    def test_many_identical_values(self):
        w = SlidingQuantileWindow()
        for i in range(1000):
            w.ingest("s", i, time=i, value=7)
        w.ingest("s", 1000, time=1000, value=1)
        w.ingest("s", 1001, time=1001, value=100)
        self.assertEqual(w.quantile(0.0), 1)
        self.assertEqual(w.quantile(0.5), 7)
        self.assertEqual(w.quantile(1.0), 100)
        # retract 500 of the 7s by identity; each duplicate held its own slot
        for i in range(500):
            self.assertTrue(w.retract("s", i + 250))
        self.assertEqual(len(w), 502)
        self.assertEqual(w.quantile(0.5), 7)

    def test_batch_expiry(self):
        w = SlidingQuantileWindow()
        o = NaiveQuantileOracle()
        for i in range(200):
            w.ingest("s", i, time=i, value=(i * 37) % 50)
            o.ingest("s", i, time=i, value=(i * 37) % 50)
        self.assertEqual(w.advance(49), 50)   # times 0..49 evicted at once
        self.assertEqual(o.advance(49), 50)
        self.assertEqual(len(w), 150)
        for q in (0.0, 0.1, 0.5, 0.9, 1.0):
            self.assertEqual(w.quantile(q), o.quantile(q))
        # continuous watermark advancement, no full re-sort each step
        for wm in range(50, 200):
            self.assertEqual(w.advance(wm), o.advance(wm))
            self.assertEqual(len(w), len(o))
        self.assertIsNone(w.quantile(0.5))

    def test_retract_then_expire_skips_lazily(self):
        w = SlidingQuantileWindow()
        for i in range(10):
            w.ingest("s", i, time=i, value=i)
        w.retract("s", 3)   # heap entry for time=3 becomes stale
        self.assertEqual(w.advance(9), 9)
        self.assertEqual(len(w), 0)


class FuzzVsOracle(unittest.TestCase):
    def _run_fuzz(self, seed: int, steps: int) -> None:
        rng = random.Random(seed)
        w = SlidingQuantileWindow()
        o = NaiveQuantileOracle()
        issued = []          # (source, seq) ever ingested
        next_seq = 0
        max_time = 0
        for _ in range(steps):
            op = rng.random()
            if op < 0.45 or not issued:
                # ingest; occasionally reuse an existing id to exercise dedup
                if issued and rng.random() < 0.1:
                    source, seq = rng.choice(issued)
                else:
                    source, seq = rng.choice(["a", "b", "c"]), next_seq
                    next_seq += 1
                    issued.append((source, seq))
                max_time += rng.choice([0, 1, 1, 2, 5])
                value = rng.choice([0, 1, 1, 2, 7, 7, 7, 42, rng.randrange(100)])
                self.assertEqual(
                    w.ingest(source, seq, max_time, value),
                    o.ingest(source, seq, max_time, value),
                )
            elif op < 0.70:
                # retract an existing or a random (possibly absent) id
                if rng.random() < 0.8:
                    source, seq = rng.choice(issued)
                else:
                    source, seq = "zz", rng.randrange(next_seq + 5)
                self.assertEqual(w.retract(source, seq), o.retract(source, seq))
            elif op < 0.85:
                watermark = rng.randrange(0, max_time + 2)
                self.assertEqual(w.advance(watermark), o.advance(watermark))
            else:
                q = rng.choice([0.0, 0.25, 0.5, 0.75, 1.0, rng.random()])
                self.assertEqual(w.quantile(q), o.quantile(q))
            self.assertEqual(len(w), len(o))
        # final full sweep of quantiles
        for i in range(21):
            q = i / 20
            self.assertEqual(w.quantile(q), o.quantile(q))

    def test_fuzz_small_many_seeds(self):
        for seed in range(20):
            with self.subTest(seed=seed):
                self._run_fuzz(seed, steps=800)

    def test_fuzz_larger(self):
        self._run_fuzz(seed=12345, steps=6000)


if __name__ == "__main__":
    unittest.main()
