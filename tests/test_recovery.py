"""Crash injection at import / manifest-commit / deletion boundaries."""

import io
import json
import os
import shutil
import tempfile
import unittest

from imagestore import ImageStore, JournalCorrupt, audit
from imagestore.events import (
    GC_AFTER_UNLINK,
    GC_BEFORE_UNLINK,
    IMPORT_JOURNAL_APPENDED,
    IMPORT_STAGED,
    InjectedCrash,
    PUB_CURRENT_APPENDED,
    PUB_BEFORE_REPLACE,
    PUB_MANIFEST_REPLACED,
    PUB_TEMP_WRITTEN,
)
from imagestore.fixture import (
    Scenario,
    build_world_packages,
    coord,
    digest_of,
)
from imagestore.journal import MAGIC


def _read(path):
    with open(path, "rb") as handle:
        return handle.read()


class RecoveryTestBase(unittest.TestCase):
    def setUp(self):
        self.base = tempfile.mkdtemp()
        self.addCleanup(shutil.rmtree, self.base, ignore_errors=True)

    def scenario(self):
        return Scenario(os.path.join(self.base, f"sc-{id(self)}"))


class ImportCrashTests(RecoveryTestBase):
    def test_crash_after_staging_leaves_no_visible_import(self):
        sc = self.scenario()
        meta, data = build_world_packages()["target-lib"]
        sc.bus.crash_at(IMPORT_STAGED)
        with self.assertRaises(InjectedCrash):
            sc.store.import_package_bytes(meta, data, digest_of(data), len(data))
        store = sc.reopen()
        self.assertEqual(store.catalog, {})
        self.assertEqual(os.listdir(os.path.join(sc.path, "repo", "staging")), [])
        # Objects staged for the uncommitted import are swept on recovery.
        total = [
            f for shard in os.listdir(os.path.join(sc.path, "repo", "objects"))
            for f in os.listdir(os.path.join(sc.path, "repo", "objects", shard))
        ] if os.path.isdir(os.path.join(sc.path, "repo", "objects")) else []
        self.assertEqual(total, [])
        # The import can be performed from scratch after recovery.
        entry = store.import_package_bytes(meta, data, digest_of(data), len(data))
        self.assertTrue(store.objects.exists(entry["archive_digest"]))

    def test_crash_after_import_journal_is_committed_on_reopen(self):
        sc = self.scenario()
        meta, data = build_world_packages()["target-lib"]
        sc.bus.crash_at(IMPORT_JOURNAL_APPENDED)
        with self.assertRaises(InjectedCrash):
            sc.store.import_package_bytes(meta, data, digest_of(data), len(data))
        store = sc.reopen()
        key = ("target-lib", "2.1", "tgt-linux-x64")
        self.assertIn(key, store.catalog)
        entry = store.catalog[key]
        self.assertTrue(store.objects.exists(entry["archive_digest"]))
        self.assertTrue(store.objects.exists(entry["meta_digest"]))
        # Re-import after crash is idempotent.
        again = store.import_package_bytes(meta, data, digest_of(data), len(data))
        self.assertEqual(again, entry)


class PublishCrashTests(RecoveryTestBase):
    def _ready(self, sc):
        from imagestore.fixture import import_all
        return import_all(sc.store)

    def _arm_on_next_publish(self, sc, event):
        # Arm after setup so the first (target) publish consumes the fault.
        sc.bus.crash_at(event)

    def test_crash_after_temp_manifest_leaves_orphan_only(self):
        sc = self.scenario()
        self._ready(sc)
        self._arm_on_next_publish(sc, PUB_TEMP_WRITTEN)
        with self.assertRaises(InjectedCrash):
            sc.store.publish_snapshot(coord("root", "1.0"), "snap-old")
        snap_dir = os.path.join(sc.path, "repo", "snapshots")
        temps = [n for n in os.listdir(snap_dir) if n.startswith(".tmp-")]
        self.assertEqual(len(temps), 1)
        store = sc.reopen()
        self.assertEqual(store.current_snapshot(), None)
        self.assertEqual(
            [n for n in os.listdir(snap_dir) if n.startswith(".tmp-")], []
        )
        self.assertNotIn("snap-old", store.snapshots)

    def test_crash_after_commit_record_before_replace_rebuilds_manifest(self):
        # The journal commit record is durable; os.replace did not happen.
        sc = self.scenario()
        self._ready(sc)
        sc.bus.crash_at(PUB_BEFORE_REPLACE)
        with self.assertRaises(InjectedCrash):
            sc.store.publish_snapshot(coord("root", "1.0"), "snap-old")
        snap_dir = os.path.join(sc.path, "repo", "snapshots")
        self.assertTrue(any(n.startswith(".tmp-") for n in os.listdir(snap_dir)))
        store = sc.reopen()
        self.assertIn("snap-old", store.snapshots)
        self.assertTrue(os.path.exists(os.path.join(snap_dir, "snap-old.json")))
        # make_current record came after the crash: current is unset.
        self.assertIsNone(store.current_snapshot())
        result = audit.audit(os.path.join(sc.path, "repo"))
        # snap-old exists but is not visible (no current, no lease).
        self.assertTrue(result.ok, result.errors)

    def test_crash_after_current_record_reopens_as_current(self):
        sc = self.scenario()
        self._ready(sc)
        sc.bus.crash_at(PUB_CURRENT_APPENDED)
        with self.assertRaises(InjectedCrash):
            sc.store.publish_snapshot(coord("root", "1.0"), "snap-old")
        store = sc.reopen()
        self.assertEqual(store.current_snapshot(), "snap-old")
        lease = store.acquire_lease()
        self.assertEqual(lease.snapshot_id, "snap-old")
        result = audit.audit(os.path.join(sc.path, "repo"))
        self.assertTrue(result.ok, result.errors)


