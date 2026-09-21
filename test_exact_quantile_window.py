import math
import random
import unittest

from exact_quantile_window import ExactQuantileWindow, NaiveQuantileOracle


class TestSpecCases(unittest.TestCase):
    def test_example_from_spec(self):
        w = ExactQuantileWindow(window=100)
        self.assertTrue(w.ingest("a", 1, ts=1, value=1))
        self.assertTrue(w.ingest("a", 2, ts=2, value=1))
        self.assertTrue(w.ingest("a", 3, ts=3, value=9))
        self.assertTrue(w.ingest("a", 4, ts=4, value=20))
        # sorted [1, 1, 9, 20], floor(0.5 * 3) = 1 -> value 1
        self.assertEqual(w.query(0.5), 1)
        # retract one of the two 1s by identity -> [1, 9, 20], idx 1 -> 9
        self.assertTrue(w.retract("a", 1))
        self.assertEqual(w.query(0.5), 9)
        self.assertEqual(len(w), 3)

    def test_empty_window_returns_none(self):
        w = ExactQuantileWindow(window=10)
        self.assertIsNone(w.query(0.5))
        w.ingest("s", 1, ts=1, value=5)
        w.retract("s", 1)
        self.assertIsNone(w.query(0.5))
        self.assertIsNone(w.query(0.0))
        self.assertIsNone(w.query(1.0))

    def test_left_open_right_closed(self):
        w = ExactQuantileWindow(window=10)
        w.ingest("s", 1, ts=5, value=50)    # watermark -> 5, left = -5
        w.ingest("s", 2, ts=10, value=100)
        evicted = w.advance(15)             # left = 5: ts == 5 is OUT
        self.assertEqual(evicted, 1)
        self.assertEqual(len(w), 1)
        self.assertEqual(w.query(0.0), 100)
        self.assertEqual(w.query(1.0), 100)
        # ts == left is rejected as late; ts == right edge is kept
        self.assertFalse(w.ingest("s", 3, ts=5, value=1))
        self.assertTrue(w.ingest("s", 4, ts=15, value=150))  # == right, kept
        self.assertEqual(len(w), 2)
        self.assertEqual(w.query(0.0), 100)
        self.assertEqual(w.query(1.0), 150)

    def test_batch_expiry(self):
        w = ExactQuantileWindow(window=10)
        for i in range(1, 51):              # ts 1..50; ingest auto-advances
            w.ingest("s", i, ts=i, value=i * 10)
        # after ingest, watermark = 50, window (40, 50]
        self.assertEqual(len(w), 10)
        self.assertEqual(w.query(0.0), 410)
        self.assertEqual(w.query(1.0), 500)
        # one advance call evicts the whole remaining window at once
        evicted = w.advance(60)             # left = 50
        self.assertEqual(evicted, 10)
        self.assertEqual(len(w), 0)
        self.assertIsNone(w.query(0.5))
        self.assertEqual(w.total_expired, 50)

    def test_duplicate_retract_is_idempotent(self):
        w = ExactQuantileWindow(window=100)
        w.ingest("s", 1, ts=1, value=7)
        w.ingest("s", 2, ts=2, value=8)
        self.assertTrue(w.retract("s", 1))
        before = len(w)
        self.assertFalse(w.retract("s", 1))     # second retract: no-op
        self.assertFalse(w.retract("s", 999))   # never existed
        self.assertEqual(len(w), before)
        self.assertEqual(w.query(0.5), 8)

    def test_dedup_by_source_seq(self):
        w = ExactQuantileWindow(window=100)
        self.assertTrue(w.ingest("s", 1, ts=1, value=7))
        self.assertFalse(w.ingest("s", 1, ts=2, value=999))  # same identity
        self.assertEqual(len(w), 1)
        self.assertEqual(w.query(0.5), 7)
        # same value, different identity: both occupy slots
        self.assertTrue(w.ingest("s", 2, ts=2, value=7))
        self.assertEqual(len(w), 2)

    def test_q_zero_and_one(self):
        w = ExactQuantileWindow(window=100)
        for i, v in enumerate([42, 5, 17, 99, 1]):
            w.ingest("s", i, ts=i + 1, value=v)
        self.assertEqual(w.query(0.0), 1)    # min
        self.assertEqual(w.query(1.0), 99)   # max
        with self.assertRaises(ValueError):
            w.query(-0.1)
        with self.assertRaises(ValueError):
            w.query(1.1)

    def test_many_identical_values(self):
        w = ExactQuantileWindow(window=10**9)
        n = 2000
        for i in range(n):
            w.ingest("s", i, ts=i + 1, value=7)
        for q in (0.0, 0.25, 0.5, 0.75, 1.0):
            self.assertEqual(w.query(q), 7)
        # retract half of them by identity; every retract hits a distinct slot
        for i in range(n // 2):
            self.assertTrue(w.retract("s", i))
        self.assertEqual(len(w), n // 2)
        self.assertEqual(w.query(0.5), 7)
        # duplicates of one value do not collapse: n-1 more retracts empty it
        for i in range(n // 2, n):
            w.retract("s", i)
        self.assertIsNone(w.query(0.5))

    def test_retract_by_identity_not_value(self):
        w = ExactQuantileWindow(window=100)
        w.ingest("a", 1, ts=1, value=1)
        w.ingest("b", 1, ts=2, value=1)   # same value, different identity
        w.ingest("c", 1, ts=3, value=9)
        w.retract("a", 1)                  # removes exactly one of the 1s
        self.assertEqual(len(w), 2)
        self.assertEqual(w.query(0.0), 1)  # the other 1 survives
        self.assertEqual(w.query(1.0), 9)

    def test_watermark_cannot_go_backward(self):
        w = ExactQuantileWindow(window=10)
        w.ingest("s", 1, ts=10, value=1)
        with self.assertRaises(ValueError):
            w.advance(5)
        self.assertEqual(w.advance(10), 0)  # same watermark: no-op


class TestRandomizedVsOracle(unittest.TestCase):
    def run_sequence(self, seed, ops):
        rng = random.Random(seed)
        w = ExactQuantileWindow(window=20)
        oracle = NaiveQuantileOracle(window=20)
        live_idents = []
        now = 0
        for step in range(ops):
            action = rng.random()
            if action < 0.45:
                now += rng.randint(0, 3)
                # small value pool -> heavy duplication
                value = rng.choice([0, 1, 1, 2, 5, 5, 5, 9])
                source = rng.choice(["a", "b", "c"])
                seq = rng.randint(0, ops // 4)  # collisions exercise dedup
                ident = (source, seq)
                r1 = w.ingest(source, seq, ts=now, value=value)
                r2 = oracle.ingest(source, seq, ts=now, value=value)
                self.assertEqual(r1, r2, f"ingest mismatch at step {step}")
                if r1:
                    live_idents.append(ident)
            elif action < 0.65:
                now += rng.randint(0, 5)
                w.advance(now)
                oracle.advance(now)
            elif action < 0.85:
                if live_idents and rng.random() < 0.8:
                    ident = live_idents.pop(rng.randrange(len(live_idents)))
                else:
                    ident = ("z", rng.randint(0, ops))  # likely absent
                r1 = w.retract(*ident)
                r2 = oracle.retract(*ident)
                self.assertEqual(r1, r2, f"retract mismatch at step {step}")
            else:
                q = rng.choice([0.0, 0.1, 0.5, 0.9, 1.0, rng.random()])
                got = w.query(q)
                want = oracle.query(q)
                self.assertEqual(got, want,
                                 f"query({q}) at step {step}: got {got}, want {want}")
                self.assertEqual(len(w), len(oracle))
        # final full comparison across a q sweep
        for i in range(21):
            q = i / 20
            self.assertEqual(w.query(q), oracle.query(q))
        self.assertEqual(len(w), len(oracle))

    def test_random_sequences(self):
        for seed in (1, 2, 3):
            with self.subTest(seed=seed):
                self.run_sequence(seed, ops=3000)

    def test_long_run_no_expiry_storm_resort(self):
        # many tiny advances with nothing to expire must stay cheap and correct
        rng = random.Random(7)
        w = ExactQuantileWindow(window=10**6)
        for i in range(5000):
            w.ingest("s", i, ts=i, value=rng.randint(0, 50))
        for t in range(5000, 5200):
            self.assertEqual(w.advance(t), 0)  # nothing expires, O(1) each
        self.assertEqual(len(w), 5000)
        self.assertEqual(w.query(0.5), w.query(0.5))


if __name__ == "__main__":
    unittest.main(verbosity=2)
