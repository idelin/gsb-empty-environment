"""趋势索引与校验器的单元测试（Python 3.11，标准库 unittest）。"""

import unittest

from trend import TrendIndex, tokenize
from trend.checker import full_recompute_check

DAY = 86400


def ts(day, sec=0):
    return day * DAY + sec


def brute_force_topk(idx, base, recent, k):
    """不剪枝的参考实现：直接对两个窗口全量聚合后排序。"""
    base_docs, base_df = idx.window_stats(*base)
    recent_docs, recent_df = idx.window_stats(*recent)
    if recent_docs == 0 or k <= 0:
        return []
    rows = [
        (t, df, base_df.get(t, 0), (df + idx.alpha) / (base_df.get(t, 0) + idx.alpha))
        for t, df in recent_df.items()
        if df >= idx.min_support
    ]
    rows.sort(key=lambda r: (-r[3], -r[1], r[0]))
    return rows[:k]


class TestTokenizer(unittest.TestCase):
    def test_casefold_and_punctuation(self):
        self.assertEqual(tokenize("Hello, WORLD!"), ["hello", "world"])

    def test_underscore_is_separator(self):
        self.assertEqual(tokenize("a_b"), ["a", "b"])

    def test_cjk_run_is_single_token(self):
        self.assertEqual(tokenize("中文 文本"), ["中文", "文本"])

    def test_mixed_and_digits(self):
        self.assertEqual(tokenize("gpt-4o 发布"), ["gpt", "4o", "发布"])

    def test_empty(self):
        self.assertEqual(tokenize("！!，。"), [])


class TestIndexBasics(unittest.TestCase):
    def setUp(self):
        self.idx = TrendIndex(bucket_size=DAY, alpha=1.0, min_support=1)

    def test_repeated_term_counts_once_per_document(self):
        self.idx.add_document("d1", ts(0), "apple apple apple")
        self.idx.add_document("d2", ts(0), "apple banana")
        _, df = self.idx.window_stats(ts(0), ts(1))
        self.assertEqual(df["apple"], 2)    # 文档频率，不按出现次数
        self.assertEqual(df["banana"], 1)

    def test_denominator_by_visible_doc_identity(self):
        self.idx.add_document("d1", ts(0), "a")
        self.idx.add_document("d2", ts(0), "b")
        docs, _ = self.idx.window_stats(ts(0), ts(1))
        self.assertEqual(docs, 2)
        self.idx.retract_document("d2")
        docs, _ = self.idx.window_stats(ts(0), ts(1))
        self.assertEqual(docs, 1)  # 分母随可见文档身份变化

    def test_late_document_revises_old_bucket(self):
        self.idx.add_document("early", ts(5), "x")
        self.idx.add_document("late", ts(0, 100), "y")  # 迟到，落入第 0 桶
        _, df0 = self.idx.window_stats(ts(0), ts(1))
        _, df5 = self.idx.window_stats(ts(5), ts(6))
        self.assertEqual(df0, {"y": 1})
        self.assertEqual(df5, {"x": 1})

    def test_revision_replaces_old_bucket_content(self):
        self.idx.add_document("d1", ts(0), "alpha")
        self.idx.add_document("d1", ts(1), "beta")  # 同 id 修订，跨桶
        self.assertEqual(self.idx.revisions, 1)
        docs0, df0 = self.idx.window_stats(ts(0), ts(1))
        docs1, df1 = self.idx.window_stats(ts(1), ts(2))
        self.assertEqual((docs0, df0), (0, {}))
        self.assertEqual((docs1, df1), (1, {"beta": 1}))

    def test_duplicate_event_is_idempotent(self):
        self.idx.add_document("d1", ts(0), "alpha beta")
        changed = self.idx.add_document("d1", ts(0), "alpha beta")
        self.assertFalse(changed)
        self.assertEqual(self.idx.duplicate_events, 1)
        docs, df = self.idx.window_stats(ts(0), ts(1))
        self.assertEqual((docs, df), (1, {"alpha": 1, "beta": 1}))

    def test_retraction_removes_term_and_denominator(self):
        self.idx.add_document("d1", ts(0), "alpha")
        self.idx.add_document("d2", ts(0), "alpha")
        self.assertTrue(self.idx.retract_document("d1"))
        self.assertFalse(self.idx.retract_document("d1"))  # 重复撤回无效
        docs, df = self.idx.window_stats(ts(0), ts(1))
        self.assertEqual((docs, df), (1, {"alpha": 1}))


