"""bm25.py 的 unittest 测试套件（仅标准库）。

运行：python3.11 -m unittest test_bm25 -v
"""

import math
import random
import unittest

from bm25 import BM25Index, tokenize


class TestTokenize(unittest.TestCase):
    def test_rules(self):
        # casefold + 极大字母/数字序列；下划线/标点/空白是分隔符
        self.assertEqual(
            tokenize("Hello, WORLD! foo_bar 我爱 北京42"),
            ["hello", "world", "foo", "bar", "我爱", "北京42"],
        )
        # casefold 比 lower 更彻底：德语 ß -> ss
        self.assertEqual(tokenize("STRASSE Straße"), ["strasse", "strasse"])
        # 不做 CJK 切分：连续汉字是一个 token
        self.assertEqual(tokenize("我爱北京"), ["我爱北京"])
        # 空串 / 纯标点 -> 无 token
        self.assertEqual(tokenize(""), [])
        self.assertEqual(tokenize("，。！ _ "), [])


class TestStatsOnAddDelete(unittest.TestCase):
    def test_add_updates_stats(self):
        idx = BM25Index()
        self.assertEqual(idx.num_docs, 0)
        self.assertEqual(idx.avgdl, 0.0)
        idx.add_document(1, "apple banana apple")   # len 3
        idx.add_document(2, "banana")               # len 1
        idx.add_document(3, "")                     # 空文档 len 0
        self.assertEqual(idx.num_docs, 3)
        self.assertAlmostEqual(idx.avgdl, 4 / 3)
        self.assertEqual(idx.document_length(1), 3)
        self.assertEqual(idx.document_length(3), 0)
        self.assertEqual(idx.document_frequency("apple"), 1)
        self.assertEqual(idx.document_frequency("banana"), 2)
        self.assertEqual(idx.version, 3)

    def test_delete_updates_stats(self):
        idx = BM25Index()
        idx.add_document(1, "apple banana")
        idx.add_document(2, "apple apple")
        idx.add_document(3, "")
        v0 = idx.version
        idx.delete_document(2)                      # 删除后平均长度必须更新
        self.assertEqual(idx.num_docs, 2)
        self.assertAlmostEqual(idx.avgdl, 1.0)      # total 2 / N 2
        self.assertEqual(idx.document_frequency("apple"), 1)
        self.assertEqual(idx.document_frequency("banana"), 1)
        self.assertEqual(idx.version, v0 + 1)
        idx.delete_document(1)
        idx.delete_document(3)
        self.assertEqual(idx.num_docs, 0)
        self.assertEqual(idx.avgdl, 0.0)
        self.assertEqual(idx.document_frequency("apple"), 0)

    def test_delete_missing_and_duplicate_add(self):
        idx = BM25Index()
        idx.add_document(1, "x")
        with self.assertRaises(ValueError):
            idx.add_document(1, "y")
        with self.assertRaises(KeyError):
            idx.delete_document(99)


class TestHandComputedScores(unittest.TestCase):
    """手算复核：N=3, avgdl=4/3, k1=1.5, b=0.75。"""

    def setUp(self):
        self.idx = BM25Index(k1=1.5, b=0.75, block_size=2)
        self.idx.add_document(1, "apple banana")   # tf: apple=1, banana=1, |d|=2
        self.idx.add_document(2, "apple apple")    # tf: apple=2,          |d|=2
        self.idx.add_document(3, "")               # 空文档，             |d|=0
        self.snap = self.idx.snapshot()
        self.N = 3
        self.avg = 4 / 3
        self.k1, self.b = 1.5, 0.75

    def _idf(self, df):
        return math.log(1 + (self.N - df + 0.5) / (df + 0.5))

    def _contrib(self, term, df, tf, dl):
        norm = self.k1 * (1 - self.b + self.b * dl / self.avg)
        return self._idf(df) * (tf * (self.k1 + 1)) / (tf + norm)

    def test_single_term(self):
        # apple: df=2；d1 tf=1，d2 tf=2，|d1|=|d2|=2
        self.assertAlmostEqual(self.snap.score_document(1, ["apple"]),
                               self._contrib("apple", 2, 1, 2))
        self.assertAlmostEqual(self.snap.score_document(2, ["apple"]),
                               self._contrib("apple", 2, 2, 2))
        # banana: df=1，仅 d1
        self.assertAlmostEqual(self.snap.score_document(1, ["banana"]),
                               self._contrib("banana", 1, 1, 2))
        self.assertEqual(self.snap.score_document(2, ["banana"]), 0.0)
        # 空文档任何查询都得 0，且不除零
        self.assertEqual(self.snap.score_document(3, ["apple"]), 0.0)
        # 多词查询 = 各词贡献之和
        self.assertAlmostEqual(
            self.snap.score_document(1, ["apple", "banana"]),
            self._contrib("apple", 2, 1, 2) + self._contrib("banana", 1, 1, 2),
        )

    def test_repeated_query_terms_rule(self):
        # 明确规则：查询词出现 m 次即累加 m 次（多重集合）
        s1 = self.snap.score_document(1, ["apple"])
        s2 = self.snap.score_document(1, ["apple", "apple"])
        s3 = self.snap.score_document(1, "apple apple apple")  # 字符串同样规则
        self.assertAlmostEqual(s2, 2 * s1)
        self.assertAlmostEqual(s3, 3 * s1)
        # 与顺序无关
        self.assertAlmostEqual(
            self.snap.score_document(1, ["banana", "apple", "apple"]),
            self.snap.score_document(1, ["apple", "banana", "apple"]),
        )

    def test_all_empty_corpus_no_zero_division(self):
        idx = BM25Index()
        idx.add_document("a", "")
        idx.add_document("b", "")
        snap = idx.snapshot()
        self.assertEqual(snap.avgdl, 0.0)
        results, _ = snap.topk_pruned(["apple"], 5)
        self.assertEqual([s for _, s in results], [0.0, 0.0])
        self.assertEqual([d for d, _ in results], ["a", "b"])  # 同分按 ID 升序


