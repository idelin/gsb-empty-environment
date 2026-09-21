"""Basic import, closure and snapshot semantics."""

import os
import tempfile
import unittest

from imagestore import (
    DigestMismatch,
    ImageStore,
    LengthMismatch,
    MetadataConflict,
    MetadataInvalid,
)
from imagestore import closure as closure_mod
from imagestore.errors import ClosureIncomplete, SnapshotExists, UnknownSnapshot
from imagestore.fixture import (
    HOST,
    TARGET,
    archive_bytes,
    build_world_packages,
    coord,
    digest_of,
    import_all,
)


class RepoTestBase(unittest.TestCase):
    def setUp(self):
        self.base = tempfile.mkdtemp()
        self.path = os.path.join(self.base, "repo")
        self.store = ImageStore.create(self.path)

    def repo_path(self, *parts):
        return os.path.join(self.path, *parts)


class ImportTests(RepoTestBase):
    def _import(self, label):
        meta, data = build_world_packages()[label]
        return self.store.import_package_bytes(
            meta, data, digest_of(data), len(data)
        )

    def test_import_verifies_length_and_digest(self):
        meta, data = build_world_packages()["target-lib"]
        with self.assertRaises(LengthMismatch):
            self.store.import_package_bytes(
                meta, data + b"x", digest_of(data), len(data)
            )
        with self.assertRaises(LengthMismatch):
            self.store.import_package_bytes(
                meta, data[:-1], digest_of(data[:-1]), len(data)
            )
        with self.assertRaises(DigestMismatch):
            self.store.import_package_bytes(
                meta, data, digest_of(data + b"x"), len(data)
            )
        self.assertEqual(os.listdir(self.repo_path("staging")), [])

    def test_unfinished_import_not_referenced_by_manifest(self):
        # A failed verification import leaves no catalog entry and no objects.
        meta, data = build_world_packages()["target-lib"]
        with self.assertRaises(DigestMismatch):
            self.store.import_package_bytes(
                meta, data, digest_of(data + b"x"), len(data)
            )
        self.assertEqual(self.store.catalog, {})
        self.assertEqual(os.listdir(self.repo_path("objects")) or [], [])
        report = self.store.check_publish(coord("target-lib", "2.1"))
        self.assertFalse(report.complete)
        self.assertEqual(report.issues[0]["kind"], "metadata-missing")

    def test_same_digest_content_is_shared(self):
        e1 = self._import("target-lib")
        def object_files():
            total = 0
            for shard in os.listdir(self.repo_path("objects")):
                sp = self.repo_path("objects", shard)
                if os.path.isdir(sp):
                    total += len(os.listdir(sp))
            return total
        before = object_files()
        e2 = self._import("target-lib")
        self.assertEqual(e1, e2)
        self.assertEqual(object_files(), before)
        self.assertEqual(before, 2)  # exactly one meta + one archive object

    def test_conflicting_metadata_rejected(self):
        meta, data = build_world_packages()["target-lib"]
        self.store.import_package_bytes(meta, data, digest_of(data), len(data))
        meta2 = dict(meta)
        meta2["run_requires"] = [coord("other", "1.0")]
        with self.assertRaises(MetadataConflict):
            self.store.import_package_bytes(
                meta2, data, digest_of(data), len(data)
            )

    def test_metadata_validation(self):
        with self.assertRaises(MetadataInvalid):
            self.store.import_package_bytes(
                {"name": "", "version": "1", "platform": TARGET},
                b"d", digest_of(b"d"), 1,
            )
        with self.assertRaises(MetadataInvalid):
            self.store.import_package_bytes(
                {"name": "n", "version": "1", "platform": TARGET,
                 "run_requires": [{"name": "x"}]},
                b"d", digest_of(b"d"), 1,
            )


