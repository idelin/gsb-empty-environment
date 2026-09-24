"""unittest suite for batchbuf (Python 3.11, stdlib only)."""

import math
import threading
import unittest

from batchbuf import (Batch, Buffer, DoubleReleaseError, DType, Ledger,
                      QuotaExceededError, ResourceClosedError, Schema)

SCHEMA = Schema([("id", DType.INT64), ("val", DType.FLOAT64)])
ROWS = [{"id": i, "val": float(i) * 1.5} for i in range(8)]
# per batch: id data 8*8=64, id validity 8, val data 8*8=64, val validity 8
BATCH_BYTES = 64 + 8 + 64 + 8


def make_batch(ledger, rows=ROWS, schema=SCHEMA):
    return Batch.from_rows(schema, rows, ledger)


def expected_bytes(views):
    """Independent traversal of live views: unique physical buffer bytes."""
    ids = set()
    total = 0
    for batch in views:
        for col in batch._columns:
            for buf in (col._buffer, col._validity):
                if buf.id not in ids:
                    ids.add(buf.id)
                    total += buf.nbytes
    return total, ids


class TestSharedAccounting(unittest.TestCase):
    def test_two_slices_share_buffer_counted_once(self):
        ledger = Ledger()
        batch = make_batch(ledger)
        base = ledger.used
        self.assertEqual(base, BATCH_BYTES)
        s1 = batch.slice(0, 4)
        s2 = batch.slice(2, 4)
        self.assertEqual(ledger.used, base)  # sharing is free
        self.assertEqual(s1.buffer_ids(), batch.buffer_ids())
        for b in (s1, s2, batch):
            b.close()
        self.assertEqual(ledger.used, 0)

    def test_nested_slices_share_one_physical_buffer(self):
        ledger = Ledger()
        batch = make_batch(ledger)
        s1 = batch.slice(1, 6)
        s2 = s1.slice(1, 4)
        s3 = s2.slice(1, 2)
        self.assertEqual(ledger.used, BATCH_BYTES)
        self.assertEqual(len(ledger.live_buffers()), 4)
        self.assertEqual(s3.column("id").to_list(), [3, 4])
        for b in (batch, s1, s2, s3):
            b.close()
        self.assertEqual(ledger.used, 0)

    def test_ledger_crosscheck_by_traversing_live_views(self):
        ledger = Ledger()
        views = [make_batch(ledger)]
        views.append(views[0].slice(0, 5))
        views.append(views[0].slice(3, 5))
        views.append(views[1].slice(1, 3))
        views[1].column("id").set(0, 999)  # COW adds one private buffer
        total, ids = expected_bytes(views)
        self.assertEqual(ledger.used, total)
        self.assertEqual(set(ledger.live_buffers()), ids)
        for v in views:
            v.close()
        self.assertEqual(ledger.used, 0)
        self.assertEqual(ledger.live_buffers(), {})


class TestCloseSemantics(unittest.TestCase):
    def test_close_denies_access_and_is_idempotent(self):
        ledger = Ledger()
        batch = make_batch(ledger)
        batch.close()
        for _ in range(10):  # repeated close in a loop is safe
            batch.close()
        with self.assertRaises(ResourceClosedError):
            batch.column("id")
        with self.assertRaises(ResourceClosedError):
            batch.slice(0, 1)
        self.assertEqual(ledger.used, 0)

    def test_releasing_parent_keeps_live_slices_valid(self):
        ledger = Ledger()
        batch = make_batch(ledger)
        s1 = batch.slice(0, 4)
        s2 = batch.slice(4, 4)
        batch.close()  # parent gone; slices keep their leases per contract
        self.assertEqual(s1.column("id").to_list(), [0, 1, 2, 3])
        self.assertEqual(s2.column("val").to_list(), [6.0, 7.5, 9.0, 10.5])
        self.assertEqual(ledger.used, BATCH_BYTES)  # nothing reclaimed yet
        s1.close()
        self.assertEqual(ledger.used, BATCH_BYTES)  # s2 still holds refs
        s2.close()
        self.assertEqual(ledger.used, 0)

    def test_buffer_access_after_reclaim_denied(self):
        ledger = Ledger()
        buf = Buffer(bytearray(16), ledger)
        buf.release()
        self.assertTrue(buf.reclaimed)
        with self.assertRaises(ResourceClosedError):
            buf.data
        with self.assertRaises(ResourceClosedError):
            buf.acquire()
        with self.assertRaises(ResourceClosedError):
            buf.borrow()

    def test_double_release_raises(self):
        ledger = Ledger()
        buf = Buffer(bytearray(8), ledger)
        buf.release()
        with self.assertRaises(DoubleReleaseError):
            buf.release()
        self.assertEqual(ledger.used, 0)


