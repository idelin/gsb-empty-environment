import sqlite3
import unittest

from revisioned_aggregate import Revision, RevisionStore


def rev(rid, key, base, delta, seq):
    return Revision(rid, f"t-{rid}", {key: base}, {key: delta}, seq, seq)


class StoreTest(unittest.TestCase):
    def setUp(self):
        self.store = RevisionStore(":memory:")

    def test_publish_and_checkpoint_are_atomic(self):
        with self.assertRaises(sqlite3.IntegrityError):
            self.store.publish(rev("r1", "txn:A", None, 10, 1),
                               checkpoint_seq="not-an-int")
        self.assertIsNone(self.store.get_revision("r1"))
        self.assertIsNone(self.store.checkpoint())

    def test_same_id_republish_does_not_double_apply(self):
        self.assertTrue(self.store.publish(rev("r1", "txn:A", None, 10, 1), 1))
        self.assertFalse(self.store.publish(rev("r1", "txn:A", None, 10, 1), 1))
        self.assertEqual(len(self.store.revisions()), 1)

    def test_gc_requires_ack_evidence(self):
        self.store.publish(rev("r1", "txn:A", None, 10, 1), 1)
        self.store.publish(rev("r2", "txn:A", "r1", 5, 2), 2)
        self.assertEqual(self.store.gc(), [])
        self.assertIsNotNone(self.store.get_revision("r1"))

    def test_gc_collects_acked_non_heads_and_keeps_evidence(self):
        self.store.publish(rev("r1", "txn:A", None, 10, 1), 1)
        self.store.publish(rev("r2", "txn:A", "r1", 5, 2), 2)
        self.store.record_ack("ack-1", "r1")
        self.store.record_ack("ack-2", "r2")
        self.assertEqual(self.store.gc(), ["r1"])
        self.assertIsNone(self.store.get_revision("r1"))
        self.assertIsNotNone(self.store.get_revision("r2"))  # head kept
        self.assertEqual(self.store.gc_log(),
                         [("r1", "ack-1", "acked by downstream and superseded on all keys")])
        self.assertEqual(self.store.gc(), [])  # stable on re-run


if __name__ == "__main__":
    unittest.main()