class TestRankingAndTies(unittest.TestCase):
    def test_tie_break_by_id(self):
        idx = BM25Index(block_size=2)
        idx.add_document(5, "apple")
        idx.add_document(2, "apple")
        idx.add_document(9, "apple")
        snap = idx.snapshot()
        results, _ = snap.topk_pruned(["apple"], 3)
        self.assertEqual([d for d, _ in results], [2, 5, 9])  # 同分按 ID 升序
        self.assertEqual(results, snap.topk_full(["apple"], 3))

    def test_string_ids(self):
        idx = BM25Index(block_size=1)
        idx.add_document("doc-b", "x")
        idx.add_document("doc-a", "x")
        results, _ = idx.snapshot().topk_pruned(["x"], 2)
        self.assertEqual([d for d, _ in results], ["doc-a", "doc-b"])

    def test_k_edges(self):
        idx = BM25Index(block_size=2)
        for i in range(3):
            idx.add_document(i, "apple")
        snap = idx.snapshot()
        self.assertEqual(snap.topk_pruned(["apple"], 0), ([], []))
        self.assertEqual(len(snap.topk_full(["apple"], 10)), 3)  # k > N 返回全部
        empty = BM25Index().snapshot()
        self.assertEqual(empty.topk_pruned(["apple"], 5), ([], []))
        self.assertEqual(empty.topk_full(["apple"], 5), [])


class TestSnapshotAndVersioning(unittest.TestCase):
    def _build(self):
        idx = BM25Index(block_size=2)
        idx.add_document(1, "机器 学习 机器")
        idx.add_document(2, "学习 深度 学习")
        idx.add_document(3, "水果 苹果")
        idx.add_document(4, "机器 学习 深度")
        return idx

    def test_snapshot_freezes_stats_and_index(self):
        idx = self._build()
        snap1 = idx.snapshot()
        before = snap1.topk_full(["机器", "学习"], 4)
        n1, avg1, df1 = snap1.num_docs, snap1.avgdl, snap1.document_frequency("机器")
        # 快照后增删：旧快照必须完全不变
        idx.add_document(5, "机器 机器 机器 机器")
        idx.delete_document(3)
        self.assertEqual(snap1.topk_full(["机器", "学习"], 4), before)
        self.assertEqual((snap1.num_docs, snap1.avgdl, snap1.document_frequency("机器")),
                         (n1, avg1, df1))
        # 新快照反映新统计
        snap2 = idx.snapshot()
        self.assertEqual(snap2.num_docs, 4)
        self.assertEqual(snap2.document_frequency("机器"), 3)
        self.assertNotEqual(snap2.version, snap1.version)

    def test_no_stale_bounds_after_mutation(self):
        idx = self._build()
        snap1 = idx.snapshot()
        q = ["机器", "学习"]
        snap1.topk_pruned(q, 2)  # 填充上界缓存
        self.assertTrue(snap1._bounds_cache)
        self.assertTrue(all(key[0] == snap1.version for key in snap1._bounds_cache))
        ub1 = dict((key[2], val) for key, val in snap1._bounds_cache.items())

        # 增删改变全局统计（df、N、avgdl）=> version 改变
        idx.add_document(5, "机器 机器 机器")
        idx.delete_document(3)
        snap2 = idx.snapshot()
        self.assertNotEqual(snap2.version, snap1.version)
        snap2.topk_pruned(q, 2)
        # 新快照的缓存键全部属于新版本：旧上界不可能被复用
        self.assertTrue(snap2._bounds_cache)
        self.assertTrue(all(key[0] == snap2.version for key in snap2._bounds_cache))
        # 上界确实随统计变化而重算（块 0 两篇文档相同，但 idf/avgdl 变了）
        ub2 = dict((key[2], val) for key, val in snap2._bounds_cache.items())
        self.assertNotEqual(ub1[0], ub2[0])
        # 旧快照结果仍冻结、新快照结果与全量评分一致
        self.assertEqual(snap1.topk_pruned(q, 2)[0], snap1.topk_full(q, 2))
        self.assertEqual(snap2.topk_pruned(q, 2)[0], snap2.topk_full(q, 2))


