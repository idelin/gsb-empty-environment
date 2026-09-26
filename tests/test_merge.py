import unittest

from topk import Document, Field, Segment, SortField, merge_topk


def doc(doc_id, **fields):
    return Document(doc_id=doc_id, fields=fields)


SCORE_DESC = [SortField("score", desc=True, nulls_first=False)]


class TestMergeBasic(unittest.TestCase):
    def test_k_way_merge_order(self):
        seg_a = Segment("a", [doc(1, score=90), doc(4, score=70), doc(6, score=50)])
        seg_b = Segment("b", [doc(2, score=85), doc(3, score=80), doc(5, score=60)])
        result = merge_topk([seg_a, seg_b], SCORE_DESC, k=4)
        self.assertEqual([d.doc_id for d in result], [1, 2, 3, 4])

    def test_k_zero_and_negative(self):
        seg = Segment("a", [doc(1, score=1)])
        self.assertEqual(merge_topk([seg], SCORE_DESC, k=0), [])
        self.assertEqual(merge_topk([seg], SCORE_DESC, k=-3), [])

    def test_k_larger_than_total_returns_all(self):
        seg = Segment("a", [doc(1, score=1), doc(2, score=2)])
        result = merge_topk([seg], SCORE_DESC, k=100)
        self.assertEqual([d.doc_id for d in result], [2, 1])

    def test_empty_segments(self):
        self.assertEqual(merge_topk([], SCORE_DESC, k=5), [])
        self.assertEqual(merge_topk([Segment("a", [])], SCORE_DESC, k=5), [])

    def test_tie_break_by_doc_id_across_segments(self):
        seg_a = Segment("a", [doc(9, score=10), doc(3, score=10)])
        seg_b = Segment("b", [doc(7, score=10), doc(1, score=10)])
        result = merge_topk([seg_a, seg_b], SCORE_DESC, k=4)
        self.assertEqual([d.doc_id for d in result], [1, 3, 7, 9])


class TestMergeNullAndDirection(unittest.TestCase):
    def test_nulls_last_in_merge(self):
        seg_a = Segment("a", [doc(1, score=None), doc(2, score=5)])
        seg_b = Segment("b", [doc(3, score=3), doc(4, score=None)])
        result = merge_topk([seg_a, seg_b], SCORE_DESC, k=4)
        self.assertEqual([d.doc_id for d in result], [2, 3, 1, 4])

    def test_nulls_first_with_asc(self):
        spec = [SortField("score", desc=False, nulls_first=True)]
        seg_a = Segment("a", [doc(1, score=None), doc(2, score=5)])
        seg_b = Segment("b", [doc(3, score=1)])
        result = merge_topk([seg_a, seg_b], spec, k=3)
        self.assertEqual([d.doc_id for d in result], [1, 3, 2])

    def test_multi_field_merge(self):
        spec = [SortField("lang"), SortField("score", desc=True)]
        seg_a = Segment("a", [doc(1, lang="zh", score=5), doc(2, lang="en", score=9)])
        seg_b = Segment("b", [doc(3, lang="zh", score=7), doc(4, lang="en", score=1)])
        result = merge_topk([seg_a, seg_b], spec, k=4)
        # lang 升序：en 在前；同 lang 内 score 降序
        self.assertEqual([d.doc_id for d in result], [2, 4, 3, 1])


class TestMergeFilterRefill(unittest.TestCase):
    def test_filtered_candidates_are_refilled_not_prefixed(self):
        # 段 a 的原始前 3 名全部被过滤。若错误地"先取各段前 K=3 再过滤"，
        # 段 a 将贡献 0 条；正确实现应继续向后读，补足候选。
        seg_a = Segment(
            "a",
            [
                doc(1, score=99, lang="en"),
                doc(2, score=98, lang="en"),
                doc(3, score=97, lang="en"),
                doc(4, score=96, lang="zh"),
                doc(5, score=95, lang="zh"),
            ],
        )
        seg_b = Segment("b", [doc(6, score=94, lang="zh"), doc(7, score=93, lang="zh")])
        result = merge_topk(
            [seg_a, seg_b], SCORE_DESC, k=3, filter=Field("lang") == "zh"
        )
        self.assertEqual([d.doc_id for d in result], [4, 5, 6])

    def test_segment_head_fully_filtered_keeps_reading(self):
        # 段 a 头部连续多条被过滤，必须继续读取段内后续文档
        seg_a = Segment(
            "a",
            [doc(i, score=100 - i, lang="en") for i in range(1, 9)]
            + [doc(9, score=90, lang="zh")],
        )
        result = merge_topk([seg_a], SCORE_DESC, k=1, filter=Field("lang") == "zh")
        self.assertEqual([d.doc_id for d in result], [9])

    def test_whole_segment_filtered_out(self):
        seg_a = Segment("a", [doc(1, score=99, lang="en"), doc(2, score=98, lang="en")])
        seg_b = Segment("b", [doc(3, score=1, lang="zh")])
        result = merge_topk(
            [seg_a, seg_b], SCORE_DESC, k=2, filter=Field("lang") == "zh"
        )
        self.assertEqual([d.doc_id for d in result], [3])

    def test_filter_with_token_match(self):
        seg_a = Segment(
            "a",
            [
                doc(1, score=9, body="介绍归并排序"),
                doc(2, score=8, body="介绍冒泡排序"),
            ],
        )
        seg_b = Segment("b", [doc(3, score=7, body="归并与分治")])
        result = merge_topk(
            [seg_a, seg_b], SCORE_DESC, k=2, filter=Field("body").contains_token("归")
        )
        self.assertEqual([d.doc_id for d in result], [1, 3])

    def test_filter_null_field(self):
        seg = Segment(
            "a",
            [doc(1, score=9, tag=None), doc(2, score=8, tag="x"), doc(3, score=7)],
        )
        result = merge_topk([seg], SCORE_DESC, k=2, filter=Field("tag").is_null())
        self.assertEqual([d.doc_id for d in result], [1, 3])


class TestMergeDeletionBitmap(unittest.TestCase):
    def test_deleted_docs_are_invisible(self):
        seg_a = Segment("a", [doc(1, score=99), doc(2, score=80)], deleted={1})
        seg_b = Segment("b", [doc(3, score=90)])
        result = merge_topk([seg_a, seg_b], SCORE_DESC, k=3)
        self.assertEqual([d.doc_id for d in result], [3, 2])

    def test_delete_after_build(self):
        seg = Segment("a", [doc(1, score=1), doc(2, score=2), doc(3, score=3)])
        seg.delete(3)
        seg.delete(2)
        result = merge_topk([seg], SCORE_DESC, k=5)
        self.assertEqual([d.doc_id for d in result], [1])

    def test_deleted_head_does_not_block_refill(self):
        # 段头被删除时同样继续向后读
        seg = Segment(
            "a",
            [doc(1, score=99), doc(2, score=98), doc(3, score=97)],
            deleted={1, 2},
        )
        result = merge_topk([seg], SCORE_DESC, k=1)
        self.assertEqual([d.doc_id for d in result], [3])


if __name__ == "__main__":
    unittest.main()
