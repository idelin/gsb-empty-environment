"""End-to-end required scenario: pin old snapshot, publish new, GC, leases."""

import os
import threading
import unittest

from imagestore import ImageStore, LeaseInvalid, audit
from imagestore.events import (
    GC_AFTER_MARK_LOCK,
)
from imagestore.fixture import (
    Scenario,
    ThreadedSchedule,
    coord,
    digest_of,
)


class OldSnapshotScenarioTests(unittest.TestCase):
    def setUp(self):
        self.scenario = Scenario(os.path.join(self._workdir(), "scenario"))

    def _workdir(self):
        import tempfile
        path = tempfile.mkdtemp()
        self.addCleanup(lambda: __import__("shutil").rmtree(path, ignore_errors=True))
        return path

    def test_old_lease_blocks_gc_release_allows_collection(self):
        sc = self.scenario
        sc.publish_old()                       # build pinned old snapshot
        old_lease = sc.lease_old()
        sc.publish_new()                       # root 1.1 removes target-lib

        old_reader = sc.store.reader(old_lease)
        lib_coord = coord("target-lib", "2.1")
        self.assertTrue(old_reader.has_package(lib_coord))

        # GC while the old lease is live must keep every old object.
        first = sc.gc()
        self.assertEqual(first["deleted"], [])
        self.assertEqual(
            old_reader.read_archive(lib_coord),
            b"ARCHIVE target-lib 2.1\n",
        )
        # The current (new) snapshot must not contain target-lib.
        new_view = sc.store.open_snapshot("snap-new")
        self.assertNotIn(
            "target-lib",
            [c["name"] for c in new_view.coordinates()],
        )
        # Auditing both visible snapshots passes.
        result = audit.audit(os.path.join(sc.path, "repo"))
        self.assertTrue(result.ok, result.errors)

        old_lease.release()
        # Reading through the terminated lease now fails.
        with self.assertRaises(LeaseInvalid):
            old_reader.read_archive(lib_coord)

        second = sc.gc()
        deleted = set(second["deleted"])
        # target-lib meta and archive are now collectable; the old manifest
        # file itself remains (small, and harmless) but is no longer visible.
        manifest_entries = [
            p for sid, man in sc.store.snapshots.items()
            for p in man["packages"] if sid == "snap-old"
        ]
        lib_entry = next(
            p for p in sc.store.snapshots["snap-old"]["packages"]
            if p["name"] == "target-lib"
        )
        self.assertIn(lib_entry["archive_digest"], deleted)
        self.assertIn(lib_entry["meta_digest"], deleted)
        # root 1.0 is unreachable too; host packages are shared with new.
        root10 = next(
            p for p in sc.store.snapshots["snap-old"]["packages"]
            if p["name"] == "root" and p["version"] == "1.0"
        )
        self.assertIn(root10["archive_digest"], deleted)

        with self.assertRaises(LeaseInvalid):
            old_reader.read_archive(lib_coord)
        self.assertFalse(os.path.exists(
            os.path.join(sc.path, "repo", "objects",
                         lib_entry["archive_digest"][:2],
                         lib_entry["archive_digest"][2:])
        ))
        result = audit.audit(os.path.join(sc.path, "repo"))
        self.assertTrue(result.ok, result.errors)

    def test_leases_have_no_wall_clock_expiry(self):
        sc = self.scenario
        sc.publish_old()
        lease = sc.lease_old()
        import time
        time.sleep(0.02)  # wall clock passes; lease is still valid
        reader = sc.store.reader(lease)
        self.assertTrue(reader.has_package(coord("target-lib", "2.1")))
        lease.recover()                    # explicit recovery terminates it
        with self.assertRaises(LeaseInvalid):
            sc.store.reader(lease.token)

    def test_recovered_lease_terminates_across_restart(self):
        sc = self.scenario
        sc.publish_old()
        lease = sc.lease_old()
        token = lease.token
        sc.reopen()
        # The lease survives the process crash/restart.
        reader = sc.store.reader(token)
        self.assertTrue(reader.has_package(coord("target-lib", "2.1")))
        sc.store.recover_lease(token)
        sc.reopen()
        with self.assertRaises(LeaseInvalid):
            sc.store.reader(token)

    def test_concurrent_publish_after_mark_is_not_collected(self):
        """The key generational GC race, made deterministic by injection.

        Schedule: GC computes candidates while a *second* lease pins nothing;
        a snapshot published in the GC_AFTER_MARK_LOCK seam (lock not held)
        must survive the sweep, because each unlink re-tests liveness under
        the lock.
        """
        sc = self.scenario
        sc.publish_old()
        old_lease = sc.lease_old()
        sc.publish_new()
        old_lease.release()

        # At the post-mark seam, reacquire a lease on the *old* snapshot in a
        # background thread; its acquire must linearise before the sweep.
        schedule = ThreadedSchedule(
            sc.store.acquire_lease, args=("snap-old",)
        )
        sc.bus.once(GC_AFTER_MARK_LOCK, schedule.kick)
        result = sc.store.collect_garbage()
        new_lease = schedule.wait()
        self.assertIsNone(schedule.error)
        # Old objects must be retained: target-lib digest must not be deleted.
        lib = next(p for p in sc.store.snapshots["snap-old"]["packages"]
                   if p["name"] == "target-lib")
        self.assertNotIn(lib["archive_digest"], result["deleted"])
        # Reader works through the newly acquired lease.
        self.assertTrue(
            sc.store.reader(new_lease).has_package(coord("target-lib", "2.1"))
        )
        # After that lease is released, a later generation collects objects.
        new_lease.release()
        second = sc.store.collect_garbage()
        self.assertIn(lib["archive_digest"], second["deleted"])

    def test_release_and_gc_linearisation_under_threads(self):
        sc = self.scenario
        sc.publish_old()
        lease_a = sc.lease_old()
        sc.publish_new()

        # The release runs concurrently from the post-mark seam; depending on
        # thread scheduling its linearisation point lands during this sweep or
        # before the next generation.  In either case the repository stays
        # auditable and the old content is eventually collected.
        schedule = ThreadedSchedule(sc.store.release_lease, args=(lease_a.token,))
        sc.bus.once(GC_AFTER_MARK_LOCK, schedule.kick)
        result = sc.store.collect_garbage()
        schedule.wait()
        audit_result = audit.audit(os.path.join(sc.path, "repo"))
        self.assertTrue(audit_result.ok, audit_result.errors)
        final = sc.store.collect_garbage()
        lib = next(p for p in sc.store.snapshots["snap-old"]["packages"]
                   if p["name"] == "target-lib")
        self.assertFalse(os.path.exists(
            os.path.join(sc.path, "repo", "objects",
                         lib["archive_digest"][:2], lib["archive_digest"][2:])
        ))


