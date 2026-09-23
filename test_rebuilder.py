import logging
import unittest

logging.disable(logging.CRITICAL)

from cdc import Change, ChangeLog, SnapshotSource
from fixtures import late_change, sample_changes, sample_snapshot
from rebuilder import Rebuilder
from view_store import View, ViewStore


def make_store(snapshot):
    return ViewStore(View(version=1, watermark=snapshot.lsn, tables=snapshot.tables))


class RebuildAndSwapTest(unittest.TestCase):
    def test_rebuild_catches_up_and_swaps_atomically(self):
        snapshot = sample_snapshot()
        store = make_store(snapshot)
        log = ChangeLog(sample_changes())
        old = store.current()

        new_view = Rebuilder(store, SnapshotSource(snapshot), log).rebuild()

        self.assertIsNotNone(new_view)
        self.assertEqual(new_view.watermark, 104)
        self.assertIs(store.current(), new_view)
        self.assertEqual(new_view.version, old.version + 1)
        # snapshot + increments: acct1 updated, acct2 deleted, acct3 inserted
        self.assertEqual(new_view.get("accounts", 1), {"id": 1, "balance": 450})
        self.assertIsNone(new_view.get("accounts", 2))
        self.assertEqual(new_view.get("accounts", 3), {"id": 3, "balance": 200})
        self.assertEqual(new_view.get("orders", 10), {"id": 10, "status": "paid"})
        self.assertEqual(new_view.count("accounts"), 2)

    def test_increment_arriving_mid_rebuild_is_caught_before_publish(self):
        snapshot = sample_snapshot()
        store = make_store(snapshot)
        log = ChangeLog(sample_changes())
        fired = []

        def arrive():
            if not fired:
                fired.append(True)
                log.append(late_change())  # commit_seq 105 lands mid-rebuild

        log.on_tip_read = arrive
        new_view = Rebuilder(store, SnapshotSource(snapshot), log).rebuild()

        # the late increment must be included; never published at 104
        self.assertEqual(new_view.watermark, 105)
        self.assertEqual(new_view.get("accounts", 4), {"id": 4, "balance": 900})
        self.assertIs(store.current(), new_view)

    def test_not_caught_up_view_is_never_published(self):
        snapshot = sample_snapshot()
        store = make_store(snapshot)
        log = ChangeLog(sample_changes())
        stale = View(version=-1, watermark=102, tables={})
        published = store.publish_if_caught_up(stale, log.tip)
        self.assertFalse(published)
        self.assertEqual(store.current().version, 1)  # old view untouched


class NoDoubleCountTest(unittest.TestCase):
    def test_replayed_change_after_switch_is_dropped(self):
        snapshot = sample_snapshot()
        store = make_store(snapshot)
        log = ChangeLog(sample_changes())
        Rebuilder(store, SnapshotSource(snapshot), log).rebuild()

        before = store.current().count("accounts")
        for change in sample_changes():  # replay the whole fixture stream
            self.assertFalse(store.apply_live(change))
        self.assertEqual(store.current().count("accounts"), before)
        self.assertEqual(store.current().watermark, 104)

    def test_live_change_applies_exactly_once(self):
        snapshot = sample_snapshot()
        store = make_store(snapshot)
        change = sample_changes()[1]  # txn-102 inserts account 3
        self.assertTrue(store.apply_live(change))
        self.assertFalse(store.apply_live(change))  # duplicate delivery
        self.assertEqual(store.current().count("accounts"), 3)
        self.assertEqual(store.current().watermark, 102)


class OldReaderTest(unittest.TestCase):
    def test_old_reader_keeps_its_version_after_swap(self):
        snapshot = sample_snapshot()
        store = make_store(snapshot)
        log = ChangeLog(sample_changes())
        pinned = store.current()  # reader pins v1 before the rebuild

        Rebuilder(store, SnapshotSource(snapshot), log).rebuild()

        self.assertEqual(pinned.version, 1)
        self.assertEqual(pinned.watermark, 100)
        self.assertEqual(pinned.get("accounts", 1), {"id": 1, "balance": 500})
        self.assertIsNone(pinned.get("accounts", 3))
        self.assertNotEqual(store.current().version, pinned.version)


class FailureTest(unittest.TestCase):
    def test_snapshot_failure_keeps_old_view_available(self):
        snapshot = sample_snapshot()
        store = make_store(snapshot)

        class Boom:
            def take(self):
                raise RuntimeError("snapshot failed")

        result = Rebuilder(store, Boom(), ChangeLog(sample_changes())).rebuild()

        self.assertIsNone(result)
        self.assertEqual(store.current().version, 1)
        self.assertEqual(store.current().get("accounts", 1),
                         {"id": 1, "balance": 500})

    def test_malformed_increment_keeps_old_view_available(self):
        snapshot = sample_snapshot()
        store = make_store(snapshot)
        bad = Change("txn-x", "accounts", 1, None, {"id": 1}, 101)
        log = ChangeLog([bad])

        def explode(*args, **kwargs):
            raise RuntimeError("log read failed")

        log.read_since = explode
        result = Rebuilder(store, SnapshotSource(snapshot), log).rebuild()

        self.assertIsNone(result)
        self.assertEqual(store.current().version, 1)
        self.assertEqual(store.current().watermark, 100)


class ChangeSemanticsTest(unittest.TestCase):
    def test_insert_update_delete(self):
        view = View(version=1, watermark=0, tables={})
        view.apply(Change("t1", "t", 1, None, {"v": 1}, 1))          # insert
        view.apply(Change("t2", "t", 1, {"v": 1}, {"v": 2}, 2))       # update
        self.assertEqual(view.get("t", 1), {"v": 2})
        view.apply(Change("t3", "t", 1, {"v": 2}, None, 3))           # delete
        self.assertIsNone(view.get("t", 1))
        self.assertEqual(view.watermark, 3)

    def test_log_rejects_out_of_order_commit_seq(self):
        log = ChangeLog(sample_changes())
        with self.assertRaises(ValueError):
            log.append(Change("txn-bad", "t", 1, None, {}, 102))


if __name__ == "__main__":
    unittest.main()