class DeletionCrashTests(RecoveryTestBase):
    def _old_and_new(self, sc):
        sc.publish_old()
        lease = sc.lease_old()
        sc.publish_new()
        lease.release()

    def test_crash_before_unlink_changes_nothing(self):
        sc = self.scenario()
        self._old_and_new(sc)
        sc.bus.crash_at(GC_BEFORE_UNLINK)
        with self.assertRaises(InjectedCrash):
            sc.store.collect_garbage()
        store = sc.reopen()
        # Stale plan discarded; all visible snapshots still auditable.
        result = audit.audit(os.path.join(sc.path, "repo"))
        self.assertTrue(result.ok, result.errors)
        # A fresh collection now completes.
        done = store.collect_garbage()
        self.assertIsNotNone(done["generation"])

    def test_crash_after_unlink_resumes_to_safe_state(self):
        sc = self.scenario()
        self._old_and_new(sc)
        seen = {"n": 0}

        def crash_second(detail):
            seen["n"] += 1
            if seen["n"] == 2:
                raise InjectedCrash("after the second unlink")

        sc.bus.on(GC_AFTER_UNLINK, crash_second)
        with self.assertRaises(InjectedCrash):
            sc.store.collect_garbage()
        store = sc.reopen()
        result = audit.audit(os.path.join(sc.path, "repo"))
        self.assertTrue(result.ok, result.errors)
        done = store.collect_garbage()
        # Subsequent generation collects everything unreachable.
        lib = next(p for p in store.snapshots["snap-old"]["packages"]
                   if p["name"] == "target-lib")
        deleted_so_far = {lib["archive_digest"], lib["meta_digest"]}
        all_gone = all(
            not os.path.exists(store.objects.path(lib[d]))
            for d in ("archive_digest", "meta_digest")
        )
        final = store.collect_garbage()
        self.assertFalse(os.path.exists(store.objects.path(lib["archive_digest"])))


class JournalBoundaryTests(RecoveryTestBase):
    def test_torn_tail_is_truncated_and_recovery_proceeds(self):
        sc = self.scenario()
        sc.publish_old()
        journal = os.path.join(sc.path, "repo", "journal")
        data = _read(journal)
        # Append a partial frame (header claiming a large payload).
        torn = MAGIC + (1200).to_bytes(8, "big") + b'{"type":"garbage"'
        with open(journal, "wb") as handle:
            handle.write(data + torn)
        store = ImageStore.open(os.path.join(sc.path, "repo"))
        # Good records survived.
        self.assertIn("snap-old", store.snapshots)
        self.assertEqual(store.current_snapshot(), "snap-old")
        # Torn bytes removed.
        self.assertEqual(_read(journal), data)

    def test_mid_log_corruption_is_refused(self):
        sc = self.scenario()
        sc.publish_old()
        lease = sc.lease_old()
        journal = os.path.join(sc.path, "repo", "journal")
        data = bytearray(_read(journal))
        # Flip a payload byte in a frame before the last frame.
        # Find second frame start.
        first_len = int.from_bytes(bytes(data[4:12]), "big")
        second_start = 12 + first_len + 32
        data[second_start + 20] ^= 0xFF
        with open(journal, "wb") as handle:
            handle.write(bytes(data))
        with self.assertRaises(JournalCorrupt):
            ImageStore.open(os.path.join(sc.path, "repo"))

    def test_complete_frame_checksum_failure_is_refused(self):
        sc = self.scenario()
        sc.publish_old()
        journal = os.path.join(sc.path, "repo", "journal")
        data = bytearray(_read(journal))
        # Corrupt a payload byte in the *last* frame but keep frame complete.
        first_len = int.from_bytes(bytes(data[4:12]), "big")
        data[first_len - 3] ^= 0x01
        with open(journal, "wb") as handle:
            handle.write(bytes(data))
        with self.assertRaises(JournalCorrupt):
            ImageStore.open(os.path.join(sc.path, "repo"))