class TestScoring(unittest.TestCase):
    def test_zero_to_one_is_smoothed_not_infinite(self):
        idx = TrendIndex(bucket_size=DAY, alpha=1.0, min_support=1)
        idx.add_document("r1", ts(10), "newterm")
        [rec] = idx.top_trends((ts(0), ts(7)), (ts(7), ts(14)), k=10)
        self.assertEqual(rec.term, "newterm")
        self.assertEqual(rec.df_base, 0)
        self.assertAlmostEqual(rec.score, 2.0)  # (1+1)/(0+1)，有限值

    def test_empty_recent_window_reports_no_growth(self):
        idx = TrendIndex(bucket_size=DAY, alpha=1.0, min_support=1)
        idx.add_document("b1", ts(0), "oldterm")
        self.assertEqual(idx.top_trends((ts(0), ts(7)), (ts(7), ts(14)), k=10), [])

    def test_min_support_filters(self):
        idx = TrendIndex(bucket_size=DAY, alpha=1.0, min_support=2)
        idx.add_document("r1", ts(10), "solo")
        idx.add_document("r2", ts(10), "duo")
        idx.add_document("r3", ts(11), "duo")
        terms = [r.term for r in idx.top_trends((ts(0), ts(7)), (ts(7), ts(14)), k=10)]
        self.assertEqual(terms, ["duo"])  # solo 支持数 1 被过滤

    def test_raw_counts_and_denominators_in_output(self):
        idx = TrendIndex(bucket_size=DAY, alpha=1.0, min_support=1)
        idx.add_document("b1", ts(0), "hot")
        idx.add_document("b2", ts(1), "hot cold")
        idx.add_document("r1", ts(10), "hot")
        idx.add_document("r2", ts(11), "hot")
        idx.add_document("r3", ts(12), "warm")
        recs = {r.term: r for r in idx.top_trends((ts(0), ts(7)), (ts(7), ts(14)), k=10)}
        rec = recs["hot"]
        self.assertEqual((rec.df_recent, rec.df_base), (2, 2))
        self.assertEqual((rec.recent_docs, rec.base_docs), (3, 2))
        self.assertAlmostEqual(rec.score, 1.0)  # (2+1)/(2+1)
        # warm 是 0->1 新词，平滑分 (1+1)/(0+1)=2.0 高于 hot，排第一
        self.assertEqual(recs["warm"].score, 2.0)