class TestPruningCorrectness(unittest.TestCase):
    def test_best_doc_in_last_block(self):
        # 强制构造：最优文档（含稀有词 zebra）位于最后一块
        idx = BM25Index(k1=1.2, b=0.5, block_size=2)
        idx.add_document(1, "apple")
        idx.add_document(2, "apple banana")
        idx.add_document(3, "banana")
        idx.add_document(4, "apple")
        idx.add_document(5, "zebra zebra zebra zebra")  # 块 2（最后一块）独占
        snap = idx.snapshot()
        self.assertEqual(snap._blocks[-1], (5,))
        q = ["zebra", "apple"]
        results, audit = snap.topk_pruned(q, 1)
        self.assertEqual(results[0][0], 5)                       # 最优项在最后一块
        self.assertEqual(results, snap.topk_full(q, 1))          # 与全量一致
        skipped = [e for e in audit if e.decision == "skipped"]
        self.assertEqual(len(skipped), 2)                        # 前两块都被剪掉
        for entry in skipped:                                    # 证据：UB < 阈值
            self.assertLess(entry.upper_bound, entry.threshold[0])

    def test_high_frequency_term_update(self):
        # 高频词场景：大量新增含 common 的文档 => df 上升 => idf 下降 => 得分下降
        idx = BM25Index(block_size=2)
        idx.add_document(1, "common alpha")
        idx.add_document(2, "common beta")
        idx.add_document(3, "common gamma")
        snap1 = idx.snapshot()
        s1 = snap1.score_document(1, ["common"])
        idf1 = snap1.idf("common")
        for i in range(4, 14):  # 再塞 10 篇含 common 的文档
            idx.add_document(i, f"common filler{i}")
        snap2 = idx.snapshot()
        s2 = snap2.score_document(1, ["common"])
        self.assertEqual(snap2.document_frequency("common"), 13)
        self.assertLess(snap2.idf("common"), idf1)   # idf 下降
        self.assertLess(s2, s1)                      # 同一文档同一词得分下降
        # 删除 12 篇后 df 回落，idf 回升
        for i in range(2, 14):
            idx.delete_document(i)
        snap3 = idx.snapshot()
        self.assertEqual(snap3.document_frequency("common"), 1)
        self.assertGreater(snap3.idf("common"), snap2.idf("common"))

    def test_pruned_matches_full_randomized(self):
        rng = random.Random(20241001)
        vocab = ["alpha", "beta", "gamma", "delta", "机器", "学习",
                 "深度", "水果", "苹果", "x", "y", "z"]
        idx = BM25Index(k1=1.5, b=0.75, block_size=4)
        live = []
        for i in range(80):
            n = rng.choice([0, 0, 1, 2, 3, 5, 8, 13, 21])  # 含空文档
            idx.add_document(i, " ".join(rng.choice(vocab) for _ in range(n)))
            live.append(i)
        for victim in rng.sample(live, 20):                # 删除改变全局统计
            idx.delete_document(victim)
            live.remove(victim)
        for i in range(80, 95):                            # 再增，df 持续变化
            n = rng.choice([0, 1, 4, 9])
            idx.add_document(i, " ".join(rng.choice(vocab) for _ in range(n)))
        snap = idx.snapshot()
        self.assertEqual(snap.num_docs, 75)
        for _ in range(60):
            q = [rng.choice(vocab) for _ in range(rng.randint(1, 5))]  # 可能重复
            k = rng.randint(1, 80)
            pruned, audit = snap.topk_pruned(q, k)
            self.assertEqual(pruned, snap.topk_full(q, k))
            # 证据校验：每个被跳过的块都满足剪枝条件
            for entry in audit:
                if entry.decision == "skipped":
                    thr_score, thr_id = entry.threshold
                    self.assertTrue(
                        entry.upper_bound < thr_score
                        or (entry.upper_bound == thr_score
                            and entry.doc_ids[0] >= thr_id),
                        f"块 {entry.block_index} 的剪枝证据不成立",
                    )
            # 所有块都有审计记录
            self.assertEqual(len(audit), len(snap._blocks))

    def test_unknown_term_and_empty_query(self):
        idx = BM25Index(block_size=2)
        idx.add_document(1, "apple")
        idx.add_document(2, "banana")
        snap = idx.snapshot()
        # 未登录词：所有得分 0，同分按 ID
        results, _ = snap.topk_pruned(["nonexistent"], 2)
        self.assertEqual(results, [(1, 0.0), (2, 0.0)])
        # 空查询：得分全 0，与全量一致
        self.assertEqual(snap.topk_pruned([], 2)[0], snap.topk_full([], 2))


if __name__ == "__main__":
    unittest.main()
