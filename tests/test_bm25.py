"""BM25 实现的单元测试（仅标准库 unittest）。

覆盖：分词规则、手算复核公式、重复查询词、增删统计维护、空文档不除零、
删除后平均长度更新、同分按 ID、块级剪枝与全量参考对照、失效上界不可用、
快照固定统计、剪枝证据、强制构造的最优项在最后一块 / 高频词更新场景。
"""

import math
import random
import unittest
from collections import Counter

from bm25 import BM25Index, Snapshot, tokenize


def reference_rank(docs, query, k, k1=1.5, b=0.75):
    """测试内独立编写的全量评分参考：从原始词列表重算全部统计。"""
    n = len(docs)
    avgdl = sum(len(t) for t in docs.values()) / n if n else 0.0
    df = {}
    for toks in docs.values():
        for t in set(toks):
            df[t] = df.get(t, 0) + 1
    scores = {}
    for d, toks in docs.items():
        tf = Counter(toks)
        s = 0.0
        for t in tokenize(query):
            f = tf.get(t, 0)
            if not f:
                continue
            idf = math.log(1.0 + (n - df[t] + 0.5) / (df[t] + 0.5))
            norm = k1 * (1 - b + b * len(toks) / avgdl) if avgdl else k1 * (1 - b)
            s += idf * f * (k1 + 1.0) / (f + norm)
        if s > 0.0:
            scores[d] = s
    return sorted(scores.items(), key=lambda x: (-x[1], x[0]))[:k]


class TestTokenizer(unittest.TestCase):
    def test_ascii_lowercase_and_digits(self):
        self.assertEqual(tokenize("BM25 is GREAT!"), ["bm25", "is", "great"])

    def test_cjk_char_per_token(self):
        self.assertEqual(tokenize("苹果Apple香蕉"), ["苹", "果", "apple", "香", "蕉"])

    def test_separators(self):
        self.assertEqual(tokenize("a1_b2...c3"), ["a1", "b2", "c3"])
        self.assertEqual(tokenize("3.14"), ["3", "14"])

    def test_no_tokens(self):
        self.assertEqual(tokenize("！！！ 🎉"), [])
        self.assertEqual(tokenize(""), [])


class TestFormulaHandCheck(unittest.TestCase):
    def setUp(self):
        self.idx = BM25Index(k1=1.2, b=0.5)
        self.idx.add_document(0, "apple apple banana")
        self.idx.add_document(1, "apple")
        self.idx.add_document(2, "banana banana banana")

    def test_hand_computed_term_score(self):
        avgdl = 7.0 / 3.0
        idf_a = math.log(1.0 + (3 - 2 + 0.5) / (2 + 0.5))
        norm0 = 1.2 * (1 - 0.5 + 0.5 * 3 / avgdl)
        expected = idf_a * 2 * 2.2 / (2 + norm0)
        self.assertAlmostEqual(self.idx.score_document(["apple"], 0), expected, places=15)

    def test_duplicate_query_terms_add_up(self):
        # 规则：重复查询词每次出现独立累加 => 恰为单项两倍（浮点精确）
        single = self.idx.score_document(["apple"], 0)
        double = self.idx.score_document(["apple", "apple"], 0)
        self.assertEqual(double, 2.0 * single)
        self.assertEqual(self.idx.search("apple apple", 1),
                         self.idx.search_full("apple apple", 1))

    def test_hand_computed_ranking(self):
        docs = {0: tokenize("apple apple banana"),
                1: tokenize("apple"),
                2: tokenize("banana banana banana")}
        for q in ("apple", "banana", "apple banana", "apple apple"):
            for k in (1, 2, 3, 5):
                self.assertEqual(self.idx.search(q, k),
                                 reference_rank(docs, q, k, k1=1.2, b=0.5))


class TestStatsMaintenance(unittest.TestCase):
    def test_add_delete_stats(self):
        idx = BM25Index()
        idx.add_document(0, "apple banana")
        idx.add_document(1, "a b c d")
        idx.add_document(2, "p q r s t u")
        self.assertEqual(idx.n_docs, 3)
        self.assertEqual(idx.total_len, 12)
        self.assertEqual(idx.avgdl, 4.0)
        self.assertEqual(idx.df["apple"], 1)
        self.assertEqual(idx.df["a"], 1)

        # 删除文档后平均长度与 df 同步更新
        idx.delete_document(2)
        self.assertEqual(idx.n_docs, 2)
        self.assertEqual(idx.total_len, 6)
        self.assertEqual(idx.avgdl, 3.0)
        self.assertNotIn("p", idx.df)
        self.assertNotIn("p", idx.postings)

        idx.delete_document(0)
        self.assertEqual(idx.df.get("apple", 0), 0)
        self.assertEqual(idx.avgdl, 4.0)

    def test_delete_unknown_raises(self):
        idx = BM25Index()
        with self.assertRaises(KeyError):
            idx.delete_document(99)

    def test_duplicate_add_raises(self):
        idx = BM25Index()
        idx.add_document(0, "apple")
        with self.assertRaises(ValueError):
            idx.add_document(0, "other")

    def test_non_int_id_raises(self):
        idx = BM25Index()
        with self.assertRaises(TypeError):
            idx.add_document("doc-1", "apple")


