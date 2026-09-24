"""Contract tests for slicedbuf: accounting, leasing, COW, interleavings."""
import math
import random
import threading
import unittest

from slicedbuf import (
    FLOAT64,
    INT64,
    Batch,
    Buffer,
    ClosedError,
    DoubleReleaseError,
    Ledger,
    QuotaExceeded,
    Schema,
    assert_consistent,
)


def int_schema():
    return Schema([("a", INT64)])


def make_batch(ledger, data, schema=None):
    return Batch.from_columns(ledger, schema or int_schema(), [data])


class LedgerAccountingTests(unittest.TestCase):
    def test_shared_buffer_counted_once(self):
        ledger = Ledger(1 << 20)
        batch = make_batch(ledger, list(range(10)))
        left = batch.slice(0, 5)
        right = batch.slice(5, 5)
        snap = ledger.snapshot()
        self.assertEqual(snap.live_bytes, 10 * 8 + 10)
        self.assertEqual(len(snap.live), 2)
        assert_consistent(ledger, [batch, left, right])
        for b in (batch, left, right):
            b.release()
        self.assertEqual(ledger.snapshot().live_bytes, 0)

    def test_peak_bytes_conservation(self):
        ledger = Ledger(1 << 20)
        rng = random.Random(7)
        buffers = []
        expected_live = 0
        expected_peak = 0
        snap = ledger.snapshot()
        for _ in range(300):
            if buffers and rng.random() < 0.5:
                victim = buffers.pop(rng.randrange(len(buffers)))
                expected_live -= victim.size
                victim.release()
            else:
                size = rng.randrange(1, 96)
                buffers.append(Buffer(ledger, bytes(size)))
                expected_live += size
                expected_peak = max(expected_peak, expected_live)
            snap = ledger.snapshot()
            self.assertEqual(snap.live_bytes, expected_live)
        self.assertEqual(snap.peak_bytes, expected_peak)
        for buf in buffers:
            buf.release()
        self.assertEqual(ledger.snapshot().live_bytes, 0)

    def test_ledger_double_release_rejected(self):
        ledger = Ledger(1024)
        buf = Buffer(ledger, b"x" * 10)
        buf.release()
        with self.assertRaises(DoubleReleaseError):
            ledger.release(buf.id)
        with self.assertRaises(DoubleReleaseError):
            ledger.release(999999)

    def test_quota_rejected_before_any_state_change(self):
        ledger = Ledger(35)
        with self.assertRaises(QuotaExceeded):
            make_batch(ledger, [1, 2, 3, 4])
        snap = ledger.snapshot()
        self.assertEqual(snap.live_bytes, 0)
        self.assertEqual(snap.live, {})
        self.assertEqual(snap.freed_count, 1)

    def test_failed_build_releases_partials_exactly_once(self):
        schema = Schema([("a", INT64), ("b", INT64)])
        ledger = Ledger(36 + 16)
        with self.assertRaises(QuotaExceeded):
            Batch.from_columns(ledger, schema, [[1, 2, 3, 4], [5, 6, 7, 8]])
        snap = ledger.snapshot()
        self.assertEqual(snap.live_bytes, 0)
        self.assertEqual(snap.freed_count, 2)

    def test_ledger_close_rejects_reservation_and_is_idempotent(self):
        ledger = Ledger(1024)
        buf = Buffer(ledger, b"abcd")
        ledger.close()
        ledger.close()
        with self.assertRaises(ClosedError):
            Buffer(ledger, b"nope")
        with self.assertRaises(ClosedError):
            make_batch(ledger, [1])
        buf.release()
        self.assertEqual(ledger.snapshot().live_bytes, 0)


