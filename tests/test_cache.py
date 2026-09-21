import shutil
import tempfile
import unittest
from pathlib import Path

from buildcache.cache import BuildCache, NonDeterministicConflict


class CacheTests(unittest.TestCase):
    def setUp(self):
        self.cache = BuildCache(tempfile.mkdtemp())

    def tearDown(self):
        shutil.rmtree(self.cache.root, ignore_errors=True)

    def test_publish_and_summary_check(self):
        entry, published = self.cache.publish(
            "docs", {"a.txt": b"alpha", "b.txt": b"bravo"})
        self.assertTrue(published)
        self.assertEqual(set(entry.digests), {"a.txt", "b.txt"})

        got = self.cache.lookup("docs")
        self.assertIsNotNone(got)
        self.assertTrue(got.verify())
        self.assertEqual(got.read_output("a.txt"), b"alpha")

    def test_identical_content_is_deduped(self):
        first, p1 = self.cache.publish("k", {"o": b"data"})
        second, p2 = self.cache.publish("k", {"o": b"data"})
        self.assertTrue(p1)
        self.assertFalse(p2)
        self.assertEqual(first.digests, second.digests)
        self.assertEqual(self.cache.conflicts, [])

    def test_different_content_conflicts_and_keeps_first(self):
        self.cache.publish("k", {"o": b"one"})
        with self.assertRaises(NonDeterministicConflict):
            self.cache.publish("k", {"o": b"two"})
        self.assertEqual(len(self.cache.conflicts), 1)

        entry = self.cache.lookup("k")
        self.assertEqual(entry.read_output("o"), b"one")  # not overwritten

    def test_corrupt_entry_fails_summary_check(self):
        self.cache.publish("k", {"o": b"original"})
        entry = self.cache.lookup("k")
        (entry.path / "o").write_bytes(b"tampered")
        self.assertFalse(entry.verify())
        self.assertIsNone(self.cache.lookup("k"))

    def test_missing_file_fails_summary_check(self):
        entry, _ = self.cache.publish("k", {"o": b"x"})
        (entry.path / "o").unlink()
        self.assertFalse(entry.verify())
        self.assertIsNone(self.cache.lookup("k"))

    def test_empty_outputs_rejected(self):
        with self.assertRaises(ValueError):
            self.cache.publish("k", {})

    def test_entry_lives_under_content_keyed_dir(self):
        entry, _ = self.cache.publish("some-key", {"o": b"x"})
        self.assertTrue(Path(entry.path).is_dir())
        self.assertTrue((entry.path / "manifest.json").is_file())


if __name__ == "__main__":
    unittest.main()