if __name__ == "__main__":
    unittest.main()


class InFlightGcRootTests(unittest.TestCase):
    def setUp(self):
        import tempfile, shutil
        self.base = tempfile.mkdtemp()
        self.addCleanup(shutil.rmtree, self.base, ignore_errors=True)

    def test_gc_running_during_import_preserves_inflight_objects(self):
        import threading
        from imagestore.events import IMPORT_BEGIN_WINDOW
        from imagestore.content import digest_hex
        sc = Scenario(os.path.join(self.base, "sc"))
        sc.publish_old()
        sc.lease_old().release()
        sc.publish_new()

        store = sc.store
        gc_result = {}
        gc_done = threading.Event()

        def at_window(_detail):
            # Runs with the import lock released and import-begin durable.
            gc_result["result"] = store.collect_garbage()
            gc_done.set()

        sc.bus.on(IMPORT_BEGIN_WINDOW, at_window)
        meta = {"name": "late-add", "version": "3.0",
                "platform": "tgt-linux-x64",
                "run_requires": [], "host_requires": []}
        data = b"late package bytes"
        entry = store.import_package_bytes(
            meta, data, digest_hex(data), len(data)
        )
        self.assertTrue(gc_done.wait(5))
        # The in-flight meta digest must not have been deleted while imported.
        self.assertNotIn(entry["meta_digest"], gc_result["result"]["deleted"])
        self.assertTrue(store.objects.exists(entry["meta_digest"]))
        self.assertTrue(store.objects.exists(entry["archive_digest"]))
