import shutil
import tempfile
import threading
import unittest
from pathlib import Path

from buildcache.builder import Builder, BuildError, BuildNode
from buildcache.cache import BuildCache, NonDeterministicConflict


class BuilderTests(unittest.TestCase):
    def setUp(self):
        self.tmp = Path(tempfile.mkdtemp())
        self.cache = BuildCache(self.tmp / "cache")
        self.work = self.tmp / "work"
        self.builder = Builder(self.cache, work_root=self.work)

    def tearDown(self):
        shutil.rmtree(self.tmp, ignore_errors=True)

    def test_publishes_when_all_outputs_present(self):
        def action(staging):
            (staging / "a.txt").write_bytes(b"alpha")
            (staging / "b.txt").write_bytes(b"bravo")

        node = BuildNode("docs", ("a.txt", "b.txt"), action)
        result = self.builder.build(node)
        self.assertEqual(result.status, "published")
        self.assertTrue(result.entry.verify())
        self.assertEqual(self.builder.build(node).status, "cache-hit")

    def test_missing_output_publishes_nothing_and_cleans_staging(self):
        def action(staging):
            (staging / "a.txt").write_bytes(b"alpha")  # b.txt intentionally absent

        node = BuildNode("partial", ("a.txt", "b.txt"), action)
        with self.assertRaises(BuildError):
            self.builder.build(node)

        self.assertIsNone(self.cache.lookup("partial"))
        self.assertEqual(list(self.work.iterdir()), [])  # staging cleaned

    def test_failing_action_publishes_nothing_and_cleans_staging(self):
        def action(staging):
            (staging / "a.txt").write_bytes(b"alpha")
            raise RuntimeError("compiler exploded")

        node = BuildNode("boom", ("a.txt",), action)
        with self.assertRaises(RuntimeError):
            self.builder.build(node)

        self.assertIsNone(self.cache.lookup("boom"))
        self.assertEqual(list(self.work.iterdir()), [])

    def test_concurrent_duplicate_builds_share_one_run(self):
        runs = {"n": 0}
        barrier = threading.Barrier(4)

        def action(staging):
            runs["n"] += 1
            (staging / "out.txt").write_bytes(b"hello")

        node = BuildNode("once", ("out.txt",), action)
        results = []
        errors = []

        def worker():
            barrier.wait(timeout=5)  # all threads enter build() together
            try:
                results.append(self.builder.build(node))
            except BaseException as exc:  # noqa: BLE001
                errors.append(exc)

        threads = [threading.Thread(target=worker) for _ in range(4)]
        for t in threads:
            t.start()
        for t in threads:
            t.join()

        self.assertEqual(errors, [])
        self.assertEqual(runs["n"], 1)  # single-flight, no duplicate build
        self.assertEqual(len(results), 4)
        digests = {r.entry.digests["out.txt"] for r in results}
        self.assertEqual(len(digests), 1)
        self.assertEqual({r.status for r in results} <=
                         {"published", "deduped", "cache-hit"}, True)

    def test_forced_identical_rebuild_is_deduped(self):
        BuildNode("k", ("o",), lambda d: (d / "o").write_bytes(b"x"))
        first = self.builder.build(BuildNode(
            "k", ("o",), lambda d: (d / "o").write_bytes(b"x")))
        second = self.builder.build(BuildNode(
            "k", ("o",), lambda d: (d / "o").write_bytes(b"x")), force=True)
        self.assertEqual(first.status, "published")
        self.assertEqual(second.status, "deduped")

    def test_forced_different_rebuild_flags_conflict(self):
        self.builder.build(BuildNode(
            "k", ("o",), lambda d: (d / "o").write_bytes(b"first")))
        with self.assertRaises(NonDeterministicConflict):
            self.builder.build(BuildNode(
                "k", ("o",), lambda d: (d / "o").write_bytes(b"second")),
                force=True)

        entry = self.cache.lookup("k")
        self.assertEqual(entry.read_output("o"), b"first")  # no overwrite
        self.assertEqual(len(self.cache.conflicts), 1)

    def test_node_without_outputs_rejected(self):
        with self.assertRaises(ValueError):
            BuildNode("empty", (), lambda d: None)


if __name__ == "__main__":
    unittest.main()