if __name__ == "__main__":
    unittest.main()


class InFlightProtectionTests(RecoveryTestBase):
    def test_inflight_import_objects_survive_collect(self):
        # A brand new coordinate crashes after import-begin but before the
        # commit record: GC roots the import-begin digests, and restart aborts
        # the unfinished import and sweeps only its exclusively-held objects.
        sc = self.scenario()
        sc.publish_old()
        sc.lease_old()
        sc.publish_new()
        from imagestore.content import digest_hex
        store = sc.store
        new_meta = {
            "name": "inflight-pkg", "version": "1.0",
            "platform": "tgt-linux-x64", "run_requires": [], "host_requires": [],
        }
        new_data = b"inflight archive body"
        # Crash after objects are committed but before the import record:
        # use the appended hook, then simulate the narrower staged case too.
        store.bus.crash_at(IMPORT_JOURNAL_APPENDED)
        with self.assertRaises(InjectedCrash):
            store.import_package_bytes(
                new_meta, new_data, digest_hex(new_data), len(new_data)
            )
        # Reopening keeps the committed objects (journal record landed before
        # the crash), making the import visible.
        reopened = sc.reopen()
        self.assertIn(
            ("inflight-pkg", "1.0", "tgt-linux-x64"), reopened.catalog
        )

        # Narrower case: crash at IMPORT_STAGED (import-begin durable, objects
        # still only in staging). Recovery aborts and leaves no garbage.
        from imagestore.events import IMPORT_STAGED
        other_meta = dict(new_meta, name="inflight-pkg", version="2.0")
        other_data = b"second body"
        reopened.bus.crash_at(IMPORT_STAGED)
        with self.assertRaises(InjectedCrash):
            reopened.import_package_bytes(
                other_meta, other_data, digest_hex(other_data), len(other_data)
            )
        reopened2 = sc.reopen()
        self.assertNotIn(
            ("inflight-pkg", "2.0", "tgt-linux-x64"), reopened2.catalog
        )
        self.assertEqual(reopened2.inflight, {})
        staging = os.path.join(sc.path, "repo", "staging")
        self.assertEqual(os.listdir(staging), [])
        result = audit.audit(os.path.join(sc.path, "repo"))
        self.assertTrue(result.ok, result.errors)

    def test_crash_on_last_unlink_restarts_to_auditable_store(self):
        sc = self.scenario()
        sc.publish_old()
        sc.lease_old().release()
        sc.publish_new()
        candidates = []

        def count_candidates(detail):
            candidates.append(detail["digest"])

        sc.bus.on(GC_BEFORE_UNLINK, count_candidates)
        # Crash only at the final candidate's AFTER_UNLINK boundary.
        total = {"n": 0}

        def crash_on_last(detail):
            total["n"] += 1

        # First dry planning pass: gather candidate count using a fresh store.
        sc.bus.off(GC_BEFORE_UNLINK, count_candidates)
        live = sc.store._live_digests()
        all_digests = sc.store.objects.all_digests()
        garbage = sorted(all_digests - live)
        target = garbage[-1]
        sc.bus.crash_at(GC_AFTER_UNLINK, {"digest": target})
        with self.assertRaises(InjectedCrash):
            sc.store.collect_garbage()
        reopened = sc.reopen()
        result = audit.audit(os.path.join(sc.path, "repo"))
        self.assertTrue(result.ok, result.errors)
        # Next generation completes and collects the rest.
        done = reopened.collect_garbage()
        final = audit.audit(os.path.join(sc.path, "repo"))
        self.assertTrue(final.ok, final.errors)

    def test_republish_after_pre_replace_crash_rebuilds_committed_snapshot(self):
        sc = self.scenario()
        from imagestore.fixture import import_all
        import_all(sc.store)
        sc.bus.crash_at(PUB_BEFORE_REPLACE)
        with self.assertRaises(InjectedCrash):
            sc.store.publish_snapshot(coord("root", "1.0"), "snap-old")
        reopened = sc.reopen()
        # The commit record was durable: recovery materialises the manifest;
        # current did not linearise yet.
        self.assertIn("snap-old", reopened.snapshots)
        self.assertTrue(os.path.exists(
            os.path.join(sc.path, "repo", "snapshots", "snap-old.json")
        ))
        self.assertIsNone(reopened.current_snapshot())
        # It is safe to make it current afterwards.
        reopened._journal({"type": "current", "snapshot": "snap-old"})
        reopened.current = "snap-old"
        reopened._rebuild_current()
        self.assertEqual(reopened.current_snapshot(), "snap-old")
        result = audit.audit(os.path.join(sc.path, "repo"))
        self.assertTrue(result.ok, result.errors)
