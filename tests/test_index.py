import threading
import unittest

from indexing.events import Event
from indexing.index import InvertedIndex


def upsert(seq, doc_id, text, version=None):
    return Event(seq=seq, doc_id=doc_id, op="upsert", text=text, version=version)


def delete(seq, doc_id, version=None):
    return Event(seq=seq, doc_id=doc_id, op="delete", version=version)


class AtomicBatchTest(unittest.TestCase):
    def test_batch_is_one_commit(self):
        index = InvertedIndex()
        result = index.apply_batch([upsert(1, "a", "hello hello"), upsert(2, "b", "world")])
        self.assertEqual(result.commit_version, 1)
        self.assertEqual(len(result.applied), 2)
        self.assertEqual(index.search("hello"), {"a": 2})
        self.assertEqual(index.search("world"), {"b": 1})

    def test_snapshot_is_isolated_from_later_commits(self):
        index = InvertedIndex()
        index.apply_batch([upsert(1, "a", "hello")])
        snap = index.snapshot()
        index.apply_batch([upsert(2, "b", "hello"), delete(3, "a")])
        self.assertEqual(snap.commit_version, 1)
        self.assertEqual(snap.search("hello"), {"a": 1})
        self.assertEqual(index.search("hello"), {"b": 1})

    def test_concurrent_readers_never_see_partial_batch(self):
        index = InvertedIndex()
        errors = []
        stop = threading.Event()

        def reader():
            while not stop.is_set():
                snap = index.snapshot()
                hello = snap.search("hello")
                world = snap.search("world")
                # both terms are written by the same batch: never see one alone
                if (len(hello) > 0) != (len(world) > 0):
                    errors.append((hello, world))

        threads = [threading.Thread(target=reader) for _ in range(4)]
        for t in threads:
            t.start()
        seq = 1
        for _ in range(50):
            index.apply_batch([upsert(seq, f"d{seq}", "hello world")])
            seq += 1
        stop.set()
        for t in threads:
            t.join()
        self.assertEqual(errors, [])


class VersionIdempotencyTest(unittest.TestCase):
    def test_reapply_same_batch_is_noop(self):
        index = InvertedIndex()
        events = [upsert(1, "a", "hello hello"), upsert(2, "b", "hello")]
        index.apply_batch(events)
        again = index.apply_batch(events)
        self.assertEqual(len(again.applied), 0)
        self.assertEqual(len(again.skipped), 2)
        self.assertEqual(index.search("hello"), {"a": 2, "b": 1})
        self.assertEqual(again.commit_version, 1)  # no empty commit

    def test_stale_declared_version_is_rejected(self):
        index = InvertedIndex()
        index.apply_batch([upsert(1, "a", "new text", version=5)])
        result = index.apply_batch([upsert(2, "a", "old text", version=3)])
        self.assertEqual(len(result.applied), 0)
        self.assertEqual(index.search("new"), {"a": 1})
        self.assertEqual(index.search("old"), {})

    def test_delete_then_older_versioned_upsert_cannot_resurrect(self):
        index = InvertedIndex()
        index.apply_batch([upsert(1, "a", "hello", version=1), delete(2, "a", version=2)])
        self.assertEqual(index.search("hello"), {})
        result = index.apply_batch([upsert(3, "a", "hello", version=1)])
        self.assertEqual(len(result.applied), 0)
        self.assertEqual(index.search("hello"), {})

    def test_delete_then_newer_upsert_wins(self):
        index = InvertedIndex()
        index.apply_batch([upsert(1, "a", "hello"), delete(2, "a"), upsert(3, "a", "hi")])
        self.assertEqual(index.search("hello"), {})
        self.assertEqual(index.search("hi"), {"a": 1})

    def test_update_replaces_term_counts(self):
        index = InvertedIndex()
        index.apply_batch([upsert(1, "a", "x x x")])
        index.apply_batch([upsert(2, "a", "y")])
        self.assertEqual(index.search("x"), {})
        self.assertEqual(index.search("y"), {"a": 1})


class PersistenceTest(unittest.TestCase):
    def test_roundtrip(self):
        index = InvertedIndex()
        index.apply_batch([upsert(1, "a", "hello 世界"), delete(2, "b")])
        clone = InvertedIndex.from_dict(index.to_dict())
        self.assertEqual(clone.search("hello"), {"a": 1})
        self.assertEqual(clone.search("世"), {"a": 1})
        self.assertEqual(clone.commit_version, 1)
        self.assertEqual(clone._tombstones, {"b": 2})


if __name__ == "__main__":
    unittest.main()