class SliceLeaseTests(unittest.TestCase):
    def setUp(self):
        self.ledger = Ledger(1 << 20)

    def test_nested_slices_read_correct_windows(self):
        batch = make_batch(self.ledger, list(range(10)))
        mid = batch.slice(2, 6)
        inner = mid.slice(1, 3)
        self.assertEqual(inner.column("a").to_list(), [3, 4, 5])
        self.assertEqual(self.ledger.snapshot().live_bytes, 90)
        for b in (batch, mid, inner):
            b.release()

    def test_releasing_parent_keeps_slices_valid(self):
        batch = make_batch(self.ledger, [10, 20, 30, 40])
        view = batch.slice(1, 2)
        batch.release()
        batch.release()
        self.assertEqual(view.column("a").to_list(), [20, 30])
        view.release()
        self.assertEqual(self.ledger.snapshot().live_bytes, 0)

    def test_repeated_close_is_safe(self):
        batch = make_batch(self.ledger, [1, 2, 3])
        view = batch.slice(0, 3)
        for _ in range(3):
            batch.close()
            view.close()
        self.assertEqual(self.ledger.snapshot().live_bytes, 0)

    def test_closed_resources_reject_access(self):
        batch = make_batch(self.ledger, [1, 2, 3])
        view = batch.slice(0, 2)
        batch.release()
        with self.assertRaises(ClosedError):
            batch.column("a")
        with self.assertRaises(ClosedError):
            batch.slice(0, 1)
        with self.assertRaises(ClosedError):
            batch.borrow()
        view.release()
        with self.assertRaises(ClosedError):
            view.column("a").get(0)

    def test_slice_out_of_range_rejected(self):
        batch = make_batch(self.ledger, [1, 2, 3])
        with self.assertRaises(IndexError):
            batch.slice(2, 5)
        with self.assertRaises(IndexError):
            batch.slice(-1, 2)
        batch.release()


class TypedValueTests(unittest.TestCase):
    def setUp(self):
        self.ledger = Ledger(1 << 20)

    def test_null_and_nan_are_distinct(self):
        schema = Schema([("x", FLOAT64)])
        batch = Batch.from_columns(
            self.ledger, schema, [[1.5, None, float("nan")]]
        )
        col = batch.column("x")
        self.assertEqual(col.get(0), 1.5)
        self.assertIsNone(col.get(1))
        nan_value = col.get(2)
        self.assertIsNotNone(nan_value)
        self.assertTrue(math.isnan(nan_value))
        batch.release()

    def test_null_survives_slicing_and_cow(self):
        schema = Schema([("x", FLOAT64)])
        batch = Batch.from_columns(self.ledger, schema, [[None, 2.0, None]])
        view = batch.slice(1, 2)
        view.column("x").set(1, 3.5)
        self.assertIsNone(batch.column("x").get(2))
        self.assertEqual(view.column("x").to_list(), [2.0, 3.5])
        batch.release()
        view.release()

    def test_int64_exact_arithmetic(self):
        values = [2**62 + 123456789, -2**63, 2**63 - 1, 0]
        batch = make_batch(self.ledger, values)
        self.assertEqual(batch.column("a").to_list(), values)
        self.assertEqual(sum(batch.column("a").to_list()), sum(values))
        batch.release()

    def test_int64_overflow_rejected(self):
        with self.assertRaises(Exception):
            make_batch(self.ledger, [2**63])
        self.assertEqual(self.ledger.snapshot().live_bytes, 0)


class CopyOnWriteTests(unittest.TestCase):
    def setUp(self):
        self.ledger = Ledger(1 << 20)

    def test_cow_isolates_writer_and_accounts_temp_copy(self):
        batch = make_batch(self.ledger, [0, 1, 2, 3])
        view = batch.slice(0, 4)
        before = self.ledger.snapshot().live_bytes
        view.column("a").set(0, 99)
        self.assertEqual(batch.column("a").get(0), 0)
        self.assertEqual(view.column("a").get(0), 99)
        after = self.ledger.snapshot().live_bytes
        self.assertEqual(after, before + 36)
        assert_consistent(self.ledger, [batch, view])
        batch.release()
        view.release()

    def test_cow_quota_rejected_before_state_change(self):
        ledger = Ledger(36)
        batch = make_batch(ledger, [10, 11, 12, 13])
        view = batch.slice(0, 4)
        with self.assertRaises(QuotaExceeded):
            view.column("a").set(0, 77)
        self.assertEqual(view.column("a").get(0), 10)
        self.assertEqual(batch.column("a").get(0), 10)
        self.assertEqual(ledger.snapshot().live_bytes, 36)
        batch.release()
        view.release()

    def test_unshared_view_writes_in_place(self):
        batch = make_batch(self.ledger, [1, 2, 3])
        before = self.ledger.snapshot().live_bytes
        batch.column("a").set(1, 42)
        self.assertEqual(batch.column("a").get(1), 42)
        self.assertEqual(self.ledger.snapshot().live_bytes, before)
        batch.release()


