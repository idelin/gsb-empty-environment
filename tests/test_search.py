"""跨段 TopK 归并测试：多字段、null、过滤补足、删除位图、稳定性。"""

from __future__ import annotations

import itertools
import unittest

from minisearch import (
    Document,
    Index,
    SearchHit,
    SortField,
    NULLS_FIRST,
    NULLS_LAST,
)
from minisearch.corpus import build_demo_index


def _doc(doc_id: int, text: str, **fields) -> Document:
    return Document(doc_id=doc_id, text=text, fields=fields)


def brute_force_topk(index: Index, query, sort_by, limit, doc_filter=None):
    """无状态对照：枚举所有可见文档，排序后过滤取前 K。"""
    fields = tuple(sort_by)
    out = []
    for segment in index.segments:
        for key, doc, score in segment.iter_ranked(query, fields):
            if doc_filter is None or doc_filter(doc):
                out.append((key, segment.segment_id, doc, score))
    out.sort(key=lambda item: item[0])
    return [(seg_id, doc.doc_id, score) for _key, seg_id, doc, score in out[:limit]]


def naive_per_segment_headk(index: Index, query, sort_by, limit, doc_filter=None):
    """错误做法：每段只取排序后前 K 个原始项，再过滤、归并。"""
    fields = tuple(sort_by)
    picked = []
    for segment in index.segments:
        stream = segment.iter_ranked(query, fields)
        for _ in range(limit):
            try:
                key, doc, score = next(stream)
            except StopIteration:
                break
            if doc_filter is None or doc_filter(doc):
                picked.append((key, segment.segment_id, doc, score))
    picked.sort(key=lambda item: item[0])
    return [(seg_id, doc.doc_id, score) for _key, seg_id, doc, score in picked[:limit]]


