import hashlib
import tempfile
import unittest
from pathlib import Path

from buildcache.cache import CacheEntry, CacheStore, ConflictError


def make_entry(key="k" * 64, digest="d" * 64, outputs=None):
    return CacheEntry(key=key, digest=digest, outputs=outputs or {"a.o": "1" * 64})


class CacheStoreTests(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self.tmp.cleanup)
        self.store = CacheStore(Path(self.tmp.name) / "cache")

    def test_publish_and_get_roundtrip(self):
        entry = make_entry()
        artifacts = {"a.o": b"object-bytes"}
        published = self.store.publish(entry, artifacts)
        self.assertEqual(published, entry)
        self.assertEqual(self.store.get(entry.key), entry)
        obj = self.store.objects_dir / hashlib.sha256(b"object-bytes").hexdigest()
        self.assertEqual(obj.read_bytes(), b"object-bytes")

    def test_get_missing_key_returns_none(self):
        self.assertIsNone(self.store.get("0" * 64))

    def test_same_key_same_content_dedupes(self):
        first = self.store.publish(make_entry(), {"a.o": b"x"})
        again = self.store.publish(make_entry(), {"a.o": b"x"})
        self.assertEqual(first, again)
        entries = list(self.store.entries_dir.glob("*.json"))
        self.assertEqual(len(entries), 1)

    def test_same_key_different_content_conflicts(self):
        self.store.publish(make_entry(), {"a.o": b"x"})
        other = make_entry(digest="e" * 64, outputs={"a.o": "2" * 64})
        with self.assertRaises(ConflictError):
            self.store.publish(other, {"a.o": b"y"})
        kept = self.store.get(other.key)
        self.assertEqual(kept.digest, "d" * 64)
        self.assertEqual(kept.outputs, {"a.o": "1" * 64})

    def test_conflict_writes_no_objects(self):
        self.store.publish(make_entry(), {"a.o": b"x"})
        before = set(self.store.objects_dir.iterdir())
        with self.assertRaises(ConflictError):
            self.store.publish(make_entry(digest="e" * 64, outputs={"a.o": "2" * 64}),
                               {"a.o": b"conflicting-bytes"})
        self.assertEqual(set(self.store.objects_dir.iterdir()), before)

    def test_entries_survive_reopen(self):
        self.store.publish(make_entry(), {"a.o": b"x"})
        reopened = CacheStore(self.store.root)
        self.assertEqual(reopened.get("k" * 64), make_entry())


if __name__ == "__main__":
    unittest.main()
