import os
import shutil
import tempfile
import threading
import unittest

from imagerepo import FaultBus, Repository
from imagerepo.fixtures import TARGET
from imagerepo.model import HOST_PLATFORM, Ident
from imagerepo.replay import Replay

FIXTURE = os.path.join(os.path.dirname(__file__), "..", "fixtures", "scenario.json")
APP_V1 = Ident("app", "1.0.0", TARGET)
APP_V2 = Ident("app", "2.0.0", TARGET)
NEWLIB = Ident("newlib", "1.0.0", TARGET)


class ConcurrencyTests(unittest.TestCase):
    def setUp(self):
        self.dir = tempfile.mkdtemp()
        self.repo = Repository.open(os.path.join(self.dir, "repo"))
        rp = Replay(self.repo, FIXTURE)
        rp.import_v1_closure()
        self.old = rp.publish_old()
        self.lease = self.repo.grant_lease(self.old, holder="concurrent-reader")

    def tearDown(self):
        shutil.rmtree(self.dir)

    def test_post_mark_publish_is_not_swept_by_current_generation(self):
        """A snapshot committed after the mark survives: its SNAP_BEGIN intent
        (with the full object set) was visible to the GC scan fence."""
        rp = Replay(self.repo, FIXTURE)
        rp.import_v2_closure()

        # Intent registered BEFORE the fence; object set pinned from scan on.
        snap_new = self.repo.begin_publish([APP_V2])
        gen = self.repo.scan_generation()

        errors = []

        def publish():
            try:
                self.repo.write_manifest(snap_new)
                self.repo.commit_publish(snap_new)
            except Exception as exc:  # pragma: no cover
                errors.append(exc)

        t = threading.Thread(target=publish)
        t.start()
        t.join(timeout=0.2)
        self.repo.mark_generation(gen)
        report = self.repo.sweep_generation(gen)
        t.join(timeout=2)
        self.assertFalse(t.is_alive())
        self.assertEqual(errors, [])
        # New snapshot and all its closure objects remain.
        self.assertEqual(self.repo.current_snapshot(), snap_new)
        lease = self.repo.grant_lease(snap_new)
        self.assertEqual(self.repo.read_archive(lease, NEWLIB),
                         rp.world[NEWLIB]["payload"])
        # The old snapshot is retained by the still-active lease.
        self.assertEqual(report.dropped_manifests, [])

    def test_begin_inside_gc_window_waits_for_generation(self):
        """A SNAP_BEGIN attempted between fence and DONE blocks until DONE.

        Re-publishing the *same* content cannot reference a swept object, so
        it succeeds once the gate opens; this isolates the blocking behavior.
        """
        gen = self.repo.scan_generation()
        done = []
        errors = []

        def publish():
            try:
                done.append(self.repo.publish([APP_V1]))
            except Exception as exc:
                errors.append(exc)

        t = threading.Thread(target=publish)
        t.start()
        t.join(timeout=0.2)
        self.assertTrue(t.is_alive())
        self.repo.mark_generation(gen)
        self.repo.sweep_generation(gen)
        t.join(timeout=2)
        self.assertFalse(t.is_alive())
        self.assertEqual(errors, [])
        self.assertEqual(done[0], self.old)
        self.assertEqual(self.repo.current_snapshot(), self.old)

    def test_new_roots_after_fence_report_content_gap(self):
        """If a begin is delayed past the fence, objects this generation
        legitimately sweeps cannot be resurrected: a content gap is reported."""
        rp = Replay(self.repo, FIXTURE)
        rp.import_v2_closure()
        gen = self.repo.scan_generation()
        self.repo.mark_generation(gen)
        self.repo.sweep_generation(gen)
        from imagerepo import IncompleteSnapshot
        with self.assertRaises(IncompleteSnapshot) as ctx:
            self.repo.publish([APP_V2])
        self.assertTrue(any(g.kind == "content" for g in ctx.exception.gaps))

    def test_inflight_import_concurrent_with_sweep_is_retained(self):
        """An import beginning after the GC scan re-pins its object."""
        rp = Replay(self.repo, FIXTURE)
        spec = rp.world[NEWLIB]
        gen = self.repo.scan_generation()  # newlib blob not even imported yet
        self.repo.mark_generation(gen)

        errors = []

        def do_import():
            try:
                self.repo.import_package(NEWLIB, spec["payload"])
            except Exception as exc:  # pragma: no cover
                errors.append(exc)

        t = threading.Thread(target=do_import)
        t.start()
        t.join(timeout=0.5)
        report = self.repo.sweep_generation(gen)
        t.join(timeout=2)
        self.assertFalse(errors)
        self.assertNotIn(spec["digest"], report.dropped_objects)
        self.assertTrue(os.path.exists(self.repo.object_path(spec["digest"])))

    def test_release_concurrent_with_scan_keeps_lease_snapshot(self):
        """Lease released after the scan LP is still retained this generation;
        a generation whose fence is after the release collects it."""
        rp = Replay(self.repo, FIXTURE)
        rp.import_v2_closure()
        snap_new = self.repo.publish([APP_V2])
        # Release the setup lease, then re-pin at a controlled point.
        self.repo.release_lease(self.lease)
        lease = self.repo.grant_lease(self.old, holder="phase")
        gen = self.repo.scan_generation()  # lease valid at fence
        self.repo.release_lease(lease)
        drop_objects, drop_manifests = self.repo.mark_generation(gen)
        self.assertNotIn(self.old, drop_manifests)
        report = self.repo.sweep_generation(gen)
        self.assertNotIn(self.old, report.dropped_manifests)
        report = self.repo.run_gc()
        self.assertIn(self.old, report.dropped_manifests)
        self.assertNotIn(snap_new, report.dropped_manifests)


if __name__ == "__main__":
    unittest.main()
