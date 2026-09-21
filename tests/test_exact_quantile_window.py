import random
import unittest

from exact_quantile_window import ExactQuantileWindow
from naive_oracle import NaiveQuantileWindow


class ExactQuantileWindowTest(unittest.TestCase):
    def test_specification_example_and_identity_retraction(self):
        window = ExactQuantileWindow(10)
        for seq, value in enumerate((1, 1, 9, 20)):
            window.ingest("s", seq, seq + 1, value)

        window.advance_watermark(10)

        self.assertEqual(window.active_values_sorted(), [1, 1, 9, 20])
        self.assertEqual(window.quantile(0.5), 1)

        self.assertTrue(window.retract("s", 0))
        self.assertEqual(window.active_values_sorted(), [1, 9, 20])
        self.assertEqual(window.quantile(0.5), 9)

    def test_left_open_right_closed_boundaries(self):
        window = ExactQuantileWindow(10)
        window.ingest("left", 1, 0, 0)
        window.ingest("inside", 1, 5, 5)
        window.ingest("right", 1, 10, 10)
        window.ingest("future", 1, 15, 15)

        window.advance_watermark(10)
        self.assertEqual(window.active_values_sorted(), [5, 10])
        self.assertEqual(window.quantile(0), 5)
        self.assertEqual(window.quantile(1), 10)

        window.advance_watermark(15)
        self.assertEqual(window.active_values_sorted(), [10, 15])

    def test_batch_expiry_removes_many_events_at_once(self):
        window = ExactQuantileWindow(10)
        window.advance_watermark(10)

        for timestamp in range(1, 10):
            window.ingest("old", timestamp, timestamp, timestamp)
        for timestamp in range(10, 21):
            window.ingest("new", timestamp, timestamp, timestamp * 10)

        window.advance_watermark(20)
        self.assertEqual(
            window.active_values_sorted(),
            [110, 120, 130, 140, 150, 160, 170, 180, 190, 200],
        )

        window.advance_watermark(40)
        self.assertIsNone(window.quantile(0.5))
        summary = window.memory_summary()
        self.assertEqual(summary["active_event_count"], 0)
        self.assertEqual(summary["active_heap_entries"], 0)
        self.assertEqual(summary["waiting_heap_entries"], 0)
        self.assertEqual(summary["treap_nodes"], 0)

    def test_duplicate_ingest_and_duplicate_retraction(self):
        window = ExactQuantileWindow(10)
        window.advance_watermark(10)

        self.assertTrue(window.ingest("s", 1, 5, 7))
        self.assertFalse(window.ingest("s", 1, 999, 999))
        self.assertEqual(window.active_values_sorted(), [7])

        self.assertTrue(window.retract("s", 1))
        self.assertFalse(window.retract("s", 1))
        self.assertIsNone(window.quantile(0.5))

    def test_q_zero_and_one(self):
        window = ExactQuantileWindow(10)
        window.advance_watermark(10)
        values = [3, 1, 4, 1, 5, 9, 2, 6]
        for seq, value in enumerate(values):
            window.ingest("s", seq, seq + 1, value)

        self.assertEqual(window.quantile(0), 1)
        self.assertEqual(window.quantile(1), 9)
        with self.assertRaises(ValueError):
            window.quantile(-0.1)
        with self.assertRaises(ValueError):
            window.quantile(1.1)

    def test_many_equal_values_and_partial_retraction(self):
        window = ExactQuantileWindow(100)
        window.advance_watermark(100)

        for seq in range(1000):
            self.assertTrue(window.ingest("same", seq, 50, 5))

        self.assertEqual(window.quantile(0.37), 5)
        self.assertEqual(window.memory_summary()["treap_nodes"], 1)

        for seq in range(500):
            self.assertTrue(window.retract("same", seq))
        self.assertEqual(window.active_values_sorted(), [5] * 500)
        self.assertEqual(window.memory_summary()["treap_nodes"], 1)

        window.advance_watermark(150)
        self.assertIsNone(window.quantile(0.5))
        summary = window.memory_summary()
        self.assertEqual(summary["unique_event_records"], 1000)
        self.assertEqual(summary["retracted_event_count"], 500)
        self.assertEqual(summary["expired_event_count"], 500)

    def test_random_operations_match_naive_sorting_oracle(self):
        rng = random.Random(20260921)
        actual = ExactQuantileWindow(10)
        oracle = NaiveQuantileWindow(10)
        right = 0

        actual.advance_watermark(0)
        oracle.advance_watermark(0)

        known_events = []
        check_quantiles = (0, 0.1, 0.25, 0.5, 0.75, 0.9, 1)

        for seq in range(3000):
            choice = rng.randrange(7)

            if choice in (0, 1, 2) or not known_events:
                timestamp = right + rng.randrange(0, 8)
                value = rng.choice((0, 1, 1, 2, 3, 5, 8, 13, 0, 100))
                self.assertEqual(
                    actual.ingest("src", seq, timestamp, value),
                    oracle.ingest("src", seq, timestamp, value),
                )
                known_events.append(seq)
            elif choice in (3, 4):
                right += rng.randrange(0, 13)
                actual.advance_watermark(right)
                oracle.advance_watermark(right)
            elif choice == 5 and known_events:
                event_seq = rng.choice(known_events)
                self.assertEqual(
                    actual.retract("src", event_seq),
                    oracle.retract("src", event_seq),
                )
            else:
                event_seq = rng.choice(known_events)
                value = rng.choice((0, 1, 1, 2, 3, 5, 8, 13, 100))
                self.assertFalse(actual.ingest("src", event_seq, right + 1, value))
                self.assertFalse(oracle.ingest("src", event_seq, right + 1, value))

            if rng.random() < 0.2:
                right += rng.randrange(0, 5)
                actual.advance_watermark(right)
                oracle.advance_watermark(right)

            self.assertEqual(
                actual.active_values_sorted(),
                oracle.active_values_sorted(),
            )
            for q in check_quantiles:
                self.assertEqual(
                    actual.quantile(q),
                    oracle.quantile(q),
                    f"mismatch at operation {seq}, q={q}",
                )


if __name__ == "__main__":
    unittest.main()
