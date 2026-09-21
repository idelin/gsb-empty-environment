from __future__ import annotations

import concurrent.futures
import pathlib
import sys
import tempfile
import threading
import time
import unittest

sys.path.insert(0, str(pathlib.Path(__file__).resolve().parent.parent))

from buildcache import (
    DigestMismatchError,
    MissingOutputError,
    NonDeterministicConflictError,
    SimulatorBuilder,
    Version,
)
from buildcache.engine import ActionCache
from buildcache.manifest import (
    BuildNode,
    BuildOutput,
    Manifest,
    Package,
    parse_manifest,
)
from buildcache.store import ContentAddressableStore, sha256_hex

EXAMPLE = pathlib.Path(__file__).resolve().parent.parent / "examples" / "manifest.json"


def make_manifest(*nodes: BuildNode, package: str = "pkg", version: str = "1.0.0") -> Manifest:
    return Manifest(
        packages={package: Package(package, Version.parse(version))},
        nodes={node.key: node for node in nodes},
    )


def node(key: str, *outputs: BuildOutput, behavior: str = "deterministic", package: str = "pkg",
         inputs=None) -> BuildNode:
    return BuildNode(
        key=key, package=package, inputs=inputs or {}, outputs=tuple(outputs), behavior=behavior
    )


def text_output(path: str, body: str, sha256: str | None = None) -> BuildOutput:
    return BuildOutput(path=path, template=body, encoding="utf-8", sha256=sha256)


class ContentStoreTest(unittest.TestCase):
    def setUp(self):
        self._tmp = tempfile.TemporaryDirectory()
        self.store = ContentAddressableStore(self._tmp.name)

    def tearDown(self):
        self._tmp.cleanup()

    def test_blob_dedup_and_readback(self):
        data = b"hello blob"
        digest = sha256_hex(data)
        self.assertEqual(self.store.put_blob(data), digest)
        self.assertEqual(self.store.put_blob(data), digest)
        self.assertTrue(self.store.has_blob(digest))
        self.assertEqual(self.store.get_blob(digest), data)

    def test_publish_same_content_dedups(self):
        entry = {"key": "k", "outputs": {"a": "d1"}, "output_digest": "od1", "attempt_id": "x"}
        first = self.store.publish_entry("k", dict(entry))
        second = self.store.publish_entry("k", dict(entry))
        self.assertEqual(first, second)

    def test_publish_different_content_conflicts_and_keeps_first(self):
        entry1 = {"key": "k", "outputs": {"a": "d1"}, "output_digest": "od1", "attempt_id": "a1"}
        entry2 = {"key": "k", "outputs": {"a": "d2"}, "output_digest": "od2", "attempt_id": "a2"}
        self.store.publish_entry("k", entry1)
        with self.assertRaises(NonDeterministicConflictError):
            self.store.publish_entry("k", entry2)
        self.assertEqual(self.store.read_entry("k")["attempt_id"], "a1")


