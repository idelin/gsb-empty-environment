import unittest

from topk import Document, SortField, sort_key


def doc(doc_id, **fields):
    return Document(doc_id=doc_id, fields=fields)


class TestSortSpec(unittest.TestCase):
    def test_multi_field_mixed_direction(self):
        docs = [
            doc(1, score=90, ts=5),
            doc(2, score=90, ts=3),
            doc(3, score=80, ts=1),
            doc(4, score=90, ts=3),
        ]
        spec = [SortField("score", desc=True), SortField("ts", desc=False)]
        ordered = [d.doc_id for d in sorted(docs, key=sort_key(spec))]
        # score 降序优先；同分 ts 升序；再同分按 doc_id 升序
        self.assertEqual(ordered, [2, 4, 1, 3])

    def test_nulls_last_default(self):
        docs = [doc(1, v=None), doc(2, v=1), doc(3, v=2)]
        spec = [SortField("v")]
        self.assertEqual([d.doc_id for d in sorted(docs, key=sort_key(spec))], [2, 3, 1])

    def test_nulls_first(self):
        docs = [doc(1, v=None), doc(2, v=1), doc(3, v=2)]
        spec = [SortField("v", nulls_first=True)]
        self.assertEqual([d.doc_id for d in sorted(docs, key=sort_key(spec))], [1, 2, 3])

    def test_null_position_independent_of_direction(self):
        docs = [doc(1, v=None), doc(2, v=1), doc(3, v=2)]
        # 降序 + NULLS LAST：null 仍在最后
        spec = [SortField("v", desc=True, nulls_first=False)]
        self.assertEqual([d.doc_id for d in sorted(docs, key=sort_key(spec))], [3, 2, 1])
        # 降序 + NULLS FIRST：null 仍在最前
        spec = [SortField("v", desc=True, nulls_first=True)]
        self.assertEqual([d.doc_id for d in sorted(docs, key=sort_key(spec))], [1, 3, 2])

    def test_missing_field_is_null(self):
        docs = [doc(1), doc(2, v=0)]
        spec = [SortField("v")]
        self.assertEqual([d.doc_id for d in sorted(docs, key=sort_key(spec))], [2, 1])

    def test_tie_break_by_doc_id(self):
        docs = [doc(9, v=1), doc(3, v=1), doc(7, v=1)]
        spec = [SortField("v")]
        self.assertEqual([d.doc_id for d in sorted(docs, key=sort_key(spec))], [3, 7, 9])


if __name__ == "__main__":
    unittest.main()
