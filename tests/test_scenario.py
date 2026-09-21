import os
import shutil
import tempfile
import unittest

from imagerepo import Repository
from imagerepo.checker import audit_repository
from imagerepo.fixtures import TARGET, load_json
from imagerepo.model import HOST_PLATFORM, Ident
from imagerepo.replay import Replay


class ScenarioTests(unittest.TestCase):
    """The required scenario: old snapshot pinned while new snapshot + GC run."""

    def setUp(self):
        self.dir = tempfile.mkdtemp()
        self.repo = Repository.open(os.path.join(self.dir, "repo"))
        self.world = load_json(
            os.path.join(os.path.dirname(__file__), "..", "fixtures", "scenario.json"))
        self.rp = Replay(self.repo, os.path.join(
            os.path.dirname(__file__), "..", "fixtures", "scenario.json"))

    def tearDown(self):
        shutil.rmtree(self.dir)

    def import_world(self):
        for ident in self.world:
            self.rp.import_one(ident)

    def test_required_lifecycle(self):
        # 1. Root package with a target dep and a host build tool.
        self.rp.import_v1_closure()
        old = self.rp.publish_old()
        lease = self.rp.pin_old(holder="reader-a")

        # 2. Import v2 closure, publish new snapshot, run a GC generation.
        self.rp.import_v2_closure()
        new = self.rp.publish_new()
        self.assertNotEqual(old, new)
        report = self.repo.run_gc()
        self.assertEqual(report.dropped_objects, [])
        self.assertEqual(report.dropped_manifests, [])

        # 3. Old lease still valid: old package reads must succeed.
        oldlib = Ident("oldlib", "1.0.0", TARGET)
        payload = self.repo.read_archive(lease, oldlib)
        self.assertEqual(payload, self.world[oldlib]["payload"])
        host_tool = Ident("gcc", "1.0.0", HOST_PLATFORM)
        self.assertTrue(self.repo.read_archive(lease, host_tool))

        # 4. Only after release may the old snapshot and its private blobs go.
        self.repo.release_lease(lease)
        report = self.repo.run_gc()
        self.assertIn(old, report.dropped_manifests)
        old_private = {
            self.world[oldlib]["digest"],
            self.world[Ident("app", "1.0.0", TARGET)]["digest"],
            self.world[host_tool]["digest"],
        }
        self.assertTrue(old_private.issubset(set(report.dropped_objects)))
        # Shared libc blob survives.
        libc_digest = self.world[Ident("libc", "1.0.0", TARGET)]["digest"]
        self.assertTrue(os.path.exists(self.repo.object_path(libc_digest)))
        self.assertFalse(os.path.exists(self.repo.snap_path(old)))

        audit_repository(self.repo.root)  # raises on any violation

    def test_declared_digest_length_verified(self):
        spec = self.world[Ident("libc", "1.0.0", TARGET)]
        with self.assertRaises(Exception):
            self.repo.import_package(
                Ident("libc", "1.0.0", TARGET), spec["payload"],
                declared_digest="0" * 64)
        with self.assertRaises(Exception):
            self.repo.import_package(
                Ident("libc", "1.0.0", TARGET), spec["payload"],
                declared_length=spec["length"] + 1)

    def test_same_digest_shared_inode(self):
        # Import libc under a second identity but identical blob: one inode.
        spec = self.world[Ident("libc", "1.0.0", TARGET)]
        alias = Ident("libc-alias", "1.0.0", TARGET)
        self.repo.import_package(alias, spec["payload"])
        self.repo.import_package(Ident("libc", "1.0.0", TARGET), spec["payload"])
        p1 = self.repo.object_path(spec["digest"])
        self.assertEqual(os.stat(p1).st_nlink, 1)  # same content -> single blob
        self.assertEqual(os.path.getsize(p1), spec["length"])

    def test_metadata_identity_immutable(self):
        spec = self.world[Ident("libc", "1.0.0", TARGET)]
        self.repo.import_package(
            Ident("libc", "1.0.0", TARGET), spec["payload"],
            runtime_deps=[])
        from imagerepo import ImmutabilityViolation
        with self.assertRaises(ImmutabilityViolation):
            self.repo.import_package(
                Ident("libc", "1.0.0", TARGET), spec["payload"],
                runtime_deps=[("x", "1")] if False else
                [__import__("imagerepo.model", fromlist=["Dep"]).Dep("x", "1")])

    def test_incomplete_publish_reports_gaps_and_paths(self):
        # Only the host tool is missing for app v1.
        self.rp.import_one(Ident("libc", "1.0.0", TARGET))
        self.rp.import_one(Ident("oldlib", "1.0.0", TARGET))
        self.rp.import_one(Ident("app", "1.0.0", TARGET))
        from imagerepo import IncompleteSnapshot
        try:
            self.repo.publish([Ident("app", "1.0.0", TARGET)])
        except IncompleteSnapshot as exc:
            kinds = {g.kind for g in exc.gaps}
            self.assertIn("metadata", kinds)
            gcc_gap = next(g for g in exc.gaps
                           if g.ident == Ident("gcc", "1.0.0", HOST_PLATFORM))
            self.assertEqual(gcc_gap.path[0], Ident("app", "1.0.0", TARGET))
            self.assertEqual(gcc_gap.path[-1], Ident("gcc", "1.0.0", HOST_PLATFORM))
        else:
            self.fail("expected IncompleteSnapshot")
        # No manifest may reference an unfinishable snapshot.
        self.assertEqual(os.listdir(self.repo.snaps_dir), [])
        self.assertIsNone(self.repo.current_snapshot())

    def test_inflight_import_is_gc_root(self):
        self.rp.import_v1_closure()
        old = self.rp.publish_old()
        self.repo.grant_lease(old, holder="r")
        # Begin an import of an unreferenced object, then GC while in-flight.
        spec = self.world[Ident("newlib", "1.0.0", TARGET)]
        meta, payload = self.repo.prepare_import(
            Ident("newlib", "1.0.0", TARGET), spec["payload"])
        import_id = self.repo.begin_import(meta)
        self.repo.stage_import(import_id, payload)
        gen = self.repo.scan_generation()
        self.repo.mark_generation(gen)
        report = self.repo.sweep_generation(gen)
        self.assertNotIn(spec["digest"], report.dropped_objects)
        self.repo.commit_import(import_id)
        # Once committed but still unreferenced, a later generation drops it.
        report = self.repo.run_gc()
        self.assertIn(spec["digest"], report.dropped_objects)

    def test_lease_does_not_expire_on_wall_clock(self):
        self.rp.import_v1_closure()
        snap = self.rp.publish_old()
        lease = self.repo.grant_lease(snap, holder="slow")
        import time
        time.sleep(0.01)  # no timeout of any kind
        self.assertEqual(self.repo.lease_snapshot(lease), snap)
        reaped = self.repo.reap_leases("slow")
        self.assertEqual(reaped, [lease])
        from imagerepo import LeaseInvalid
        with self.assertRaises(LeaseInvalid):
            self.repo.lease_snapshot(lease)

    def test_same_digest_shares_single_blob(self):
        """Two distinct package identities with identical bytes share one blob."""
        payload = b"identical-bytes\n"
        a = Ident("shared-a", "9.0", TARGET)
        b = Ident("shared-b", "9.0", TARGET)
        self.repo.import_package(a, payload)
        self.repo.import_package(b, payload)
        digest = __import__("hashlib").sha256(payload).hexdigest()
        self.assertTrue(os.path.exists(self.repo.object_path(digest)))
        blobs = []
        for _dp, _d, files in os.walk(self.repo.objects_dir):
            blobs.extend(files)
        self.assertEqual(blobs.count(digest), 1)
        snap_a = self.repo.publish([a])
        lease_a = self.repo.grant_lease(snap_a)
        snap_b = self.repo.publish([b])
        lease_b = self.repo.grant_lease(snap_b)
        self.assertEqual(self.repo.read_archive(lease_a, a), payload)
        self.assertEqual(self.repo.read_archive(lease_b, b), payload)

    def test_repo_closure_matches_independent_oracle(self):
        """Every visible snapshot: the repo resolver and the independent
        closure oracle agree on both closure sets."""
        from imagerepo.closure import compute_closure
        from imagerepo.model import Ident as I
        self.rp.import_v1_closure()
        self.rp.publish_old()
        self.rp.import_v2_closure()
        self.rp.publish_new()
        for snap_file in sorted(os.listdir(self.repo.snaps_dir)):
            snap_id = snap_file[:-5]
            roots = self.repo.snapshot_roots(snap_id)
            rt, bd, gaps = self.repo._resolve_closure(roots)
            self.assertEqual(gaps, [])

            def get_meta(ident):
                return self.repo.get_metadata(ident)

            def has_object(digest, length):
                return self.repo.has_object(digest, length)

            oracle = compute_closure(roots, get_meta, has_object)
            self.assertTrue(oracle.ok, oracle.gaps)
            self.assertEqual(rt, oracle.runtime)
            self.assertEqual(bd, oracle.build)


if __name__ == "__main__":
    unittest.main()