class SearchTest(unittest.TestCase):
    def setUp(self) -> None:
        self.index = build_demo_index()

    def test_matches_brute_force_many_cases(self) -> None:
        queries = [None, "keyboard", "monitor 4k", "旗舰", "mechanical keyboard", "usb"]
        sort_specs = [
            [SortField("price")],
            [SortField("price", descending=True)],
            [SortField("rating", descending=True, nulls=NULLS_LAST), SortField("price")],
            [SortField("rating", nulls=NULLS_FIRST)],
            [SortField("updated", descending=True), SortField("doc_id")],
            [SortField("_score", descending=True)],
        ]
        filters = [
            None,
            lambda doc: not doc.fields["promo"],
            lambda doc: doc.fields["flagship"],
            lambda doc: doc.fields["in_stock"] and not doc.fields["promo"],
        ]
        for query, sort_by, doc_filter in itertools.product(queries, sort_specs, filters):
            with self.subTest(query=query, sort=sort_by, filter=bool(doc_filter)):
                got = [
                    (hit.segment_id, hit.doc_id, hit.score)
                    for hit in self.index.search(
                        query, sort_by=sort_by, limit=6, doc_filter=doc_filter
                    )
                ]
                expected = brute_force_topk(
                    self.index, query, sort_by, 6, doc_filter
                )
                self.assertEqual(got, expected)

    def test_filter_refills_when_segment_heads_all_filtered(self) -> None:
        # 段 0 价格最低两项是 doc1(15) 与 doc7(18, 促销)；段 2 最低是
        # doc17(55, 促销)。错误做法每段只取前 K=2 个原始项时，doc6(60) 这种
        # “排在被过滤段头之后”的候选永远没有机会进入归并。
        field = [SortField("price")]
        non_promo = lambda doc: not doc.fields["promo"]

        correct = brute_force_topk(self.index, None, field, 2, non_promo)
        wrong = naive_per_segment_headk(self.index, None, field, 2, non_promo)
        self.assertEqual([doc_id for _seg, doc_id, _s in correct], [1, 6])
        self.assertEqual([doc_id for _seg, doc_id, _s in wrong], [1, 18])
        self.assertNotEqual(correct, wrong)

        hits = self.index.search(None, sort_by=field, limit=2, doc_filter=non_promo)
        self.assertEqual([hit.doc_id for hit in hits], [1, 6])

    def test_filter_refills_full_eight(self) -> None:
        hits = self.index.search(
            None,
            sort_by=[SortField("price")],
            limit=8,
            doc_filter=lambda doc: not doc.fields["promo"],
        )
        ids = [hit.doc_id for hit in hits]
        self.assertEqual(len(hits), 8)
        self.assertEqual(ids, [1, 6, 18, 5, 11, 9, 3, 19])
        prices = [hit.document.fields["price"] for hit in hits]
        self.assertEqual(prices, sorted(prices))

    def test_segment_head_fully_filtered_keeps_reading(self) -> None:
        # query='usb' 命中段0 doc3(100) 与段1 doc14(40, 促销)。
        # 段 1 的段头 doc14 被过滤后，引擎继续读完该段（再无候选），
        # 不报错、不截断，返回剩余满足条件的候选。
        hits = self.index.search(
            "usb",
            sort_by=[SortField("price")],
            limit=3,
            doc_filter=lambda doc: not doc.fields["promo"],
        )
        self.assertEqual([hit.doc_id for hit in hits], [3])

    def test_nulls_first_and_last(self) -> None:
        spec_last = [
            SortField("rating", descending=True, nulls=NULLS_LAST),
            SortField("price"),
        ]
        hits_last = self.index.search("keyboard", sort_by=spec_last, limit=10)
        ratings = [hit.document.fields["rating"] for hit in hits_last]
        self.assertIsNone(ratings[-1])
        non_null_desc = [r for r in ratings if r is not None]
        self.assertEqual(non_null_desc, sorted(non_null_desc, reverse=True))

        spec_first = [SortField("rating", nulls=NULLS_FIRST), SortField("price")]
        hits_first = self.index.search("keyboard", sort_by=spec_first, limit=10)
        ratings_first = [hit.document.fields["rating"] for hit in hits_first]
        self.assertIsNone(ratings_first[0])
        non_null = ratings_first[1:]
        self.assertEqual(non_null, sorted(non_null))

    def test_default_null_position_follows_sql_convention(self) -> None:
        # 默认：升序 NULLS LAST；降序 NULLS FIRST。
        asc_hits = self.index.search(
            "keyboard", sort_by=[SortField("rating")], limit=10
        )
        self.assertIsNone(asc_hits[-1].document.fields["rating"])

        desc_hits = self.index.search(
            "keyboard", sort_by=[SortField("rating", descending=True)], limit=10
        )
        self.assertIsNone(desc_hits[0].document.fields["rating"])

    def test_ties_stable_by_doc_id(self) -> None:
        index = Index()
        index.commit(
            [
                _doc(30, "shared term x", rating=4.0),
                _doc(10, "shared term y", rating=4.0),
                _doc(20, "shared term z", rating=4.0),
            ]
        )
        index.commit(
            [
                _doc(5, "shared term a", rating=4.0),
                _doc(15, "shared term b", rating=None),
            ]
        )
        hits = index.search(
            "shared",
            sort_by=[SortField("rating", descending=True, nulls=NULLS_LAST)],
            limit=5,
        )
        self.assertEqual([h.doc_id for h in hits], [5, 10, 20, 30, 15])

    def test_deleted_docs_invisible_and_refilled(self) -> None:
        self.assertTrue(self.index.delete(10))
        self.assertTrue(self.index.delete(23))
        self.assertFalse(self.index.delete(10))
        hits = self.index.search(
            "keyboard",
            sort_by=[SortField("price")],
            limit=3,
            doc_filter=lambda doc: doc.fields["in_stock"],
        )
        ids = [hit.doc_id for hit in hits]
        self.assertEqual(ids, [9, 19, 2])
        self.assertNotIn(10, ids)
        for hit in hits:
            self.assertFalse(self.index.segment_of(hit.doc_id).is_deleted(hit.doc_id))
            self.assertTrue(hit.document.fields["in_stock"])

    def test_delete_unknown_id(self) -> None:
        self.assertFalse(self.index.delete(9999))

    def test_score_descending_default(self) -> None:
        hits = self.index.search("4k monitor 旗舰", limit=10)
        scores = [hit.score for hit in hits]
        self.assertEqual(scores, sorted(scores, reverse=True))
        # doc4 与 doc13 都同时命中 4k、monitor，相关度应高于只命中 monitor 的文档
        self.assertIn(hits[0].doc_id, (4, 13))

    def test_limit_zero_and_empty_result(self) -> None:
        self.assertEqual(self.index.search(None, limit=0), [])
        self.assertEqual(
            self.index.search(
                "usb",
                doc_filter=lambda doc: doc.fields["flagship"] and not doc.fields["in_stock"],
            ),
            [],
        )

    def test_empty_query_has_no_matches(self) -> None:
        self.assertEqual(self.index.search("", limit=5), [])
        self.assertEqual(self.index.search("   ... !!! ", limit=5), [])

    def test_invalid_arguments(self) -> None:
        with self.assertRaises(ValueError):
            self.index.search(None, limit=-1)
        with self.assertRaises(ValueError):
            SortField("price", nulls="middle")
        mixed = Index()
        mixed.commit([_doc(1, "a", price="expensive")])
        mixed.commit([_doc(2, "b", price=1)])
        with self.assertRaises(TypeError):
            mixed.search(None, sort_by=[SortField("price")])


if __name__ == "__main__":
    unittest.main()
