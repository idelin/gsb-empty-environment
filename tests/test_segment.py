"""段索引：倒排链、删除位图、段内候选不截断。"""

from __future__ import annotations

import math
import unittest

from minisearch import Document, IndexSegment, SortField


def _doc(doc_id: int, text: str, **fields) -> Document:
    return Document(doc_id=doc_id, text=text, fields=fields)


class SegmentTest(unittest.TestCase):
    def setUp(self) -> None:
        self.segment = IndexSegment(
            0,
            [
                _doc(1, "alpha beta", price=10),
                _doc(2, "beta gamma", price=5),
                _doc(3, "alpha gamma", price=10),
            ],
        )

    def test_postings_sorted(self) -> None:
        self.assertEqual(self.segment.postings["alpha"], [1, 3])
        self.assertEqual(self.segment.postings["beta"], [1, 2])

    def test_or_matching(self) -> None:
        self.assertEqual(self.segment.matching_doc_ids("alpha beta"), [1, 2, 3])
        self.assertEqual(self.segment.matching_doc_ids("missing"), [])
        self.assertEqual(self.segment.matching_doc_ids(""), [])
        self.assertEqual(self.segment.matching_doc_ids(None), [1, 2, 3])

    def test_delete_bitmap(self) -> None:
        self.assertTrue(self.segment.delete(2))
        self.assertFalse(self.segment.delete(2))
        self.assertFalse(self.segment.delete(999))
        self.assertTrue(self.segment.is_deleted(2))
        self.assertEqual(self.segment.live_count, 2)
        self.assertEqual(self.segment.matching_doc_ids("beta"), [1])
        self.assertEqual(self.segment.matching_doc_ids(None), [1, 3])

    def test_delete_excluded_from_idf(self) -> None:
        field = SortField("_score", descending=True)
        before = list(self.segment.iter_ranked("beta", (field,)))
        self.segment.delete(2)
        after = list(self.segment.iter_ranked("beta", (field,)))
        self.assertEqual([doc.doc_id for _key, doc, _score in after], [1])
        idf_before = math.log((3 + 1) / (2 + 1)) + 1.0
        idf_after = math.log((2 + 1) / (1 + 1)) + 1.0
        self.assertNotAlmostEqual(
            before[0][2], after[0][2]
        )
        self.assertGreater(after[0][2], before[0][2])
        self.assertGreater(idf_after, idf_before)

    def test_segment_yields_all_candidates_not_topk(self) -> None:
        field = SortField("price")
        ids = [doc.doc_id for _key, doc, _score in self.segment.iter_ranked("beta", (field,))]
        self.assertEqual(ids, [2, 1])

    def test_duplicate_doc_id_rejected(self) -> None:
        with self.assertRaises(ValueError):
            IndexSegment(1, [_doc(1, "a"), _doc(1, "b")])


if __name__ == "__main__":
    unittest.main()
