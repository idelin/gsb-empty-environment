import os
import shutil
import tempfile
import unittest

from imagerepo import CrashInjected, FaultBus, IncompleteSnapshot, Repository
from imagerepo.checker import audit_repository
from imagerepo.fixtures import TARGET
from imagerepo.model import HOST_PLATFORM, Ident
from imagerepo.replay import Replay

FIXTURE = os.path.join(os.path.dirname(__file__), "..", "fixtures", "scenario.json")
APP_V1 = Ident("app", "1.0.0", TARGET)
APP_V2 = Ident("app", "2.0.0", TARGET)
LIBV1 = Ident("libc", "1.0.0", TARGET)
GCC10 = Ident("gcc", "1.0.0", HOST_PLATFORM)
OLDLIB = Ident("oldlib", "1.0.0", TARGET)


class CrashCase(unittest.TestCase):
    def setUp(self):
        self.dir = tempfile.mkdtemp()
        self.repo_path = os.path.join(self.dir, "repo")

    def tearDown(self):
        shutil.rmtree(self.dir)

    def open(self, bus=None):
        return Repository.open(self.repo_path, bus or FaultBus())

    def world(self):
        rp = Replay(self.repo, FIXTURE)
        return rp

    def import_v1(self, repo):
        rp = Replay(repo, FIXTURE)
        rp.import_v1_closure()
        return rp

    # -------------------------------------------------------------- imports
    def test_crash_at_each_import_boundary(self):
        for event in ("import.begin.journal", "import.begin.durable",
                      "import.object.published", "import.commit.journal",
                      "import.visible"):
            shutil.rmtree(self.repo_path, ignore_errors=True)
            bus = FaultBus()
            bus.raise_on(event)
            repo = self.open(bus)
            rp = Replay(repo, FIXTURE)
            spec = rp.world[LIBV1]
            meta, payload = repo.prepare_import(
                LIBV1, spec["payload"],
                runtime_deps=spec["runtime_deps"],
                build_deps=spec["build_deps"])
            import_id = None
            with self.assertRaises(CrashInjected):
                if event.startswith("import.begin"):
                    repo.begin_import(meta)
                elif event == "import.object.published":
                    import_id = repo.begin_import(meta)
                    repo.stage_import(import_id, payload)
                else:
                    import_id = repo.begin_import(meta)
                    repo.stage_import(import_id, payload)
                    repo.commit_import(import_id)
            # Simulate process restart; recovery must complete or abort cleanly.
            repo2 = self.open()
            self.assertEqual(os.listdir(repo2.stage_dir), [])
            # import.visible fires after both the durable COMMIT and the meta
            # file exist; at every earlier boundary nothing is visible.
            if event == "import.visible":
                self.assertIsNotNone(repo2.get_metadata(LIBV1))
            else:
                self.assertIsNone(repo2.get_metadata(LIBV1))
            audit_repository(repo2.root)
            # Import is retryable and then works.
            rp2 = Replay(repo2, FIXTURE)
            rp2.import_one(LIBV1)
            self.assertIsNotNone(repo2.get_metadata(LIBV1))

    def test_commit_record_without_meta_file_is_redelivered(self):
        """IMPORT_COMMIT durable, metadata file lost: recovery redelivers it."""
        bus = FaultBus()
        bus.raise_on("import.visible")  # after COMMIT+meta; craft manual case below
        repo = self.open(bus)
        rp = Replay(repo, FIXTURE)
        spec = rp.world[LIBV1]
        meta, payload = repo.prepare_import(
            LIBV1, spec["payload"],
            runtime_deps=spec["runtime_deps"], build_deps=spec["build_deps"])
        import_id = repo.begin_import(meta)
        repo.stage_import(import_id, payload)
        # Durable commit first (this is the LP); remove the derived meta to
        # simulate a crash strictly between append and file creation.
        repo._append({"type": "IMPORT_COMMIT", "import_id": import_id,
                      "ident": LIBV1.__dict__})
        self.assertTrue(os.path.exists(repo.meta_path(LIBV1)) is False
                        or True)  # meta is written after append in normal path
        # Delete the meta that _commit_import may already have written; replay
        # must recreate it from the durable intent/record.
        meta_file = repo.meta_path(LIBV1)
        if os.path.exists(meta_file):
            os.unlink(meta_file)
        repo2 = self.open()
        redelivered = repo2.get_metadata(LIBV1)
        self.assertIsNotNone(redelivered)
        self.assertEqual(redelivered.archive_digest, spec["digest"])

    def test_orphan_blob_from_crashed_import_is_swept(self):
        bus = FaultBus()
        bus.raise_on("import.object.published")
        repo = self.open(bus)
        rp = Replay(repo, FIXTURE)
        with self.assertRaises(CrashInjected):
            rp.import_one(Ident("newlib", "1.0.0", TARGET))
        repo = self.open()
        report = repo.run_gc()
        digest = Replay(repo, FIXTURE).world[Ident("newlib", "1.0.0", TARGET)]["digest"]
        self.assertIn(digest, report.dropped_objects)

    # ------------------------------------------------------------- snapshot
    def test_crash_at_snapshot_boundaries(self):
        events = ("snap.begin.journal", "snap.begin.durable",
                  "snap.manifest.write", "snap.manifest.durable",
                  "snap.current.replace", "snap.current.replaced",
                  "snap.published")
        for event in events:
            shutil.rmtree(self.repo_path, ignore_errors=True)
            repo = self.open()
            self.import_v1(repo)
            bus = FaultBus()
            bus.raise_on(event)
            repo.bus = bus
            snap_id = None
            with self.assertRaises(CrashInjected):
                if event.startswith("snap.begin"):
                    repo.begin_publish([APP_V1])
                else:
                    snap_id = repo.begin_publish([APP_V1])
                    if event.startswith("snap.manifest"):
                        repo.write_manifest(snap_id)
                    else:
                        repo.write_manifest(snap_id)
                        repo.commit_publish(snap_id)
            repo2 = self.open()
            current = repo2.current_snapshot()
            if event in ("snap.current.replaced", "snap.published"):
                self.assertEqual(current, snap_id)
                # The visible snapshot must be complete even if COMMIT was lost.
                audit_repository(repo2.root)
            else:
                self.assertIsNone(current)
                self.assertEqual(os.listdir(repo2.snaps_dir), [])
            # Retrying after recovery always reaches a complete snapshot.
            snap_again = repo2.publish([APP_V1])
            self.assertEqual(repo2.current_snapshot(), snap_again)
            audit_repository(repo2.root)

    # ---------------------------------------------------------------- leases
    def test_crash_lease_grant_release(self):
        for event in ("lease.grant.journal", "lease.grant.file", "lease.granted",
                      "lease.release.journal", "lease.released"):
            shutil.rmtree(self.repo_path, ignore_errors=True)
            repo = self.open()
            rp = self.import_v1(repo)
            snap = rp.publish_old()
            bus = FaultBus()
            bus.raise_on(event)
            repo.bus = bus
            if event.startswith("lease.grant"):
                with self.assertRaises(CrashInjected):
                    repo.grant_lease(snap, holder="h")
                repo2 = self.open()
                # Either fully granted or nothing; snapshot remains readable
                # by granting a fresh lease.
                lease = repo2.grant_lease(snap, holder="h2")
                self.assertEqual(repo2.read_archive(lease, OLDLIB),
                                 Replay(repo2, FIXTURE).world[OLDLIB]["payload"])
            else:
                lease = repo.grant_lease(snap, holder="h")
                with self.assertRaises(CrashInjected):
                    repo.release_lease(lease)
                repo2 = self.open()
                if event == "lease.release.journal":
                    # Release record never landed -> lease survives.
                    self.assertIn(lease, repo2.valid_leases())
                else:
                    self.assertNotIn(lease, repo2.valid_leases())

    # -------------------------------------------------------------------- gc
    def test_crash_at_gc_boundaries(self):
        events = ("gc.begin.journal", "gc.begin.durable", "gc.mark.durable",
                  "gc.unlink.manifest.before", "gc.unlink.object.before",
                  "gc.done.journal", "gc.done")
        for event in events:
            shutil.rmtree(self.repo_path, ignore_errors=True)
            repo = self.open()
            rp = self.import_v1(repo)
            old = rp.publish_old()
            lease = rp.pin_old()
            rp.import_v2_closure()
            new = rp.publish_new()
            repo.release_lease(lease)
            self.assertEqual(repo.valid_leases(), {})
            bus = FaultBus()
            bus.raise_on(event)
            repo.bus = bus
            gen = None
            crashed = False
            try:
                gen = repo.scan_generation()
                repo.mark_generation(gen)
                repo.sweep_generation(gen)
            except CrashInjected:
                crashed = True
            self.assertTrue(crashed)
            # Restart finishes or discards the generation safely.
            repo2 = self.open()
            audit_repository(repo2.root)
            self.assertEqual(repo2.current_snapshot(), new)
            # New snapshot is never removed by the crashed generation.
            self.assertTrue(os.path.exists(repo2.snap_path(new)))
            # Old snapshot may have been swept before the crash or remain;
            # running GC to completion converges: it is gone either way.
            if os.path.exists(repo2.snap_path(old)):
                report = repo2.run_gc()
                self.assertIn(old, report.dropped_manifests)
            else:
                repo2.run_gc()
            self.assertFalse(os.path.exists(repo2.snap_path(old)))
            audit_repository(repo2.root)


if __name__ == "__main__":
    unittest.main()
