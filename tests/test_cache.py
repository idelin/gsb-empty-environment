import os
import shutil
import tempfile
import threading
import unittest
from pathlib import Path

from buildcache.cache import (
    BuildCache,
    IntegrityError,
    MissingOutputError,
    NonDeterministicConflict,
    PublishResult,
)
from buildcache.digest import build_key

KEY = build_key("core", "2.4.1", {"opt": True}, ["a.txt", "b.txt"])
KEY2 = build_key("core", "2.4.1", {"opt": False}, ["a.txt"])


class CacheTestBase(unittest.TestCase):
    def setUp(self):
        self.root = Path(tempfile.mkdtemp(prefix="bcache-"))
        self.cache = BuildCache(self.root)

    def tearDown(self):
        shutil.rmtree(self.root, ignore_errors=True)

    def make_stage(self, files):
        stage = self.root / "work"
        stage.mkdir(exist_ok=True)
        for name, data in files.items():
            path = stage / name
            path.parent.mkdir(parents=True, exist_ok=True)
            path.write_bytes(data)
        return stage


class PublishTests(CacheTestBase):
    def test_publish_all_or_nothing(self):
        stage = self.make_stage({"a.txt": b"AAA", "b.txt": b"BBB"})
        result = self.cache.publish(
            KEY, stage, package="core", version="2.4.1", inputs={"opt": True},
            outputs=["a.txt", "b.txt"],
        )
        self.assertEqual(result, PublishResult.PUBLISHED)
        self.assertTrue(self.cache.has(KEY))
        self.assertTrue(self.cache.verify_entry(KEY))
        record = self.cache.get(KEY)
        self.assertEqual(set(record["outputs"]), {"a.txt", "b.txt"})

    def test_missing_one_output_publishes_nothing(self):
        stage = self.make_stage({"a.txt": b"AAA"})  # b.txt missing
        with self.assertRaises(MissingOutputError) as ctx:
            self.cache.publish(
                KEY, stage, package="core", version="2.4.1", inputs={"opt": True},
                outputs=["a.txt", "b.txt"],
            )
        self.assertEqual(ctx.exception.missing, ("b.txt",))
        self.assertFalse(self.cache.has(KEY))
        self.assertEqual(self.cache.keys(), [])

    def test_duplicate_same_content_is_deduplicated(self):
        stage = self.make_stage({"a.txt": b"AAA", "b.txt": b"BBB"})
        first = self.cache.publish(
            KEY, stage, package="core", version="2.4.1", inputs={"opt": True},
            outputs=["a.txt", "b.txt"],
        )
        # rebuild into a fresh staging directory with identical bytes
        second_stage = self.make_stage({"a.txt": b"AAA", "b.txt": b"BBB", "extra": b"x"})
        second = self.cache.publish(
            KEY, second_stage, package="core", version="2.4.1", inputs={"opt": True},
            outputs=["a.txt", "b.txt"], attempt="1",
        )
        self.assertEqual(first, PublishResult.PUBLISHED)
        self.assertEqual(second, PublishResult.DEDUPLICATED)
        self.assertFalse((self.cache.entry_path(KEY) / "extra").exists())

    def test_different_content_raises_conflict_and_keeps_first(self):
        stage_a = self.make_stage({"a.txt": b"first", "b.txt": b"B"})
        self.cache.publish(
            KEY, stage_a, package="core", version="2.4.1", inputs={"opt": True},
            outputs=["a.txt", "b.txt"],
        )
        stage_b = self.make_stage({"a.txt": b"second", "b.txt": b"B"})
        with self.assertRaises(NonDeterministicConflict) as ctx:
            self.cache.publish(
                KEY, stage_b, package="core", version="2.4.1", inputs={"opt": True},
                outputs=["a.txt", "b.txt"], attempt="1",
            )
        self.assertIn("a.txt", ctx.exception.diffs)
        # first writer wins; no overwrite
        self.assertEqual((self.cache.entry_path(KEY) / "a.txt").read_bytes(), b"first")

    def test_tampered_stage_between_stage_and_commit(self):
        stage = self.make_stage({"a.txt": b"AAA"})
        area, digests = self.cache._stage_build(KEY2, stage, ["a.txt"], "probe")
        try:
            (area / "a.txt").write_bytes(b"TAMPERED")
            manifest = self.cache._manifest(KEY2, "core", "2.4.1", {"opt": False}, digests)
            with self.assertRaises(IntegrityError):
                self.cache._commit(area, KEY2, manifest, digests)
            self.assertFalse(self.cache.has(KEY2))
            # failed claim must not leave an entry directory
            self.assertEqual(self.cache.keys(), [])
        finally:
            shutil.rmtree(area, ignore_errors=True)

    def test_no_tmp_left_after_success_or_failure(self):
        stage = self.make_stage({"a.txt": b"AAA"})
        self.cache.publish(
            KEY2, stage, package="core", version="2.4.1", inputs={"opt": False},
            outputs=["a.txt"],
        )
        leftovers = [p for p in self.cache.tmp_dir.iterdir() if p.name.startswith(".staging-")]
        self.assertEqual(leftovers, [])
        with self.assertRaises(MissingOutputError):
            bad = self.make_stage({"a.txt": b"x"})
            self.cache.publish(
                build_key("x", "1.0.0", {}, ["nope"]), bad,
                package="x", version="1.0.0", inputs={}, outputs=["nope"],
            )
        self.assertEqual(list(self.cache.tmp_dir.iterdir()), [])

    def test_reload_from_disk(self):
        stage = self.make_stage({"a.txt": b"AAA", "b.txt": b"BBB"})
        self.cache.publish(
            KEY, stage, package="core", version="2.4.1", inputs={"opt": True},
            outputs=["a.txt", "b.txt"],
        )
        reopened = BuildCache(self.root)
        self.assertEqual(reopened.keys(), [KEY])
        self.assertTrue(reopened.verify_entry(KEY))

    def test_path_traversal_rejected(self):
        with self.assertRaises(Exception):
            stage = self.make_stage({"a.txt": b"x"})
            self.cache.publish(
                build_key("p", "1.0.0", {}, ["../escape"]), stage,
                package="p", version="1.0.0", inputs={}, outputs=["../escape"],
            )


