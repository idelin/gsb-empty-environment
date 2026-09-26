import math
import unittest

from syno import PositionalIndex, QueryExpander, Searcher, SynonymStore


def build_searcher(docs, store, top_k=10, **kw):
    idx = PositionalIndex()
    for did, text in docs:
        idx.add_document(did, text)
    return Searcher(idx, QueryExpander(store, **kw), top_k=top_k)


class SearchTest(unittest.TestCase):
    def setUp(self):
        self.store = SynonymStore()
        self.store.add_word_group(["car", "automobile", "vehicle"], 0.7)
        self.store.add_word_pair("fast", "quick", 0.6)
        self.store.add_phrase_group(["kicked the bucket", "died"], 0.8)

    def test_original_ranks_above_expansion(self):
        docs = [
            ("orig", "a fast car"),
            ("syn", "a quick automobile"),
            ("noise", "coffee is good"),
        ]
        se = build_searcher(docs, self.store)
        res = se.search("fast car")
        self.assertEqual([d.doc_id for d in res.results], ["orig", "syn"])
        self.assertNotIn("noise", [d.doc_id for d in res.results])

    def test_evidence_tags_distinguish_original_and_expansion(self):
        se = build_searcher([("d", "a quick car")], self.store)
        res = se.search("fast car")
        tags = {(e.concept_label, e.surface, e.source_tag())
                for e in res.results[0].evidences}
        self.assertIn(("fast", "quick", "扩展词"), tags)
        self.assertIn(("car", "car", "原词"), tags)

    def test_original_and_expansion_same_position_not_double_counted(self):
        # 构造不可能严格同位置（同一位置文本唯一）；用同概念多命中验证饱和有上界。
        docs = [
            ("one", "car"),
            ("many", "car car car car car car car car"),
        ]
        se = build_searcher(docs, self.store)
        res = se.search("car")
        scores = {d.doc_id: d.score for d in res.results}
        # 8 次原词命中相对 1 次的得分比必须有上界（饱和），远小于 8 倍
        self.assertLess(scores["many"] / scores["one"], 2.0)
        # 且每个 car 位置只产生一条证据（不会因扩展词重复计分）
        many = next(d for d in res.results if d.doc_id == "many")
        self.assertEqual(len(many.evidences), 8)

    def test_shared_expansion_scored_once(self):
        st = SynonymStore()
        # 查询两根 a/c 经不同权重到达共享 s；文档只有一个 s
        st.add_word_pair("a", "s", 0.8)
        st.add_word_pair("c", "s", 0.6)
        se = build_searcher([("d", "s")], st, max_word_hops=1)
        res = se.search("a c")
        dr = res.results[0]
        # 单个 "s" 位置只能归属一个概念（全局位置占用）
        self.assertEqual(len(dr.evidences), 1)
        # 另一个概念未占位置则不产生得分；总证据数 1，无重复计分
        concepts_hit = {e.concept for e in dr.evidences}
        self.assertEqual(len(concepts_hit), 1)

    def test_phrase_is_not_split(self):
        docs = [
            ("phrase_syn", "he died young"),
            ("words_only", "kicked the empty bucket today"),
        ]
        se = build_searcher(docs, self.store)
        res = se.search("kicked the bucket")
        ids = {d.doc_id for d in res.results}
        # died 作为扩展短语命中；散列词不构成短语概念命中
        self.assertIn("phrase_syn", ids)
        syn = next(d for d in res.results if d.doc_id == "phrase_syn")
        self.assertTrue(any(e.kind == "phrase" for e in syn.evidences))
        ev = next(e for e in syn.evidences if e.kind == "phrase")
        self.assertEqual((ev.surface, ev.length, ev.is_original), ("died", 1, False))

    def test_phrase_preserves_path_length(self):
        se = build_searcher([("d", "he kicked the bucket")], self.store)
        res = se.search("kicked the bucket")
        ev = res.results[0].evidences[0]
        self.assertEqual(ev.length, 3)
        self.assertTrue(ev.is_original)

    def test_noncontiguous_phrase_does_not_match(self):
        se = build_searcher([("d", "the bucket was kicked hard")], self.store)
        res = se.search("kicked the bucket")
        # 仅可能有原词单词命中，绝不能出现短语命中
        self.assertFalse(any(e.kind == "phrase" for d in res.results for e in d.evidences))

    def test_truncated_result_marked_incomplete(self):
        docs = [("d", "a quick car")]
        idx = PositionalIndex()
        idx.add_document("d", "a quick car")
        exp = QueryExpander(self.store, max_word_hops=1, node_budget=2, edge_budget=8)
        res = Searcher(idx, exp).search("fast car")
        self.assertTrue(res.truncated)
        self.assertEqual(res.truncation, "node_budget")

    def test_top_k_limit_and_deterministic_order(self):
        docs = [(f"d{i}", "car") for i in range(7)]
        se = build_searcher(docs, self.store, top_k=3)
        res = se.search("car")
        self.assertEqual(len(res.results), 3)
        # 同分时按 doc_id 确定排序
        self.assertEqual([d.doc_id for d in res.results], ["d0", "d1", "d2"])

    def test_cjk_phrase_search(self):
        st = SynonymStore()
        st.add_phrase_group(["机器学习", "人工智能"], 0.75)
        se = build_searcher([
            ("a", "机器学习入门"),
            ("b", "人工智能应用"),
        ], st)
        res = se.search("机器学习")
        ids = [d.doc_id for d in res.results]
        self.assertEqual(ids, ["a", "b"])
        b = next(d for d in res.results if d.doc_id == "b")
        ev = next(e for e in b.evidences if e.kind == "phrase")
        self.assertEqual((ev.surface, ev.is_original, ev.length),
                         ("人工智能", False, 4))

    def test_score_has_idf_structure(self):
        # 仅一个文档含 car 时，car 概念得分 = idf*饱和项 + 原词bonus，且为正
        se = build_searcher([("d", "car")], self.store)
        res = se.search("car")
        n = 1
        idf = math.log(1.0 + (n + 1.0) / 2.0)
        expected = idf * (1 / (1.2 + 1)) + 0.5
        self.assertAlmostEqual(res.results[0].score, expected, places=6)


if __name__ == "__main__":
    unittest.main()