class TestEmptyDocuments(unittest.TestCase):
    def test_empty_docs_no_zero_division(self):
        idx = BM25Index()
        idx.add_document(0, "")
        idx.add_document(1, "！！！ 🎉")  # 没有任何词的 UTF-8 文档
        self.assertEqual(idx.doc_len[0], 0)
        self.assertEqual(idx.n_docs, 2)
        self.assertEqual(idx.avgdl, 0.0)
        self.assertEqual(idx.search("apple", 5), [])       # 不除零
        self.assertEqual(idx.search_full("apple", 5), [])

        idx.add_document(2, "apple")
        self.assertAlmostEqual(idx.avgdl, 1.0 / 3.0)
        self.assertEqual(idx.search("apple", 5)[0][0], 2)  # 空文档得分 0 且不进结果

        idx.delete_document(0)                              # 删除空文档后 avgdl 更新
        self.assertAlmostEqual(idx.avgdl, 0.5)


class TestTieBreakById(unittest.TestCase):
    def test_equal_scores_sorted_by_id(self):
        idx = BM25Index(block_size=2)
        for i in (5, 3, 0, 4, 1, 2):
            idx.add_document(i, "apple")  # 全部同文同分
        hits = idx.search("apple", 6)
        self.assertEqual([d for d, _ in hits], [0, 1, 2, 3, 4, 5])
        self.assertEqual(idx.search("apple", 1)[0][0], 0)
        idx.delete_document(0)
        self.assertEqual(idx.search("apple", 1)[0][0], 1)


class TestBlockPruning(unittest.TestCase):
    def test_best_doc_in_last_block_forced(self):
        # 强制构造：最优文档在最后一个块，中间块必须被剪掉
        idx = BM25Index(block_size=2)
        idx.add_document(0, "apple apple apple")   # 块 0
        idx.add_document(1, "apple")               # 块 0
        idx.add_document(2, "apple")               # 块 1（将被剪枝）
        idx.add_document(3, "apple")               # 块 1（将被剪枝）
        idx.add_document(4, "apple")               # 块 2
        idx.add_document(5, "zebra zebra zebra apple")  # 块 2，最优
        hits, ev = idx.search_debug("apple zebra", 1)
        self.assertEqual([d for d, _ in hits], [5])
        self.assertEqual(hits, idx.search_full("apple zebra", 1))
        self.assertEqual(ev["blocks_scored"], [0, 2])
        self.assertEqual([p["block"] for p in ev["blocks_pruned"]], [1])
        self.assertLess(ev["docs_scored"], ev["docs_candidate"])

    def test_prune_evidence_is_valid(self):
        # 证据核验：每个未读块都满足“不可能胜过阈值”
        idx = BM25Index(block_size=2)
        for i in range(12):
            idx.add_document(i, "apple" if i % 3 else "apple apple apple")
        _hits, ev = idx.search_debug("apple", 3)
        self.assertTrue(ev["blocks_pruned"])
        for p in ev["blocks_pruned"]:
            self.assertTrue(
                p["bound"] < p["threshold"]
                or (p["bound"] == p["threshold"]
                    and p["block_min_doc_id"] > p["threshold_doc_id"]),
                "被剪枝的块必须有严格证据: %r" % p)

    def test_block_bound_is_true_max(self):
        idx = BM25Index(block_size=2)
        idx.add_document(0, "apple apple")
        idx.add_document(1, "apple")
        bound = idx.block_bound("apple", 0)
        for d in (0, 1):
            self.assertLessEqual(idx.term_score("apple", d), bound)
        self.assertEqual(bound, max(idx.term_score("apple", 0),
                                    idx.term_score("apple", 1)))


class TestStaleBoundsInvalidated(unittest.TestCase):
    def test_high_freq_term_update_forced(self):
        # 强制构造：高频词 apple 的 df/avgdl 因增删改变，
        # 旧上界若被继续使用会错误剪掉含最优文档的块。
        idx = BM25Index(block_size=4)
        idx.add_document(0, "apple")
        idx.add_document(1, "apple")
        idx.add_document(4, "apple")   # 块 1
        self.assertEqual(idx.search("apple", 1)[0][0], 0)  # 同分按 ID

        stale = idx.block_bound("apple", 1)
        self.assertEqual(idx._block_bounds[("apple", 1)], (3, stale))

        idx.add_document(5, "apple apple apple apple apple apple")  # 高频更新
        self.assertEqual(idx.version, 4)
        # 缓存条目版本已落后 => 失效，不得直接使用
        self.assertNotEqual(idx._block_bounds[("apple", 1)][0], idx.version)

        fresh = idx.block_bound("apple", 1)
        self.assertNotEqual(fresh, stale)
        self.assertEqual(idx._block_bounds[("apple", 1)][0], idx.version)

        hits, ev = idx.search_debug("apple", 1)
        self.assertEqual([d for d, _ in hits], [5])
        self.assertEqual(hits, idx.search_full("apple", 1))
        self.assertIn(1, ev["blocks_scored"])  # 块 1 未被错误剪枝
        # 失效上界 < 新阈值 < 最优文档新得分：误用旧上界必然丢解
        new_threshold_holder = idx.score_document(["apple"], 0)
        self.assertLess(stale, new_threshold_holder)
        self.assertLess(new_threshold_holder, hits[0][1])

    def test_delete_invalidates_bounds(self):
        idx = BM25Index(block_size=2)
        idx.add_document(0, "apple apple apple")
        idx.add_document(1, "apple")
        before = idx.block_bound("apple", 0)
        idx.delete_document(0)
        self.assertNotEqual(idx._block_bounds[("apple", 0)][0], idx.version)
        after = idx.block_bound("apple", 0)
        # 删除改变了 avgdl，上界被重算（此处因 avgdl 变小而合法地变大）
        self.assertNotEqual(after, before)
        self.assertEqual(after, idx.term_score("apple", 1))