class ConcurrentTests(CacheTestBase):
    def test_concurrent_same_content_one_publish(self):
        results = []
        errors = []

        def worker():
            stage = self.root / f"stage-{threading.get_ident()}"
            stage.mkdir(exist_ok=True)
            (stage / "a.txt").write_bytes(b"AAA")
            (stage / "b.txt").write_bytes(b"BBB")
            try:
                results.append(
                    self.cache.publish(
                        KEY, stage, package="core", version="2.4.1",
                        inputs={"opt": True}, outputs=["a.txt", "b.txt"],
                    )
                )
            except BaseException as exc:
                errors.append(exc)

        threads = [threading.Thread(target=worker) for _ in range(8)]
        for t in threads:
            t.start()
        for t in threads:
            t.join()
        self.assertEqual(errors, [])
        self.assertEqual(results.count(PublishResult.PUBLISHED), 1)
        self.assertEqual(results.count(PublishResult.DEDUPLICATED), 7)
        self.assertTrue(self.cache.verify_entry(KEY))

    def test_concurrent_divergent_builds_conflict_no_overwrite(self):
        conflicts = []
        published = []

        def worker(payload):
            stage = self.root / f"stage-{threading.get_ident()}-{payload[:3]}"
            stage.mkdir(parents=True, exist_ok=True)
            (stage / "a.txt").write_bytes(payload.encode())
            (stage / "b.txt").write_bytes(b"B")
            try:
                result = self.cache.publish(
                    KEY, stage, package="core", version="2.4.1",
                    inputs={"opt": True}, outputs=["a.txt", "b.txt"],
                )
                published.append(result)
            except NonDeterministicConflict as exc:
                conflicts.append(exc)

        threads = [
            threading.Thread(target=worker, args=(v,))
            for v in ("payload-alpha", "payload-beta", "payload-gamma")
        ]
        for t in threads:
            t.start()
        for t in threads:
            t.join()
        self.assertEqual(len(published), 1)
        self.assertEqual(len(conflicts), 2)
        winner = (self.cache.entry_path(KEY) / "a.txt").read_bytes().decode()
        self.assertIn(winner, {"payload-alpha", "payload-beta", "payload-gamma"})
        # loser artifacts must not linger in tmp
        self.assertEqual(
            [p for p in self.cache.tmp_dir.iterdir() if p.name.startswith(".staging-")], []
        )


if __name__ == "__main__":
    unittest.main()
