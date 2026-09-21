"""Missing-item classification, independent audit and mark/new-publish races."""

import json
import os
import shutil
import tempfile
import unittest

from imagestore import ImageStore, audit
from imagestore.errors import ClosureIncomplete
from imagestore.events import GC_AFTER_MARK_LOCK
from imagestore.fixture import (
    Scenario,
    ThreadedSchedule,
    coord,
    import_all,
)


class MissingItemTests(unittest.TestCase):
    def setUp(self):
        self.base = tempfile.mkdtemp()
        self.addCleanup(shutil.rmtree, self.base, ignore_errors=True)
        self.store = ImageStore.create(os.path.join(self.base, "repo"))
        import_all(self.store)

    def _issue_kinds(self):
        with self.assertRaises(ClosureIncomplete) as ctx:
            self.store.publish_snapshot(coord("root", "1.0"), "never")
        return {
            (i["kind"], i["coordinate"]["name"]): i
            for i in ctx.exception.issues
        }

    def test_content_missing_reported_with_path(self):
        entry = self.store.catalog[("target-lib", "2.1", "tgt-linux-x64")]
        os.unlink(self.store.objects.path(entry["archive_digest"]))
        issues = self._issue_kinds()
        issue = issues[("content-missing", "target-lib")]
        self.assertIn("root==1.0@", issue["path"])
        self.assertIn("target-lib==2.1@", issue["path"])

    def test_digest_mismatch_reported_with_path(self):
        entry = self.store.catalog[("host-binutils", "2.41", "host-macos-arm64")]
        path = self.store.objects.path(entry["archive_digest"])
        with open(path, "wb") as handle:
            handle.write(b"corrupted bytes on disk")
        issues = self._issue_kinds()
        issue = issues[("digest-mismatch", "host-binutils")]
        self.assertIn("host-gcc==9.4@", issue["path"])
        self.assertIn("run_requires", issue["path"])
        self.assertIn("host-binutils==2.41@", issue["path"])

    def test_metadata_missing_distinct_from_content(self):
        # Remove the catalog entry as if the package was never imported,
        # while objects happen to linger: still metadata-missing.
        self.store.catalog.pop(("target-lib", "2.1", "tgt-linux-x64"))
        issues = self._issue_kinds()
        self.assertIn(("metadata-missing", "target-lib"), issues)
        # Independently of that, corrupting a still-reachable transitive host
        # package produces a digest-mismatch while a target dep removed from
        # the catalog stays metadata-missing.
        bin_entry = self.store.catalog[
            ("host-binutils", "2.41", "host-macos-arm64")
        ]
        with open(self.store.objects.path(bin_entry["archive_digest"]), "wb") as h:
            h.write(b"x")
        with self.assertRaises(ClosureIncomplete) as ctx:
            self.store.publish_snapshot(coord("root", "1.0"), "never2")
        by_kind = {}
        for issue in ctx.exception.issues:
            by_kind.setdefault(issue["kind"], set()).add(
                issue["coordinate"]["name"]
            )
        self.assertEqual(by_kind["metadata-missing"], {"target-lib"})
        self.assertEqual(by_kind["digest-mismatch"], {"host-binutils"})


class IndependentAuditTests(unittest.TestCase):
    def setUp(self):
        self.base = tempfile.mkdtemp()
        self.addCleanup(shutil.rmtree, self.base, ignore_errors=True)

    def test_every_visible_snapshot_complete_and_immutable(self):
        sc = Scenario(os.path.join(self.base, "sc"))
        sc.publish_old()
        sc.lease_old()
        sc.publish_new()
        root = os.path.join(sc.path, "repo")

        result = audit.audit(root)
        self.assertTrue(result.ok, result.errors)
        self.assertEqual(set(result.visible_snapshots), {"snap-old", "snap-new"})
        first = open(os.path.join(root, "snapshots", "snap-old.json"), "rb").read()

        # Publishing more never changes old manifest bytes.
        sc.gc()
        self.assertEqual(
            open(os.path.join(root, "snapshots", "snap-old.json"), "rb").read(),
            first,
        )
        result = audit.audit(root)
        self.assertTrue(result.ok, result.errors)

    def test_auditor_detects_tampered_manifest(self):
        sc = Scenario(os.path.join(self.base, "sc"))
        sc.publish_old()
        root = os.path.join(sc.path, "repo")
        manifest_path = os.path.join(root, "snapshots", "snap-old.json")
        with open(manifest_path, "rb") as handle:
            manifest = json.loads(handle.read())
        manifest["root"]["version"] = "9.9"
        with open(manifest_path, "wb") as handle:
            handle.write(json.dumps(manifest, sort_keys=True,
                                    separators=(",", ":")).encode())
        result = audit.audit(root)
        self.assertFalse(result.ok)
        self.assertTrue(any("differs from committed" in e for e in result.errors))

    def test_auditor_detects_deleted_object(self):
        sc = Scenario(os.path.join(self.base, "sc"))
        sc.publish_old()
        root = os.path.join(sc.path, "repo")
        lib = next(p for p in sc.store.snapshots["snap-old"]["packages"]
                   if p["name"] == "target-lib")
        os.unlink(os.path.join(root, "objects",
                               lib["archive_digest"][:2],
                               lib["archive_digest"][2:]))
        result = audit.audit(root)
        self.assertFalse(result.ok)
        self.assertTrue(any("content-missing" in e for e in result.errors))


