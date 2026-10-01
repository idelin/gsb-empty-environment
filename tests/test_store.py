import tempfile
import unittest
from pathlib import Path

from modelcheck.encoding import encode_state
from modelcheck.store import CompressedStateStore


class ConstantHashStore(CompressedStateStore):
    """Force every key into one slot to simulate hash collisions."""

    @staticmethod
    def hash_key(key):
        return 0


class TestCompressedStore(unittest.TestCase):
    def test_add_and_contains(self):
        store = CompressedStateStore()
        keys = [encode_state(("s", i)) for i in range(50)]
        for key in keys:
            self.assertTrue(store.add(key))
        for key in keys:
            self.assertFalse(store.add(key))
            self.assertIn(key, store)
        self.assertEqual(len(store), 50)

    def test_hash_collisions_compare_full_state(self):
        store = ConstantHashStore()
        keys = [encode_state(("collision", i)) for i in range(200)]
        for key in keys:
            self.assertTrue(store.add(key))
        # identical hashes, yet no two distinct states are merged
        self.assertEqual(len(store), 200)
        for key in keys:
            self.assertIn(key, store)
            self.assertFalse(store.add(key))
        self.assertNotIn(encode_state(("collision", 999)), store)

    def test_snapshot_roundtrip(self):
        store = CompressedStateStore()
        keys = [encode_state(("k", i, (i, i + 1))) for i in range(100)]
        for key in keys:
            store.add(key)
        with tempfile.TemporaryDirectory() as tmp:
            path = Path(tmp) / "store.chk"
            store.save(path)
            loaded = CompressedStateStore.load(path)
        self.assertEqual(len(loaded), len(store))
        self.assertEqual(set(loaded.keys()), set(store.keys()))


if __name__ == "__main__":
    unittest.main()