class TestQuota(unittest.TestCase):
    def test_quota_rejected_before_any_state_change(self):
        ledger = Ledger(quota=BATCH_BYTES)
        batch = make_batch(ledger)
        before = ledger.live_buffers()
        with self.assertRaises(QuotaExceededError):
            make_batch(ledger)
        # nothing changed: first batch intact, ledger identical
        self.assertEqual(ledger.used, BATCH_BYTES)
        self.assertEqual(ledger.live_buffers(), before)
        self.assertEqual(batch.column("id").to_list(), list(range(8)))
        batch.close()
        self.assertEqual(ledger.used, 0)

    def test_failed_build_releases_partial_columns_exactly_once(self):
        ledger = Ledger()
        schema = Schema([("a", DType.INT64), ("b", DType.INT64)])
        rows = [{"a": 1, "b": 2}, {"a": 3, "b": "not-an-int"}]
        with self.assertRaises(TypeError):
            Batch.from_rows(schema, rows, ledger)
        # column "a" was built then cleaned up: no leak, no double release
        self.assertEqual(ledger.used, 0)
        self.assertEqual(ledger.live_buffers(), {})

    def test_cow_over_quota_fails_atomically(self):
        ledger = Ledger()
        batch = make_batch(ledger)
        view = batch.slice(0, 8)  # shares buffers -> COW needs a private copy
        ledger.set_quota(ledger.used)  # any further charge must fail
        before = ledger.live_buffers()
        with self.assertRaises(QuotaExceededError):
            view.column("id").set(0, 12345)
        # column untouched: still shares the original buffer, value unchanged
        self.assertEqual(view.buffer_ids(), batch.buffer_ids())
        self.assertEqual(view.column("id").get(0), 0)
        self.assertEqual(ledger.live_buffers(), before)
        view.close()
        batch.close()
        self.assertEqual(ledger.used, 0)


class TestCopyOnWrite(unittest.TestCase):
    def test_cow_isolates_writer_and_counts_temp_copy(self):
        ledger = Ledger()
        batch = make_batch(ledger)
        view = batch.slice(0, 8)
        base = ledger.used
        view.column("id").set(0, 777)
        # private data (64B) + validity (8B) copies charged while originals live
        self.assertEqual(ledger.used, base + 64 + 8)
        self.assertEqual(batch.column("id").get(0), 0)   # original untouched
        self.assertEqual(view.column("id").get(0), 777)  # writer sees new value
        view.close()
        self.assertEqual(ledger.used, base)
        batch.close()
        self.assertEqual(ledger.used, 0)

    def test_cow_validity_copy_and_null_vs_value(self):
        ledger = Ledger()
        batch = make_batch(ledger)
        view = batch.slice(0, 8)
        view.column("val").set(1, None)
        self.assertIsNone(view.column("val").get(1))
        self.assertEqual(batch.column("val").get(1), 1.5)
        view.close()
        batch.close()
        self.assertEqual(ledger.used, 0)

    def test_unique_column_writes_in_place(self):
        ledger = Ledger()
        batch = make_batch(ledger)
        ids_before = batch.buffer_ids()
        batch.column("id").set(3, 42)  # sole owner: no copy
        self.assertEqual(batch.buffer_ids(), ids_before)
        self.assertEqual(ledger.used, BATCH_BYTES)
        self.assertEqual(batch.column("id").get(3), 42)
        batch.close()


