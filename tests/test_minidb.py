import os
import unittest

from minidb import (
    ConflictError,
    Database,
    Event,
    ForeignKey,
    FrozenSnapshotError,
    apply_events,
    load_events,
    verify,
)

FIXTURE = os.path.join(os.path.dirname(__file__), "..", "fixtures", "changes.json")


def build_db(user_ids=(1, 2, 3, 4)):
    db = Database()
    db.create_table("users", primary_key="id")
    db.create_table(
        "orders",
        primary_key="id",
        foreign_keys=[ForeignKey(column="user_id", ref_table="users", ref_column="id")],
    )
    names = {1: "alice", 2: "bob", 3: "carol", 4: "dave", 5: "erin", 6: "frank"}
    for user_id in user_ids:
        db.insert("users", {"id": user_id, "name": names[user_id]})
    db.insert("orders", {"id": 100, "user_id": 1, "item": "book"})
    db.insert("orders", {"id": 101, "user_id": 2, "item": "pen"})
    return db


class KeyChangeTests(unittest.TestCase):
    def setUp(self):
        self.db = build_db()

    def test_simple_key_change_updates_index(self):
        txn = self.db.begin("t1")
        txn.change_primary_key("users", 1, 10, commit_seq=1)
        txn.commit()
        self.assertEqual(self.db.get("users", 10)["name"], "alice")
        self.assertEqual(verify(self.db), [])

    def test_old_key_no_longer_hits(self):
        txn = self.db.begin("t1")
        txn.change_primary_key("users", 1, 10, commit_seq=1)
        txn.commit()
        self.assertIsNone(self.db.get("users", 1))

    def test_entity_id_is_stable_across_key_change(self):
        before = self.db.entity_id_of("users", 1)
        txn = self.db.begin("t1")
        txn.change_primary_key("users", 1, 10, commit_seq=1)
        txn.commit()
        self.assertEqual(self.db.entity_id_of("users", 10), before)

    def test_foreign_keys_follow_entity_in_same_transaction(self):
        txn = self.db.begin("t1")
        txn.change_primary_key("users", 1, 10, commit_seq=1)
        txn.commit()
        self.assertEqual(self.db.get("orders", 100)["user_id"], 10)
        self.assertEqual(verify(self.db), [])

    def test_swap_two_keys_in_one_transaction(self):
        id1 = self.db.entity_id_of("users", 1)
        id2 = self.db.entity_id_of("users", 2)
        txn = self.db.begin("swap")
        txn.change_primary_key("users", 1, 2, commit_seq=1)
        txn.change_primary_key("users", 2, 1, commit_seq=2)
        txn.commit()
        self.assertEqual(self.db.get("users", 2)["name"], "alice")
        self.assertEqual(self.db.get("users", 1)["name"], "bob")
        self.assertEqual(self.db.entity_id_of("users", 2), id1)
        self.assertEqual(self.db.entity_id_of("users", 1), id2)
        # References follow the entity, not the key value.
        self.assertEqual(self.db.get("orders", 100)["user_id"], 2)
        self.assertEqual(self.db.get("orders", 101)["user_id"], 1)
        self.assertEqual(verify(self.db), [])

    def test_key_cycle_rotation(self):
        txn = self.db.begin("cycle")
        txn.change_primary_key("users", 1, 2, commit_seq=1)
        txn.change_primary_key("users", 2, 3, commit_seq=2)
        txn.change_primary_key("users", 3, 1, commit_seq=3)
        txn.commit()
        self.assertEqual(self.db.get("users", 2)["name"], "alice")
        self.assertEqual(self.db.get("users", 3)["name"], "bob")
        self.assertEqual(self.db.get("users", 1)["name"], "carol")
        self.assertEqual(verify(self.db), [])

    def test_occupied_new_key_fails_whole_transaction(self):
        txn = self.db.begin("bad")
        txn.change_primary_key("users", 1, 10, commit_seq=1)  # fine alone
        txn.change_primary_key("users", 3, 4, commit_seq=2)   # 4 occupied
        with self.assertRaises(ConflictError):
            txn.commit()
        self.assertEqual(txn.state, "rolled back")
        # The first, individually-valid change is rolled back too.
        self.assertEqual(self.db.get("users", 1)["name"], "alice")
        self.assertIsNone(self.db.get("users", 10))
        self.assertEqual(self.db.get("orders", 100)["user_id"], 1)
        self.assertEqual(verify(self.db), [])

    def test_rollback_restores_index_and_reverse_refs(self):
        snapshot_refs = {k: set(v) for k, v in self.db.reverse_refs.items()}
        txn = self.db.begin("bad")
        txn.change_primary_key("users", 1, 2, commit_seq=1)
        with self.assertRaises(ConflictError):
            txn.commit()
        self.assertEqual(
            {k: set(v) for k, v in self.db.reverse_refs.items()}, snapshot_refs
        )
        self.assertEqual(verify(self.db), [])

    def test_retired_entity_ids_are_never_reused(self):
        retired = self.db.entity_id_of("users", 4)
        self.db.delete("users", 4)
        new_id = self.db.insert("users", {"id": 4, "name": "dana"})
        self.assertNotEqual(new_id, retired)
        self.assertIn(retired, self.db.tables["users"].retired_ids)
        self.assertEqual(verify(self.db), [])

    def test_delete_referenced_row_is_rejected(self):
        with self.assertRaises(ConflictError):
            self.db.delete("users", 1)

    def test_snapshot_is_consistent_and_frozen(self):
        snap = self.db.snapshot()
        txn = self.db.begin("t1")
        txn.change_primary_key("users", 1, 10, commit_seq=1)
        txn.commit()
        # The snapshot still sees the old state; the live db sees the new.
        self.assertEqual(snap.get("users", 1)["name"], "alice")
        self.assertIsNone(snap.get("users", 10))
        self.assertIsNone(self.db.get("users", 1))
        self.assertEqual(verify(snap), [])
        self.assertEqual(verify(self.db), [])
        with self.assertRaises(FrozenSnapshotError):
            snap.insert("users", {"id": 99, "name": "mallory"})
        with self.assertRaises(FrozenSnapshotError):
            snap.begin("nope")

    def test_verifier_detects_corruption(self):
        users = self.db.tables["users"]
        users.index[999] = 12345  # dangling index entry
        problems = verify(self.db)
        self.assertTrue(any("999" in p for p in problems))