class TestSnapshot(unittest.TestCase):
    def _build(self):
        idx = BM25Index(block_size=2)
        idx.add_document(0, "apple banana")
        idx.add_document(1, "apple")
        idx.add_document(2, "banana banana")
        idx.add_document(3, "苹果 苹果 apple")
        return idx

    def test_snapshot_freezes_stats_and_results(self):
        idx = self._build()
        snap = idx.snapshot()
        self.assertIsInstance(snap, Snapshot)
        frozen = (snap.n_docs, snap.avgdl, dict(snap.df), snap.version)
        hits_before = snap.search("apple", 4)
        debug_before = snap.search_debug("苹果 apple", 2)

        idx.add_document(4, "apple apple apple")
        idx.delete_document(0)

        # 快照统计与结果不变
        self.assertEqual((snap.n_docs, snap.avgdl, dict(snap.df), snap.version), frozen)
        self.assertEqual(snap.search("apple", 4), hits_before)
        self.assertEqual(snap.search_debug("苹果 apple", 2), debug_before)
        # 快照剪枝结果与自身全量参考一致
        self.assertEqual(snap.search("apple", 3), snap.search_full("apple", 3))
        # 基索引已变
        self.assertEqual(idx.n_docs, 4)
        self.assertEqual(idx.version, snap.version + 2)
        self.assertNotEqual(idx.search("apple", 4), hits_before)

    def test_snapshot_tie_break_by_id(self):
        idx = BM25Index()
        for i in (2, 0, 1):
            idx.add_document(i, "apple")
        snap = idx.snapshot()
        self.assertEqual([d for d, _ in snap.search("apple", 3)], [0, 1, 2])


class TestEdgeCases(unittest.TestCase):
    def test_empty_query_and_k(self):
        idx = BM25Index()
        idx.add_document(0, "apple")
        self.assertEqual(idx.search("", 3), [])
        self.assertEqual(idx.search("！！！", 3), [])
        self.assertEqual(idx.search("apple", 0), [])
        self.assertEqual(idx.search("apple", -1), [])

    def test_k_larger_than_candidates(self):
        idx = BM25Index()
        idx.add_document(0, "apple")
        idx.add_document(1, "banana")
        hits = idx.search("apple", 10)
        self.assertEqual([d for d, _ in hits], [0])

    def test_unknown_term(self):
        idx = BM25Index()
        idx.add_document(0, "apple")
        self.assertEqual(idx.search("zebra", 3), [])


class TestRandomizedCrossCheck(unittest.TestCase):
    def test_pruned_matches_full_and_reference(self):
        rng = random.Random(42)
        vocab = ["apple", "banana", "cherry", "x1", "y2", "z3",
                 "苹", "果", "香", "蕉"]
        idx = BM25Index(block_size=3)
        docs = {}
        for i in range(40):
            toks = [rng.choice(vocab) for _ in range(rng.randrange(0, 13))]
            docs[i] = toks
            idx.add_document(i, " ".join(toks))

        queries = ["apple", "苹果", "apple banana", "x1 x1 y2", "蕉 蕉 蕉",
                   "zebra", "apple apple apple apple"]
        for _ in range(20):
            queries.append(" ".join(rng.choice(vocab)
                                    for _ in range(rng.randrange(1, 5))))

        def check_all():
            for q in queries:
                for k in (1, 2, 3, 7, 50):
                    pruned = idx.search(q, k)
                    full = idx.search_full(q, k)
                    ref = reference_rank(docs, q, k)
                    self.assertEqual(pruned, full)
                    self.assertEqual([d for d, _ in full], [d for d, _ in ref])
                    for (_, s1), (_, s2) in zip(full, ref):
                        self.assertAlmostEqual(s1, s2, places=12)

        check_all()
        # 高频词更新：大量增删改变 df 与 avgdl 后再次对照
        for i in range(0, 40, 2):
            idx.delete_document(i)
            del docs[i]
        check_all()
        for i in range(100, 120):
            toks = [rng.choice(vocab) for _ in range(rng.randrange(0, 13))]
            docs[i] = toks
            idx.add_document(i, " ".join(toks))
        check_all()


if __name__ == "__main__":
    unittest.main()
