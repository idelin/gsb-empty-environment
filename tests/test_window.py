from __future__ import annotations

import random
import unittest

from quantile_window import (
    Event,
    ExactQuantileWindow,
    NaiveSortingWindow,
)


def ev(time: int, source: str, seq: int, value: int) -> Event:
    return Event(time=time, source=source, seq=seq, value=value)


class ExactQuantileWindowTest(unittest.TestCase):
    def test_example_duplicate_values_and_identity_retraction(self) -> None:
        window = ExactQuantileWindow(window_size=100)
        for seq, value in enumerate((1, 1, 9, 20)):
            self.assertTrue(window.ingest(ev(1, "sensor", seq, value)))

        window.advance_watermark(10)
        self.assertEqual(window.quantile(0.5), 1)

        self.assertTrue(window.retract("sensor", 0))
        self.assertEqual([window.quantile(0), window.quantile(0.5), window.quantile(1)], [1, 9, 20])

    def test_left_open_right_closed_boundary(self) -> None:
        window = ExactQuantileWindow(window_size=1)
        for time, value in ((0, 0), (1, 10), (2, 20), (3, 30)):
            window.ingest(ev(time, "s", time, value))

        result = window.advance_watermark(2)
        self.assertEqual(result.activated_events, 1)
        self.assertEqual(result.discarded_late_events, 2)
        self.assertEqual(window.quantile(0.5), 20)

        result = window.advance_watermark(3)
        self.assertEqual(result.expired_active_events, 1)
        self.assertEqual(result.activated_events, 1)
        self.assertEqual(window.quantile(0.5), 30)

    def test_empty_window_and_extreme_quantiles(self) -> None:
        window = ExactQuantileWindow(window_size=10)
        window.advance_watermark(1)
        self.assertIsNone(window.quantile(0.5))

        for seq, value in enumerate((3, 1, 2)):
            window.ingest(ev(1, "s", seq, value))
        self.assertEqual(window.quantile(0), 1)
        self.assertEqual(window.quantile(1), 3)
        with self.assertRaises(ValueError):
            window.quantile(-0.1)
        with self.assertRaises(ValueError):
            window.quantile(1.1)

    def test_duplicate_retraction_and_duplicate_ingest(self) -> None:
        window = ExactQuantileWindow(window_size=10)
        event = ev(1, "s", 7, 42)
        self.assertTrue(window.ingest(event))
        self.assertFalse(window.ingest(ev(2, "s", 7, 99)))
        window.advance_watermark(2)

        self.assertTrue(window.retract("s", 7))
        self.assertFalse(window.retract("s", 7))
        self.assertFalse(window.ingest(event))
        self.assertIsNone(window.quantile(0.5))

    def test_batch_expiration_many_equal_values(self) -> None:
        window = ExactQuantileWindow(window_size=10)
        for seq in range(100):
            window.ingest(ev(seq % 10, "same", seq, 5))
        window.ingest(ev(9, "other", 0, 99))
        window.advance_watermark(9)
        self.assertEqual(window.quantile(0.5), 5)

        result = window.advance_watermark(20)
        self.assertEqual(result.expired_active_events, 101)
        self.assertIsNone(window.quantile(0.5))

    def test_random_operation_sequence_matches_naive_oracle(self) -> None:
        rng = random.Random(20260922)
        for trial in range(40):
            window_size = rng.choice((1, 2, 3, 5, 10))
            actual = ExactQuantileWindow(window_size, treap_seed=trial)
            oracle = NaiveSortingWindow(window_size)
            known: list[tuple[str, int]] = []
            watermark = 0

            for _ in range(300):
                action = rng.randrange(7)

                if action == 0 or not known:
                    seq = len(known)
                    source = rng.choice(("a", "b"))
                    event = Event(
                        time=rng.randrange(0, 25),
                        source=source,
                        seq=seq,
                        value=rng.choice((1, 1, 1, 2, 7, 7, 20, 100)),
                    )
                    known.append((source, seq))
                    self.assertEqual(actual.ingest(event), oracle.ingest(event))
                elif action == 1:
                    source, seq = rng.choice(known)
                    duplicate = Event(
                        time=rng.randrange(0, 25),
                        source=source,
                        seq=seq,
                        value=-999,
                    )
                    self.assertEqual(actual.ingest(duplicate), oracle.ingest(duplicate))
                elif action == 2:
                    source, seq = rng.choice(known)
                    self.assertEqual(actual.retract(source, seq), oracle.retract(source, seq))
                elif action == 3:
                    watermark += rng.choice((0, 1, 2, 5))
                    actual_result = actual.advance_watermark(watermark)
                    oracle_result = oracle.advance_watermark(watermark)
                    self.assertEqual(
                        actual_result.activated_events,
                        oracle_result.activated_events,
                    )
                    self.assertEqual(
                        actual_result.expired_active_events,
                        oracle_result.expired_active_events,
                    )
                    self.assertEqual(
                        actual_result.discarded_late_events,
                        oracle_result.discarded_late_events,
                    )
                else:
                    for q in (0.0, 0.25, 0.5, 0.75, 1.0):
                        self.assertEqual(actual.quantile(q), oracle.quantile(q), (trial, q))

                actual_stats = actual.stats()
                oracle_stats = oracle.stats()
                self.assertEqual(actual_stats.active_events, oracle_stats.active_events)
                self.assertEqual(actual_stats.pending_events, oracle_stats.pending_events)
                self.assertEqual(actual_stats.stored_events, oracle_stats.stored_events)
                self.assertEqual(
                    actual_stats.identity_tombstones,
                    oracle_stats.identity_tombstones,
                )


if __name__ == "__main__":
    unittest.main()