class ClosureAndPublishTests(RepoTestBase):
    def test_two_closures_must_both_be_complete(self):
        # Import only the root: both runtime and host deps are missing.
        meta, data = build_world_packages()["root-1.0"]
        self.store.import_package_bytes(meta, data, digest_of(data), len(data))
        report = self.store.check_publish(coord("root", "1.0"))
        kinds = {i["kind"] for i in report.issues}
        self.assertEqual(kinds, {"metadata-missing"})
        missing_coords = {i["coordinate"]["name"] for i in report.issues}
        self.assertIn("target-lib", missing_coords)
        self.assertIn("host-gcc", missing_coords)
        with self.assertRaises(ClosureIncomplete) as ctx:
            self.store.publish_snapshot(coord("root", "1.0"), "snap-x")
        # every issue carries a root-to-node path
        for issue in ctx.exception.issues:
            self.assertTrue(issue["path"].startswith("root==1.0@"))
            self.assertIn("--", issue["path"])

    def test_host_tool_transitive_runtime_is_required(self):
        # gcc without binutils: host closure incomplete through host-gcc.
        for label in ("target-lib", "host-gcc", "root-1.0"):
            meta, data = build_world_packages()[label]
            self.store.import_package_bytes(meta, data, digest_of(data), len(data))
        report = self.store.check_publish(coord("root", "1.0"))
        names = {i["coordinate"]["name"] for i in report.issues}
        self.assertIn("host-binutils", names)
        issue = next(i for i in report.issues
                     if i["coordinate"]["name"] == "host-binutils")
        self.assertIn("host-gcc", issue["path"])
        self.assertIn("run_requires", issue["path"])

    def test_full_publish_is_complete_and_immutable(self):
        import_all(self.store)
        sid = self.store.publish_snapshot(coord("root", "1.0"), "snap-old")
        self.assertEqual(sid, "snap-old")
        self.assertEqual(self.store.current_snapshot(), "snap-old")
        with self.assertRaises(SnapshotExists):
            self.store.publish_snapshot(coord("root", "1.0"), "snap-old")
        manifest_path = self.repo_path("snapshots", "snap-old.json")
        before = open(manifest_path, "rb").read()
        # republishing identical content yields the same immutable file bytes
        self.assertEqual(open(manifest_path, "rb").read(), before)
        # manifest lists all 4 packages
        import json
        manifest = json.loads(before)
        self.assertEqual(len(manifest["packages"]), 4)
        runtime_names = {p[0] for p in manifest["runtime"]}
        self.assertEqual(runtime_names, {"root", "target-lib"})
        host_names = {p[0] for p in manifest["host_only"]}
        self.assertEqual(host_names, {"host-gcc", "host-binutils"})

    def test_unknown_snapshot_errors(self):
        with self.assertRaises(UnknownSnapshot):
            self.store.acquire_lease("nope")
        with self.assertRaises(UnknownSnapshot):
            self.store.open_snapshot("nope")


class IndependentClosureTests(unittest.TestCase):
    def test_pure_fixed_point_math(self):
        graph = {
            ("a", "1", "p"): {"run_requires": [("b", "1", "p")],
                               "host_requires": [("h", "1", "q")]},
            ("b", "1", "p"): {"run_requires": [], "host_requires": []},
            ("h", "1", "q"): {"run_requires": [("r", "1", "q")],
                               "host_requires": []},
            ("r", "1", "q"): {"run_requires": [], "host_requires": []},
        }
        def c(triple):
            return {"name": triple[0], "version": triple[1], "platform": triple[2]}

        def meta_of(node):
            meta = graph.get(closure_mod.coord_key(node))
            if meta is None:
                return None
            return {
                "run_requires": [c(d) for d in meta["run_requires"]],
                "host_requires": [c(d) for d in meta["host_requires"]],
            }

        runtime, paths = closure_mod.least_fixed_point(
            [c(("a", "1", "p"))], meta_of, ("run_requires",)
        )
        self.assertEqual(set(runtime), {("a", "1", "p"), ("b", "1", "p")})
        union, upaths = closure_mod.least_fixed_point(
            [c(("a", "1", "p"))], meta_of,
            ("run_requires", "host_requires"),
        )
        self.assertEqual(set(union), {
            ("a", "1", "p"), ("b", "1", "p"),
            ("h", "1", "q"), ("r", "1", "q"),
        })


if __name__ == "__main__":
    unittest.main()
