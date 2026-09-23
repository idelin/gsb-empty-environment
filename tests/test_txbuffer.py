import os
import tempfile
import unittest

from txbuffer import TransactionBuffer, feed, load_fixture, parse_event

FIXTURES = os.path.join(os.path.dirname(os.path.dirname(os.path.abspath(__file__))), "fixtures")


def change(seq, tx, table="users", pk="u1", op="insert", before=None, after=None):
    return {"kind": "change", "seq": seq, "tx_id": tx, "table": table, "pk": pk,
            "op": op, "before": before, "after": after}


def commit(seq, tx, expected):
    return {"kind": "commit", "seq": seq, "tx_id": tx, "expected_changes": expected}


def rollback(seq, tx):
    return {"kind": "rollback", "seq": seq, "tx_id": tx}


class BufferTestCase(unittest.TestCase):
    def setUp(self):
        self.buf = TransactionBuffer.open(":memory:")

    def tearDown(self):
        self.buf.close()


class TestAtomicCommit(BufferTestCase):
    def test_changes_invisible_until_commit_then_applied_atomically(self):
        feed(self.buf, load_fixture(os.path.join(FIXTURES, "01_atomic_commit.json"))[:-1])
        self.assertEqual(self.buf.read_all("users"), {})
        self.assertEqual(self.buf.status()["buffered_changes"], 2)
        self.buf.ingest(commit(3, "tx-1", 2))
        self.assertEqual(self.buf.read("users", "u1"), {"name": "Ada", "age": 36})
        self.assertEqual(self.buf.read("users", "u2"), {"name": "Grace", "age": 41})
        self.assertEqual(self.buf.status()["buffered_changes"], 0)

    def test_update_and_delete_ops(self):
        feed(self.buf, [
            change(1, "t1", after={"v": 1}), commit(2, "t1", 1),
            change(3, "t2", op="update", before={"v": 1}, after={"v": 2}),
            commit(4, "t2", 1),
            change(5, "t3", op="delete", before={"v": 2}), commit(6, "t3", 1),
        ])
        self.assertIsNone(self.buf.read("users", "u1"))


class TestRollback(BufferTestCase):
    def test_rollback_discards_buffered_changes(self):
        results = feed(self.buf, load_fixture(os.path.join(FIXTURES, "02_rollback.json")))
        self.assertEqual(results, ["accepted", "accepted"])
        self.assertEqual(self.buf.read_all("users"), {})
        self.assertEqual(self.buf.status()["buffered_changes"], 0)

    def test_late_events_of_rolled_back_tx_ignored(self):
        feed(self.buf, [change(1, "t1", after=1), rollback(2, "t1")])
        result = self.buf.ingest(change(3, "t1", after=2))
        self.assertEqual(result, "ignored_finished_tx")
        self.assertEqual(self.buf.read_all("users"), {})


class TestSequenceGap(BufferTestCase):
    def test_gap_pauses_and_resume_preserves_order(self):
        events = load_fixture(os.path.join(FIXTURES, "03_gap_pause.json"))
        feed(self.buf, events[:3])  # seqs 1, 2, 4 -> gap at 3
        status = self.buf.status()
        self.assertEqual(status["state"], "paused")
        self.assertEqual(status["last_seq"], 2)
        self.buf.ingest(events[3])  # seq 3 fills the gap
        self.assertEqual(self.buf.status()["state"], "idle")
        self.assertEqual(self.buf.status()["last_seq"], 4)
        self.buf.ingest(events[4])  # commit
        self.assertEqual(
            self.buf.read_all("users"),
            {"a": {"v": 1}, "b": {"v": 2}, "c": {"v": 3}, "d": {"v": 4}},
        )


