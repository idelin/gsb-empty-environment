import shutil
import tempfile
import unittest
from pathlib import Path

from indexing.events import Event
from indexing.pipeline import IndexingPipeline


def upsert(seq, doc_id, text, version=None):
    return Event(seq=seq, doc_id=doc_id, op="upsert", text=text, version=version)


def delete(seq, doc_id, version=None):
    return Event(seq=seq, doc_id=doc_id, op="delete", version=version)


class PipelineTest(unittest.TestCase):
    def setUp(self):
        self.dir = Path(tempfile.mkdtemp(prefix="idx-pipeline-"))

    def tearDown(self):
        shutil.rmtree(self.dir)

    def _pipeline(self):
        return IndexingPipeline(self.dir)

    def test_in_order_commit_and_search(self):
        p = self._pipeline()
        p.submit(upsert(1, "a", "hello world"))
        p.submit(upsert(2, "b", "hello"))
        result = p.commit()
        self.assertEqual(result.committed, 2)
        self.assertEqual(p.checkpoint, 2)
        self.assertEqual(p.search("hello"), {"a": 1, "b": 1})
        self.assertEqual(p.search("world"), {"a": 1})

    def test_out_of_order_is_buffered_and_missing_is_reported(self):
        p = self._pipeline()
        r2 = p.submit(upsert(2, "b", "hello"))
        self.assertEqual(r2.status, "buffered")
        self.assertEqual(r2.missing, ((1, 1),))
        self.assertEqual(p.missing_seqs(), [(1, 1)])
        r1 = p.submit(upsert(1, "a", "world"))
        self.assertEqual(r1.status, "drained")
        self.assertEqual(r1.drained, 2)
        self.assertEqual(p.missing_seqs(), [])
        p.commit()
        self.assertEqual(p.search("hello"), {"b": 1})
        self.assertEqual(p.search("world"), {"a": 1})

    def test_duplicate_events_are_idempotent(self):
        p = self._pipeline()
        e = upsert(1, "a", "hello hello")
        p.submit(e)
        p.commit()
        dup = p.submit(e)
        self.assertEqual(dup.status, "duplicate")
        p.commit()
        self.assertEqual(p.search("hello"), {"a": 2})
        self.assertEqual(p.stats()["duplicates_dropped"], 1)

    def test_duplicate_buffered_seq_does_not_replace_original(self):
        p = self._pipeline()
        p.submit(upsert(3, "c", "first"))
        p.submit(upsert(3, "c", "second"))  # duplicate, must not overwrite
        p.submit(upsert(2, "b", "b"))
        p.submit(upsert(1, "a", "a"))
        p.commit()
        self.assertEqual(p.search("first"), {"c": 1})
        self.assertEqual(p.search("second"), {})

    def test_newer_delete_arriving_first_waits_for_gap(self):
        p = self._pipeline()
        d = p.submit(delete(3, "a"))
        self.assertEqual(d.status, "buffered")  # delete seq=3 waits
        self.assertEqual(d.missing, ((1, 2),))
        p.submit(upsert(1, "a", "hello"))
        p.submit(upsert(2, "b", "world"))
        self.assertEqual(p.stats()["pending_batch"], 3)
        p.commit()
        self.assertEqual(p.search("hello"), {})  # upsert then delete applied in seq order
        self.assertEqual(p.search("world"), {"b": 1})
        self.assertEqual(p.index._tombstones, {"a": 3})

    def test_restart_does_not_double_count_and_rejects_replay(self):
        p = self._pipeline()
        p.submit(upsert(1, "a", "hello hello"))
        p.submit(upsert(2, "b", "hello"))
        p.commit()

        reborn = self._pipeline()
        self.assertEqual(reborn.checkpoint, 2)
        self.assertEqual(reborn.search("hello"), {"a": 2, "b": 1})
        replay = reborn.submit(upsert(1, "a", "hello hello"))
        self.assertEqual(replay.status, "duplicate")
        reborn.commit()
        self.assertEqual(reborn.search("hello"), {"a": 2, "b": 1})
        self.assertEqual(reborn.stats()["duplicates_dropped"], 1)

    def test_restart_preserves_pending_batch_and_buffer(self):
        p = self._pipeline()
        p.submit(upsert(1, "a", "hello"))
        p.submit(upsert(4, "d", "world"))  # gap: 2,3 buffered ahead
        # crash before commit
        reborn = self._pipeline()
        self.assertEqual(reborn.checkpoint, 0)
        self.assertEqual(len(reborn.pending_batch), 1)
        self.assertEqual(reborn.missing_seqs(), [(2, 3)])
        reborn.submit(upsert(2, "b", "x"))
        reborn.submit(upsert(3, "c", "y"))
        reborn.commit()
        self.assertEqual(reborn.checkpoint, 4)
        self.assertEqual(reborn.search("hello"), {"a": 1})
        self.assertEqual(reborn.search("world"), {"d": 1})

    def test_declared_version_override(self):
        p = self._pipeline()
        p.submit(upsert(1, "a", "old content", version=10))
        p.commit()
        p.submit(upsert(2, "a", "new content", version=5))  # stale override rejected
        result = p.commit()
        self.assertEqual(len(result.batch.applied), 0)
        self.assertEqual(p.search("old"), {"a": 1})
        self.assertEqual(p.search("new"), {})

    def test_event_validation_rejects_bad_sequences(self):
        with self.assertRaises(ValueError):
            Event(seq=0, doc_id="a", op="upsert", text="x")
        with self.assertRaises(ValueError):
            Event(seq=1, doc_id="a", op="upsert")
        with self.assertRaises(ValueError):
            Event(seq=1, doc_id="a", op="delete", text="x")


if __name__ == "__main__":
    unittest.main()
