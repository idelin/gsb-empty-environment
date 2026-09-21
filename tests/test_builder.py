import hashlib
import tempfile
import threading
import unittest
from pathlib import Path

from buildcache.builder import (
    Builder,
    SummaryMismatchError,
    deterministic_build,
)
from buildcache.cache import CacheStore, ConflictError
from buildcache.manifest import (
    Node,
    OutputSpec,
    Package,
    expected_sha256,
    output_content,
)
from buildcache.version import Version


def make_node(node_id="core-lib", inputs=("src/core.c", "include/core.h"),
              outputs=("lib/core.o", "lib/core.map")):
    package = Package("core", Version.parse("1.2.3"))
    specs = tuple(
        OutputSpec(path, expected_sha256(node_id, tuple(inputs), path))
        for path in outputs
    )
    return Node(node_id, package, tuple(inputs), specs)


class BuilderTests(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self.tmp.cleanup)
        root = Path(self.tmp.name)
        self.store = CacheStore(root / "cache")
        self.workdir = root / "work"

    def workdir_empty(self):
        return not any(self.workdir.iterdir())

    def test_all_outputs_verified_before_publish(self):
        builder = Builder(self.store, self.workdir)
        node = make_node()
        entry = builder.build(node)
        self.assertEqual(set(entry.outputs), {"lib/core.o", "lib/core.map"})
        for spec in node.outputs:
            self.assertEqual(entry.outputs[spec.path], spec.sha256)
        self.assertEqual(self.store.get(node.key), entry)
        self.assertTrue(self.workdir_empty())

    def test_rebuild_is_cache_hit(self):
        calls = []

        def counting(node, staging):
            calls.append(node.id)
            deterministic_build(node, staging)

        builder = Builder(self.store, self.workdir, build_fn=counting)
        node = make_node()
        first = builder.build(node)
        second = builder.build(node)
        self.assertEqual(first, second)
        self.assertEqual(len(calls), 1)

    def test_missing_output_publishes_nothing(self):
        def broken(node, staging):
            spec = node.outputs[0]
            target = Path(staging) / spec.path
            target.parent.mkdir(parents=True, exist_ok=True)
            target.write_bytes(output_content(node.id, node.inputs, spec.path))

        builder = Builder(self.store, self.workdir, build_fn=broken)
        node = make_node()
        with self.assertRaises(SummaryMismatchError):
            builder.build(node)
        self.assertIsNone(self.store.get(node.key))
        self.assertEqual(list(self.store.entries_dir.iterdir()), [])
        self.assertTrue(self.workdir_empty())

    def test_digest_mismatch_cleans_up(self):
        def tampered(node, staging):
            for spec in node.outputs:
                target = Path(staging) / spec.path
                target.parent.mkdir(parents=True, exist_ok=True)
                target.write_bytes(b"tampered")

        builder = Builder(self.store, self.workdir, build_fn=tampered)
        node = make_node()
        with self.assertRaises(SummaryMismatchError):
            builder.build(node)
        self.assertIsNone(self.store.get(node.key))
        self.assertEqual(list(self.store.entries_dir.iterdir()), [])
        self.assertTrue(self.workdir_empty())

    def test_concurrent_duplicate_builds_run_once(self):
        calls = []
        lock = threading.Lock()

        def counting(node, staging):
            with lock:
                calls.append(node.id)
            deterministic_build(node, staging)

        builder = Builder(self.store, self.workdir, build_fn=counting)
        node = make_node()
        results, errors = [], []

        def worker():
            try:
                results.append(builder.build(node))
            except Exception as exc:  # noqa: BLE001 - collected for assertion
                errors.append(exc)

        threads = [threading.Thread(target=worker) for _ in range(8)]
        for thread in threads:
            thread.start()
        for thread in threads:
            thread.join()
        self.assertEqual(errors, [])
        self.assertEqual(len(calls), 1)
        self.assertEqual(len(results), 8)
        self.assertEqual(len({entry.digest for entry in results}), 1)
        self.assertTrue(self.workdir_empty())

    def test_nondeterministic_conflict_keeps_first_entry(self):
        builder = Builder(self.store, self.workdir)
        node = make_node()
        original = builder.build(node)

        def variant_content(path):
            return output_content(node.id, node.inputs, path) + b"#v2"

        specs = tuple(
            OutputSpec(spec.path, hashlib.sha256(variant_content(spec.path)).hexdigest())
            for spec in node.outputs
        )
        node_v2 = Node(node.id, node.package, node.inputs, specs)
        self.assertEqual(node_v2.key, node.key)

        def variant_build(built, staging):
            for spec in built.outputs:
                target = Path(staging) / spec.path
                target.parent.mkdir(parents=True, exist_ok=True)
                target.write_bytes(variant_content(spec.path))

        other = Builder(self.store, self.workdir, build_fn=variant_build)
        with self.assertRaises(ConflictError):
            other.build(node_v2, force=True)
        self.assertEqual(self.store.get(node.key), original)


if __name__ == "__main__":
    unittest.main()