class TestTypesAndValues(unittest.TestCase):
    def test_null_and_nan_are_distinct(self):
        ledger = Ledger()
        schema = Schema([("x", DType.FLOAT64)])
        batch = Batch.from_rows(schema, [{"x": None}, {"x": math.nan},
                                         {"x": 2.0}], ledger)
        col = batch.column("x")
        self.assertIsNone(col.get(0))
        self.assertTrue(col.is_null(0))
        self.assertFalse(col.is_null(1))
        self.assertTrue(math.isnan(col.get(1)))  # NaN is a *value*
        self.assertTrue(math.isnan(batch.sum("x")))  # NaN propagates
        batch.close()
        self.assertEqual(ledger.used, 0)

    def test_integer_sum_is_exact(self):
        ledger = Ledger()
        schema = Schema([("n", DType.INT64)])
        big = 2**62
        batch = Batch.from_rows(schema, [{"n": big}, {"n": big}, {"n": None}],
                                ledger)
        result = batch.sum("n")
        self.assertIsInstance(result, int)
        self.assertEqual(result, 2**63)  # exact, no float rounding
        all_null = Batch.from_rows(schema, [{"n": None}], ledger)
        self.assertIsNone(all_null.sum("n"))
        batch.close()
        all_null.close()

    def test_type_checks(self):
        ledger = Ledger()
        schema = Schema([("n", DType.INT64)])
        with self.assertRaises(TypeError):
            Batch.from_rows(schema, [{"n": True}], ledger)  # bool != int
        with self.assertRaises(TypeError):
            Batch.from_rows(schema, [{"n": 1.5}], ledger)
        self.assertEqual(ledger.used, 0)


class TestBorrowingAndInterleaving(unittest.TestCase):
    def test_no_reclaim_during_borrow_controlled_interleaving(self):
        ledger = Ledger()
        buf = Buffer(bytearray(64), ledger)
        entered = threading.Event()
        proceed = threading.Event()
        errors = []

        def borrower():
            try:
                with buf.borrowed():
                    entered.set()
                    self.assertTrue(proceed.wait(5))
            except BaseException as exc:  # pragma: no cover
                errors.append(exc)

        t = threading.Thread(target=borrower)
        t.start()
        self.assertTrue(entered.wait(5))
        buf.release()  # last reference dropped *while borrowed*
        self.assertIn(buf.id, ledger.live_buffers())  # not reclaimed yet
        self.assertFalse(buf.reclaimed)
        proceed.set()  # borrower leaves -> pending reclaim runs
        t.join(5)
        self.assertFalse(t.is_alive())
        self.assertEqual(errors, [])
        self.assertTrue(buf.reclaimed)
        self.assertEqual(ledger.used, 0)

    def test_concurrent_borrowers_no_double_free(self):
        ledger = Ledger()
        buf = Buffer(bytearray(128), ledger)
        workers = 8
        barrier = threading.Barrier(workers)
        errors = []

        def worker():
            try:
                for _ in range(300):
                    barrier.wait(5)
                    with buf.borrowed():
                        assert not buf.reclaimed
            except BaseException as exc:
                errors.append(exc)
                barrier.abort()

        threads = [threading.Thread(target=worker) for _ in range(workers)]
        for t in threads:
            t.start()
        for t in threads:
            t.join(10)
        self.assertFalse(any(t.is_alive() for t in threads))
        self.assertEqual(errors, [])
        self.assertEqual(buf.borrower_count(), 0)
        buf.release()  # single final release: exactly one uncharge
        self.assertEqual(ledger.used, 0)
        with self.assertRaises(DoubleReleaseError):
            buf.release()

    def test_peak_byte_conservation(self):
        ledger = Ledger()
        trace = []

        def snap():
            trace.append(ledger.used)

        b1 = make_batch(ledger)
        snap()
        b2 = make_batch(ledger)
        snap()
        view = b1.slice(0, 8)
        snap()
        view.column("id").set(0, 1)  # temp private copy charged
        snap()
        view.close()
        snap()
        b1.close()
        snap()
        b2.close()
        snap()
        self.assertEqual(trace[-1], 0)
        self.assertEqual(ledger.peak, max(trace))
        self.assertEqual(ledger.peak, 2 * BATCH_BYTES + 64 + 8)


if __name__ == "__main__":
    unittest.main()