class EngineTest(unittest.TestCase):
    def setUp(self):
        self._tmp = tempfile.TemporaryDirectory()
        self.store = ContentAddressableStore(self._tmp.name)
        self.builder = SimulatorBuilder()
        self.cache = ActionCache(self.store)

    def tearDown(self):
        self._tmp.cleanup()

    def build(self, manifest: Manifest, key: str):
        return self.cache.request_build(manifest.nodes[key], manifest, self.builder)

    def test_multi_output_publishes_together(self):
        manifest = make_manifest(
            node("//multi", text_output("a.txt", "A\n"), text_output("b.txt", "B\n"))
        )
        result = self.build(manifest, "//multi")
        self.assertEqual(result.status, "published")
        self.assertEqual(set(result.outputs), {"a.txt", "b.txt"})
        entry = self.store.read_entry("//multi")
        self.assertEqual(set(entry["outputs"]), {"a.txt", "b.txt"})

    def test_blobs_contain_output_bytes(self):
        manifest = make_manifest(node("//x", text_output("a.txt", "payload\n")))
        result = self.build(manifest, "//x")
        digest = result.outputs["a.txt"]
        self.assertEqual(self.store.get_blob(digest), b"payload\n")

    def test_same_key_same_content_dedups(self):
        manifest = make_manifest(node("//x", text_output("a.txt", "stable\n")))
        first = self.build(manifest, "//x")
        second = self.build(manifest, "//x")
        self.assertEqual(first.attempt_id, second.attempt_id)
        self.assertEqual(second.status, "deduped")

    def test_missing_one_output_publishes_nothing(self):
        manifest = make_manifest(
            node(
                "//broken",
                text_output("a.txt", "A\n"),
                text_output("b.txt", "B\n"),
                behavior="missing_output",
            )
        )
        with self.assertRaises(MissingOutputError):
            self.build(manifest, "//broken")
        self.assertIsNone(self.store.read_entry("//broken"))
        blob_count = sum(1 for _ in self.store.blobs_dir.rglob("*") if _.is_file())
        self.assertEqual(blob_count, 0)

    def test_declared_digest_mismatch_publishes_nothing(self):
        bad = text_output("a.txt", "actual\n", sha256="0" * 64)
        manifest = make_manifest(node("//bad", bad))
        with self.assertRaises(DigestMismatchError):
            self.build(manifest, "//bad")
        self.assertIsNone(self.store.read_entry("//bad"))

    def test_empty_output_rejected(self):
        manifest = make_manifest(node("//empty", text_output("a.txt", "")))
        with self.assertRaises(DigestMismatchError):
            self.build(manifest, "//empty")
        self.assertIsNone(self.store.read_entry("//empty"))

    def test_declared_digest_fast_hit(self):
        body = "pinned\n"
        digest = sha256_hex(body.encode())
        manifest = make_manifest(node("//pin", text_output("a.txt", body, sha256=digest)))
        first = self.build(manifest, "//pin")
        self.assertEqual(first.status, "published")
        # A second node object with the same declared digest takes the fast hit.
        second = self.cache.request_build(manifest.nodes["//pin"], manifest, self.builder)
        self.assertEqual(second.status, "cached")
        self.assertEqual(second.attempt_id, first.attempt_id)

    def test_same_key_different_content_conflicts_first_wins(self):
        manifest = make_manifest(
            node(
                "//flaky",
                text_output("a.txt", "nonce={BUILD_NONCE}\n"),
                text_output("b.txt", "also nonce={BUILD_NONCE}\n"),
                behavior="nondeterministic",
            )
        )
        first = self.build(manifest, "//flaky")
        blobs_after_first = {
            f.name for f in self.store.blobs_dir.rglob("*") if f.is_file()
        }
        with self.assertRaises(NonDeterministicConflictError):
            self.build(manifest, "//flaky")
        kept = self.store.read_entry("//flaky")
        self.assertEqual(kept["attempt_id"], first.attempt_id)
        # The losing content must never have been written into the CAS.
        blobs_after_conflict = {
            f.name for f in self.store.blobs_dir.rglob("*") if f.is_file()
        }
        self.assertEqual(blobs_after_first, blobs_after_conflict)
        self.assertEqual(
            list(self.store.tmp_dir.iterdir()), []
        )

    def test_concurrent_same_build_single_flight(self):
        release = threading.Event()

        class SlowBuilder:
            def __init__(self):
                self.calls = 0

            def build(self, node, manifest, staging):
                self.calls += 1
                release.wait(timeout=5)
                staging.write("a.txt", b"concurrent-stable\n")

        slow = SlowBuilder()
        manifest = make_manifest(node("//race", text_output("a.txt", "ignored\n")))

        def go():
            return self.cache.request_build(manifest.nodes["//race"], manifest, slow)

        with concurrent.futures.ThreadPoolExecutor(max_workers=5) as pool:
            futures = [pool.submit(go) for _ in range(5)]
            time.sleep(0.3)
            release.set()
            results = [f.result() for f in futures]

        self.assertEqual(slow.calls, 1)
        self.assertEqual(len({r.attempt_id for r in results}), 1)

    def test_concurrent_different_keys_independent(self):
        manifest = make_manifest(
            node("//a", text_output("a.txt", "A\n")),
            node("//b", text_output("b.txt", "B\n")),
        )
        with concurrent.futures.ThreadPoolExecutor(max_workers=2) as pool:
            ra, rb = list(pool.map(lambda k: self.build(manifest, k), ["//a", "//b"]))
        self.assertEqual(ra.status, "published")
        self.assertEqual(rb.status, "published")
        self.assertNotEqual(ra.attempt_id, rb.attempt_id)

    def test_waiter_retries_after_leader_failure(self):
        manifest = make_manifest(
            node(
                "//fail",
                text_output("a.txt", "A\n"),
                text_output("b.txt", "B\n"),
                behavior="missing_output",
            )
        )

        class FlakyThenOk:
            def __init__(self):
                self.calls = 0
                self.sim = SimulatorBuilder()

            def build(self, node, manifest_, staging):
                self.calls += 1
                if self.calls == 1:
                    self.sim.build(node, manifest_, staging)  # misses one output
                else:
                    staging.write("a.txt", b"A\n")
                    staging.write("b.txt", b"B\n")

        alt_builder = FlakyThenOk()
        start = threading.Barrier(2)

        def request():
            start.wait()
            try:
                return self.cache.request_build(manifest.nodes["//fail"], manifest, alt_builder)
            except MissingOutputError:
                return self.cache.request_build(manifest.nodes["//fail"], manifest, alt_builder)

        with concurrent.futures.ThreadPoolExecutor(max_workers=2) as pool:
            results = list(pool.map(lambda _: request(), range(2)))

        self.assertTrue(any(r is not None for r in results))
        self.assertIsNotNone(self.store.read_entry("//fail"))

    def test_staging_cleaned_after_success_and_failure(self):
        manifest = make_manifest(node("//ok", text_output("a.txt", "A\n")))
        self.build(manifest, "//ok")
        leftovers = [p for p in self.store.tmp_dir.iterdir() if p.is_dir()]
        self.assertEqual(leftovers, [])

    def test_example_manifest_core_release(self):
        from buildcache import load_manifest

        manifest = load_manifest(EXAMPLE)
        result = self.cache.request_build(manifest.nodes["//core:release"], manifest, self.builder)
        self.assertEqual(result.status, "published")
        self.assertEqual(set(result.outputs), {"config/version.conf", "lib/core.dat"})

    def test_prerelease_version_rendered_into_output(self):
        manifest = make_manifest(
            node("//pre", text_output("v.txt", "v={VERSION}\n")),
            version="3.0.0-alpha.2",
        )
        result = self.build(manifest, "//pre")
        data = self.store.get_blob(result.outputs["v.txt"])
        self.assertEqual(data, b"v=3.0.0-alpha.2\n")


if __name__ == "__main__":
    unittest.main(verbosity=2)