class TestDuplicateTransaction(BufferTestCase):
    def test_duplicate_tx_never_applies_twice(self):
        results = feed(self.buf, load_fixture(os.path.join(FIXTURES, "04_duplicate_tx.json")))
        self.assertEqual(results[2:], ["duplicate", "duplicate",
                                       "ignored_finished_tx", "ignored_finished_tx",
                                       "accepted", "accepted"])
        # tx-4 inserted n=1 once; tx-5 updated it to n=2 exactly once.
        self.assertEqual(self.buf.read("users", "u1"), {"n": 2})
        self.assertEqual(self.buf.status()["state"], "idle")

    def test_same_seq_redelivery_deduped_before_commit(self):
        self.buf.ingest(change(1, "t1", after={"v": 1}))
        self.assertEqual(self.buf.ingest(change(1, "t1", after={"v": 1})), "duplicate")
        self.assertEqual(self.buf.status()["buffered_changes"], 1)


class TestEarlyCommit(BufferTestCase):
    def test_commit_waits_for_complete_change_list(self):
        events = load_fixture(os.path.join(FIXTURES, "05_early_commit.json"))
        feed(self.buf, events[:3])  # commit arrives with only 2/3 changes
        status = self.buf.status()
        self.assertEqual(status["state"], "waiting")
        self.assertIn("2/3", status["reason"])
        self.assertEqual(self.buf.read_all("users"), {})
        self.buf.ingest(events[3])  # last change completes the list
        self.assertEqual(len(self.buf.read_all("users")), 3)
        self.assertEqual(self.buf.status()["state"], "idle")


class TestDeleteThenInsert(BufferTestCase):
    def test_same_key_delete_then_insert_applies_in_order(self):
        feed(self.buf, load_fixture(os.path.join(FIXTURES, "06_delete_then_insert.json")))
        self.assertEqual(self.buf.read("users", "u1"), {"name": "new"})


class TestRecovery(unittest.TestCase):
    def test_half_transaction_invisible_after_recovery(self):
        with tempfile.TemporaryDirectory() as tmp:
            db = os.path.join(tmp, "buf.db")
            buf = TransactionBuffer.open(db)
            feed(buf, load_fixture(os.path.join(FIXTURES, "07_half_tx_recovery.json")))
            self.assertEqual(buf.status()["buffered_changes"], 2)
            buf.close()

            reopened = TransactionBuffer.open(db)
            self.assertEqual(reopened.read_all("users"), {})
            self.assertEqual(reopened.status()["buffered_changes"], 2)
            # the buffered half transaction can still complete after recovery
            feed(reopened, [change(3, "tx-8", pk="ghost3", after={"alive": True}),
                            commit(4, "tx-8", 3)])
            self.assertEqual(len(reopened.read_all("users")), 3)
            reopened.close()

    def test_committed_state_survives_restart_and_dedupes_redelivery(self):
        with tempfile.TemporaryDirectory() as tmp:
            db = os.path.join(tmp, "buf.db")
            buf = TransactionBuffer.open(db)
            feed(buf, [change(1, "t1", after={"v": 1}), commit(2, "t1", 1)])
            buf.close()

            reopened = TransactionBuffer.open(db)
            self.assertEqual(reopened.read("users", "u1"), {"v": 1})
            self.assertEqual(reopened.ingest(change(1, "t1", after={"v": 1})), "duplicate")
            self.assertEqual(reopened.ingest(commit(2, "t1", 1)), "duplicate")
            self.assertEqual(reopened.read("users", "u1"), {"v": 1})
            reopened.close()


class TestValidation(BufferTestCase):
    def test_invalid_events_rejected(self):
        bad = [
            {"kind": "bogus", "seq": 1, "tx_id": "t"},
            {"kind": "change", "seq": 0, "tx_id": "t"},
            {"kind": "change", "seq": 1, "tx_id": ""},
            {"kind": "change", "seq": 1, "tx_id": "t", "table": "users", "pk": "u1"},
            {"kind": "change", "seq": 1, "tx_id": "t", "table": "users", "pk": "u1", "op": "upsert"},
            {"kind": "commit", "seq": 1, "tx_id": "t"},
            "not-a-dict",
        ]
        for raw in bad:
            with self.assertRaises(ValueError, msg=repr(raw)):
                parse_event(raw)


if __name__ == "__main__":
    unittest.main()