class MarkPublishRaceTests(unittest.TestCase):
    def setUp(self):
        self.base = tempfile.mkdtemp()
        self.addCleanup(shutil.rmtree, self.base, ignore_errors=True)

    def test_snapshot_published_in_post_mark_seam_is_retained(self):
        sc = Scenario(os.path.join(self.base, "sc"))
        sc.publish_old()
        old_lease = sc.lease_old()
        sc.publish_new()
        old_lease.release()

        # A worker re-acquires the old lease exactly at the post-mark seam.
        schedule = ThreadedSchedule(
            sc.store.acquire_lease, args=("snap-old",)
        )
        sc.bus.once(GC_AFTER_MARK_LOCK, schedule.kick)
        gc_result = sc.store.collect_garbage()
        lease = schedule.wait()
        lib = next(p for p in sc.store.snapshots["snap-old"]["packages"]
                   if p["name"] == "target-lib")
        self.assertNotIn(lib["archive_digest"], gc_result["deleted"])
        self.assertTrue(
            sc.store.reader(lease).has_package(coord("target-lib", "2.1"))
        )
        # Independent auditor confirms the marked generation deleted nothing
        # the freshly published pin needed.
        result = audit.audit(os.path.join(sc.path, "repo"))
        self.assertTrue(result.ok, result.errors)

    def test_replay_is_deterministic(self):
        """The same fixture steps on two empty stores yield identical bytes."""
        def run(base):
            sc = Scenario(os.path.join(base, "a"))
            sc.publish_old()
            sc.publish_new()
            with open(os.path.join(sc.path, "repo", "snapshots",
                                   "snap-old.json"), "rb") as handle:
                return handle.read()
        first = run(tempfile.mkdtemp(dir=self.base))
        second = run(tempfile.mkdtemp(dir=self.base))
        self.assertEqual(first, second)


if __name__ == "__main__":
    unittest.main()


class IndependentClosureOracleTests(unittest.TestCase):
    """The auditor's own BFS must reject any snapshot whose closure lies."""

    def setUp(self):
        self.base = tempfile.mkdtemp()
        self.addCleanup(shutil.rmtree, self.base, ignore_errors=True)

    def test_oracle_walks_both_edge_kinds_for_each_visible_snapshot(self):
        sc = Scenario(os.path.join(self.base, "sc"))
        sc.publish_old()
        old_lease = sc.lease_old()
        sc.publish_new()
        root = os.path.join(sc.path, "repo")

        result = audit.audit(root)
        self.assertTrue(result.ok, result.errors)
        old_manifest = result.snapshots["snap-old"]
        new_manifest = result.snapshots["snap-new"]

        old_names = {p["name"] for p in old_manifest["packages"]}
        new_names = {p["name"] for p in new_manifest["packages"]}
        self.assertEqual(
            old_names,
            {"root", "target-lib", "host-gcc", "host-binutils"},
        )
        # New snapshot removes the target package but keeps host tools.
        self.assertEqual(
            new_names, {"root", "host-gcc", "host-binutils"}
        )
        # Both manifests pin exact versions+platforms and distinct identities.
        old_root = next(p for p in old_manifest["packages"] if p["name"] == "root")
        new_root = next(p for p in new_manifest["packages"] if p["name"] == "root")
        self.assertNotEqual(old_root["version"], new_root["version"])
        self.assertEqual(old_root["platform"], new_root["platform"])