class TestTopKPruning(unittest.TestCase):
    def test_topk_matches_brute_force(self):
        idx = TrendIndex(bucket_size=3600, alpha=0.5, min_support=2)
        hot = ["alpha", "beta", "gamma"]
        tail = [f"tail-{i}" for i in range(60)]  # 大量长尾词，应被 TopK 界剪枝
        # 确定性伪随机文档流，覆盖多个桶
        for i in range(300):
            bucket = (i * 7 + 3) % 20
            words = [hot[i % len(hot)], hot[(i // 3) % len(hot)], tail[i % len(tail)]]
            idx.add_document(f"doc-{i}", bucket * 3600 + (i % 50), " ".join(words))
        base, recent, k = (0, 10 * 3600), (10 * 3600, 20 * 3600), 5
        pruned = [(r.term, r.df_recent, r.df_base, r.score)
                  for r in idx.top_trends(base, recent, k)]
        reference = [(t, df, bdf, score) for t, df, bdf, score in brute_force_topk(idx, base, recent, k)]
        self.assertEqual(pruned, reference)
        self.assertGreater(idx.pruned_candidates, 0)  # 剪枝确实生效

    def test_sliding_window_advance(self):
        idx = TrendIndex(bucket_size=DAY, alpha=1.0, min_support=1)
        idx.add_document("d1", ts(0), "alpha")
        idx.add_document("d2", ts(7), "beta")
        first = idx.top_trends((ts(0), ts(7)), (ts(7), ts(14)), k=5)
        self.assertEqual([r.term for r in first], ["beta"])
        # 窗口整体前移后，旧文档退出近期窗
        second = idx.top_trends((ts(7), ts(14)), (ts(14), ts(21)), k=5)
        self.assertEqual(second, [])


class TestChecker(unittest.TestCase):
    def _build(self):
        idx = TrendIndex(bucket_size=DAY, alpha=1.0, min_support=1)
        idx.add_document("b1", ts(1), "apple 苹果")
        idx.add_document("b2", ts(2), "apple")
        idx.add_document("r1", ts(10), "apple 量子")
        idx.add_document("r2", ts(11), "量子")
        idx.add_document("r2", ts(11), "量子")  # 重复事件
        return idx, (ts(0), ts(7)), (ts(7), ts(14))

    def test_consistent_index_passes(self):
        idx, base, recent = self._build()
        report = full_recompute_check(idx, base, recent, k=5)
        self.assertTrue(report.ok)
        self.assertEqual(report.corrections, [])
        self.assertEqual(report.duplicate_events, 1)  # 重复事件如实上报
        self.assertEqual(report.docs_checked, 4)      # 只重算窗口内文档

    def test_checker_detects_df_corruption_with_evidence(self):
        idx, base, recent = self._build()
        # 注入损坏：篡改增量桶结构（模拟增量维护 bug）
        idx._bucket_terms[idx.bucket_of(ts(10))]["apple"].add("ghost-doc")
        report = full_recompute_check(idx, base, recent, k=5)
        self.assertFalse(report.ok)
        df_corr = [c for c in report.corrections if c.kind == "df" and c.term == "apple"]
        self.assertEqual(len(df_corr), 1)
        self.assertEqual((df_corr[0].incremental, df_corr[0].recomputed), (2, 1))
        # 贡献证据指向真实贡献文档
        self.assertEqual(df_corr[0].evidence["doc_ids"], ["r1"])

    def test_checker_reports_ranking_correction_with_evidence(self):
        idx, base, recent = self._build()
        # 注入足以翻转排名的损坏：apple 的近期 DF 被虚增
        ids = idx._bucket_terms[idx.bucket_of(ts(10))]["apple"]
        for i in range(10):
            ids.add(f"ghost-{i}")
        report = full_recompute_check(idx, base, recent, k=5)
        self.assertFalse(report.ok)
        rank_corr = [c for c in report.corrections if c.kind == "ranking"]
        self.assertEqual(len(rank_corr), 1)
        # 排名更正携带每个涉及词在两个窗口中的贡献 doc_id 证据
        evidence = rank_corr[0].evidence
        self.assertEqual(evidence["apple"]["recent_doc_ids"], ["r1"])
        self.assertEqual(evidence["apple"]["base_doc_ids"], ["b1", "b2"])
        self.assertEqual(evidence["量子"]["recent_doc_ids"], ["r1", "r2"])

    def test_checker_detects_denominator_corruption(self):
        idx, base, recent = self._build()
        idx._bucket_docs[idx.bucket_of(ts(10))].add("ghost")
        report = full_recompute_check(idx, base, recent, k=5)
        kinds = {c.kind for c in report.corrections}
        self.assertIn("denominator", kinds)

    def test_checker_zero_window(self):
        idx = TrendIndex(bucket_size=DAY, alpha=1.0, min_support=1)
        idx.add_document("b1", ts(1), "apple")
        report = full_recompute_check(idx, (ts(0), ts(7)), (ts(7), ts(14)), k=5)
        self.assertTrue(report.ok)
        self.assertEqual(report.expected_ranking, [])


if __name__ == "__main__":
    unittest.main()
