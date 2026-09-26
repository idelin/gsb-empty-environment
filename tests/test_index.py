import unittest

from invidx import Index, PhraseQuery


def make_index():
    idx = Index()
    idx.add_document("d1", {"body": "the quick brown fox"})
    idx.add_document("d2", {"body": "quick quick fox", "title": "fox"})
    idx.commit()
    idx.add_document("d3", {"body": "the lazy dog"})
    idx.commit()
    return idx


class TestSegmentsAndDeletion(unittest.TestCase):
    def test_segments_accumulate(self):
        idx = make_index()
        self.assertEqual(idx.segment_count(), 2)

    def test_doc_freq_syncs_with_delete(self):
        idx = make_index()
        self.assertEqual(idx.doc_freq("quick"), 2)
        self.assertEqual(idx.doc_freq("fox"), 2)
        idx.delete("d1")
        self.assertEqual(idx.doc_freq("quick"), 1)
        self.assertEqual(idx.doc_freq("fox"), 1)
        idx.delete("d2")
        self.assertEqual(idx.doc_freq("quick"), 0)
        self.assertEqual(idx.doc_freq("fox"), 0)

    def test_deleted_doc_positions_stop_matching(self):
        idx = make_index()
        q = PhraseQuery.of("quick fox", slop=1)
        self.assertEqual(sorted(m.docid for m in idx.search(q)), ["d1", "d2"])
        idx.delete("d2")
        self.assertEqual([m.docid for m in idx.search(q)], ["d1"])

    def test_cursor_skips_deleted(self):
        idx = make_index()
        idx.delete("d2")
        seen = []
        cur = idx.cursor()
        while not cur.exhausted():
            seen.extend(docid for docid, _ in cur.next_batch(2))
        self.assertEqual(seen, ["d1", "d3"])

    def test_duplicate_docid_rejected(self):
        idx = make_index()
        with self.assertRaises(ValueError):
            idx.add_document("d1", {"body": "again"})


class TestMerge(unittest.TestCase):
    def test_merge_compacts_and_preserves_results(self):
        idx = make_index()
        idx.delete("d1")
        q = PhraseQuery.of("quick fox", slop=1)
        before = [(m.docid, m.pos_start, m.pos_end) for m in idx.search(q)]
        idx.merge()
        self.assertEqual(idx.segment_count(), 1)
        after = [(m.docid, m.pos_start, m.pos_end) for m in idx.search(q)]
        self.assertEqual(before, after)
        self.assertEqual(idx.doc_freq("quick"), 1)
        self.assertEqual(idx.cursor()._docids, ["d2", "d3"])

    def test_merge_keeps_live_docs_searchable(self):
        idx = make_index()
        idx.merge()
        self.assertEqual(idx.doc_freq("lazy"), 1)
        self.assertEqual(len(idx.search(PhraseQuery.of("lazy dog"))), 1)


class TestSnapshot(unittest.TestCase):
    def test_snapshot_isolated_from_later_writes(self):
        idx = make_index()
        snap = idx.snapshot()
        idx.add_document("d4", {"body": "quick snapshot fox"})
        idx.commit()
        idx.delete("d1")
        self.assertEqual(snap.live_docids(), ["d1", "d2", "d3"])
        self.assertEqual(snap.doc_freq("quick"), 2)
        q = PhraseQuery.of("quick fox", slop=1)
        self.assertEqual(sorted(m.docid for m in snap.search(q)), ["d1", "d2"])
        self.assertEqual(sorted(m.docid for m in idx.search(q)), ["d2", "d4"])

    def test_snapshot_survives_merge(self):
        idx = make_index()
        snap = idx.snapshot()
        idx.delete("d2")
        idx.merge()
        self.assertEqual(snap.live_docids(), ["d1", "d2", "d3"])
        self.assertEqual(snap.doc_freq("quick"), 2)
        self.assertEqual(idx.doc_freq("quick"), 1)

    def test_snapshot_cursor_resume(self):
        idx = make_index()
        snap = idx.snapshot()
        cur = snap.cursor()
        first = cur.next_batch(2)
        self.assertEqual([d for d, _ in first], ["d1", "d2"])
        token = cur.position
        cur2 = snap.cursor(after=token)
        rest = cur2.next_batch(10)
        self.assertEqual([d for d, _ in rest], ["d3"])


class TestCrossField(unittest.TestCase):
    def test_phrase_cannot_span_fields(self):
        idx = Index()
        idx.add_document("doc", {"title": "hello", "body": "world"})
        idx.commit()
        self.assertEqual(idx.search(PhraseQuery.of("hello world")).matches, [])
        idx.add_document("doc2", {"body": "hello world"})
        self.assertEqual(len(idx.search(PhraseQuery.of("hello world"))), 1)


if __name__ == "__main__":
    unittest.main()