class FixtureEventTests(unittest.TestCase):
    def setUp(self):
        self.db = build_db()

    def test_duplicate_event_does_not_move_twice(self):
        event = Event("t1", "users", "id", 1, 10, 1)
        report = apply_events(self.db, [event, event])
        self.assertEqual(report.committed, ["t1"])
        self.assertEqual(len(report.duplicates), 1)
        self.assertEqual(self.db.get("users", 10)["name"], "alice")
        self.assertEqual(verify(self.db), [])

    def test_late_event_for_old_key_is_isolated(self):
        first = Event("t1", "users", "id", 1, 10, 5)
        late = Event("t2", "users", "id", 1, 99, 6)   # key 1 already gone
        report = apply_events(self.db, [first, late])
        self.assertEqual(report.committed, ["t1"])
        self.assertEqual(report.stale, [late])
        self.assertIsNone(self.db.get("users", 99))
        self.assertEqual(self.db.get("users", 10)["name"], "alice")

    def test_late_event_with_older_commit_seq_is_isolated(self):
        first = Event("t1", "users", "id", 1, 10, 5)
        late = Event("t2", "users", "id", 10, 15, 3)  # seq 3 < applied 5
        report = apply_events(self.db, [first, late])
        self.assertEqual(report.stale, [late])
        self.assertIsNone(self.db.get("users", 15))
        self.assertEqual(self.db.get("users", 10)["name"], "alice")

    def test_conflicting_fixture_transaction_fails_atomically(self):
        events = [
            Event("t1", "users", "id", 1, 10, 1),
            Event("t1", "users", "id", 3, 4, 2),  # 4 occupied -> whole txn fails
        ]
        report = apply_events(self.db, events)
        self.assertEqual(report.committed, [])
        self.assertIn("t1", report.failed)
        self.assertEqual(self.db.get("users", 1)["name"], "alice")
        self.assertIsNone(self.db.get("users", 10))
        self.assertEqual(verify(self.db), [])

    def test_full_fixture_end_to_end(self):
        db = build_db(user_ids=(1, 2, 3, 4, 5, 6))
        db.insert("orders", {"id": 102, "user_id": 2, "item": "lamp"})
        db.insert("orders", {"id": 103, "user_id": 4, "item": "mug"})
        snap = db.snapshot()

        report = apply_events(db, load_events(FIXTURE))

        self.assertEqual(report.committed, ["txn-001", "txn-002", "txn-003"])
        self.assertIn("txn-004", report.failed)          # occupied new key
        self.assertEqual(len(report.duplicates), 1)      # txn-001 redelivery
        self.assertEqual(len(report.stale), 2)           # txn-005, txn-006

        # Simple move, swap, and cycle all landed.
        self.assertEqual(db.get("users", 10)["name"], "alice")
        self.assertEqual(db.get("users", 3)["name"], "bob")
        self.assertEqual(db.get("users", 2)["name"], "carol")
        self.assertEqual(db.get("users", 5)["name"], "dave")
        self.assertEqual(db.get("users", 6)["name"], "erin")
        self.assertEqual(db.get("users", 4)["name"], "frank")
        # Failed transaction left no trace.
        self.assertIsNone(db.get("users", 11))
        # Foreign keys followed their entities.
        self.assertEqual(db.get("orders", 100)["user_id"], 10)
        self.assertEqual(db.get("orders", 101)["user_id"], 3)
        self.assertEqual(db.get("orders", 103)["user_id"], 5)
        # Every readable snapshot is consistent.
        self.assertEqual(verify(db), [])
        self.assertEqual(verify(snap), [])


if __name__ == "__main__":
    unittest.main()