class ConcurrencyTests(unittest.TestCase):
    def test_no_reclaim_during_borrow_controlled_interleaving(self):
        ledger = Ledger(1 << 20)
        batch = make_batch(ledger, [7, 8, 9])
        values_buf = batch.column("a").buffers()[0]
        entered = threading.Event()
        proceed = threading.Event()
        observations = []

        def borrower():
            with values_buf.borrow():
                entered.set()
                self.assertTrue(proceed.wait(5))
                observations.append(values_buf.read(0, 8))
                observations.append(values_buf.is_open)

        thread = threading.Thread(target=borrower)
        thread.start()
        self.assertTrue(entered.wait(5))
        batch.release()
        self.assertTrue(values_buf.is_open)
        self.assertIn(values_buf.id, ledger.snapshot().live)
        proceed.set()
        thread.join(5)
        self.assertEqual(observations[1], True)
        self.assertFalse(values_buf.is_open)
        self.assertNotIn(values_buf.id, ledger.snapshot().live)

    def test_concurrent_release_exactly_once(self):
        ledger = Ledger(1 << 20)
        batch = make_batch(ledger, list(range(100)))
        slices = [batch.slice(0, 100) for _ in range(8)]
        batch.release()
        barrier = threading.Barrier(len(slices) + 1)
        errors = []

        def releaser(view):
            barrier.wait()
            try:
                view.release()
                view.release()
            except BaseException as exc:
                errors.append(exc)

        threads = [threading.Thread(target=releaser, args=(s,)) for s in slices]
        for thread in threads:
            thread.start()
        barrier.wait()
        for thread in threads:
            thread.join(5)
        self.assertEqual(errors, [])
        snap = ledger.snapshot()
        self.assertEqual(snap.live_bytes, 0)
        self.assertEqual(snap.freed_count, 2)

    def test_concurrent_borrowers_read_consistently(self):
        ledger = Ledger(1 << 20)
        data = list(range(200))
        batch = make_batch(ledger, data)
        expected = sum(data)
        barrier = threading.Barrier(5)
        results = []
        errors = []

        def worker():
            barrier.wait()
            try:
                for _ in range(20):
                    with batch.borrow():
                        results.append(sum(batch.column("a").to_list()))
            except BaseException as exc:
                errors.append(exc)

        threads = [threading.Thread(target=worker) for _ in range(4)]
        for thread in threads:
            thread.start()
        barrier.wait()
        for thread in threads:
            thread.join(10)
        self.assertEqual(errors, [])
        self.assertEqual(results, [expected] * 80)
        batch.release()
        self.assertEqual(ledger.snapshot().live_bytes, 0)


class InvariantAuditTests(unittest.TestCase):
    def test_independent_traversal_matches_ledger(self):
        ledger = Ledger(1 << 20)
        schema = Schema([("a", INT64), ("x", FLOAT64)])
        batch = Batch.from_columns(
            ledger, schema, [[1, 2, 3, 4], [0.5, None, float("nan"), 4.5]]
        )
        s1 = batch.slice(0, 3)
        s2 = s1.slice(1, 2)
        assert_consistent(ledger, [batch, s1, s2])
        s1.column("a").set(0, 100)
        assert_consistent(ledger, [batch, s1, s2])
        batch.release()
        assert_consistent(ledger, [batch, s1, s2])
        s1.release()
        assert_consistent(ledger, [batch, s1, s2])
        s2.release()
        snap = assert_consistent(ledger, [batch, s1, s2])
        self.assertEqual(snap.live_bytes, 0)
        self.assertGreater(snap.peak_bytes, 0)


if __name__ == "__main__":
    unittest.main()
