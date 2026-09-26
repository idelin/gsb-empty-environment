"""qexpand 的 unittest 测试：覆盖题目要求的各项行为。"""

import unittest

from qexpand import (
    DEFAULT_EXPANSION_WEIGHT,
    Edge,
    PositionalIndex,
    SynonymGroups,
    WordGraph,
    build_query_graph,
    enumerate_paths,
    search,
    tokenize,
)

W = DEFAULT_EXPANSION_WEIGHT  # 0.8


def make_groups():
    g = SynonymGroups()
    g.add_word_group(["car", "automobile"])
    g.add_word_group(["bike", "bicycle"])
    g.add_phrase_group(["new york", "ny"])
    g.add_phrase_group(["machine learning", "ml"])
    g.add_phrase_group(["深度学习", "dl"])
    return g


def make_index():
    idx = PositionalIndex()
    idx.add_document("d1", "New York is a city that never sleeps. I love New York.")
    idx.add_document("d2", "NY subway runs all night.")
    idx.add_document("d3", "I bought a car last week. The car is red.")
    idx.add_document("d4", "My automobile is fast.")
    idx.add_document("d5", "Machine learning powers modern search. ML is everywhere.")
    idx.add_document("d6", "深度学习是机器学习的分支，深度学习很有用。")
    idx.add_document("d7", "A bike or a bicycle is fine for short trips.")
    idx.add_document("d8", "York is historic, quite unlike anything new.")
    idx.add_document("d9", "new things and old york tales")  # 拆散，不应命中
    return idx


class TestTokenize(unittest.TestCase):
    def test_ascii_and_casefold(self):
        self.assertEqual(tokenize("Hello, World!"), ["hello", "world"])
        self.assertEqual(tokenize("foo_bar v2 42"), ["foo_bar", "v2", "42"])

    def test_cjk_char_by_char(self):
        self.assertEqual(tokenize("机器学习"),
                         ["机", "器", "学", "习"])
        self.assertEqual(tokenize("深度学习是ML"),
                         ["深", "度", "学", "习", "是", "ml"])

    def test_mixed_punctuation(self):
        self.assertEqual(tokenize("  New-York...NY! "), ["new", "york", "ny"])


class TestSynonymGroupsSeparation(unittest.TestCase):
    def test_word_group_rejects_multiword(self):
        g = SynonymGroups()
        with self.assertRaises(ValueError):
            g.add_word_group(["new york", "ny"])

    def test_phrase_group_requires_multiword_member(self):
        g = SynonymGroups()
        with self.assertRaises(ValueError):
            g.add_phrase_group(["car", "automobile"])

    def test_word_synonyms_only_from_word_groups(self):
        g = make_groups()
        # "ny" 只在短语组里，不应产生单词同义
        self.assertEqual(g.word_synonyms("ny"), [])
        self.assertEqual(g.word_synonyms("car"), [("automobile", 0)])


class TestCycleAndOneHop(unittest.TestCase):
    def test_cycle_expands_only_once(self):
        # a<->b 对称同义：查询 a 只多出 b，且 b 不再被同义组传递
        g = SynonymGroups()
        g.add_word_group(["a", "b"])
        g.add_word_group(["b", "c"])  # 若传递扩展会引入 c
        graph = build_query_graph(["a"], g)
        paths, truncated = enumerate_paths(graph)
        self.assertFalse(truncated)
        self.assertEqual(sorted(p.terms for p in paths), [("a",), ("b",)])

    def test_expansion_never_reexpanded(self):
        # 查询 b 本身可以扩展到 a 和 c，但 a/c 不再继续扩展
        g = SynonymGroups()
        g.add_word_group(["a", "b"])
        g.add_word_group(["b", "c"])
        g.add_word_group(["c", "d"])
        graph = build_query_graph(["b"], g)
        paths, _ = enumerate_paths(graph)
        self.assertEqual(sorted(p.terms for p in paths),
                         [("a",), ("b",), ("c",)])

    def test_no_exponential_blowup(self):
        # 每组 5 个同义词、查询 3 个词：一跳扩展 => (1+4)^3 = 125 条上界，
        # 而不是沿同义图无限传播；预算 64 应截断且标记不完整
        g = SynonymGroups()
        for ch in "abc":
            g.add_word_group([f"{ch}{i}" for i in range(5)])
        graph = build_query_graph(["a0", "b0", "c0"], g)
        paths, truncated = enumerate_paths(graph, max_paths=64)
        self.assertTrue(truncated)
        self.assertEqual(len(paths), 64)
        paths_all, truncated_all = enumerate_paths(graph, max_paths=1000)
        self.assertFalse(truncated_all)
        self.assertEqual(len(paths_all), 125)


class TestWordGraphAndPaths(unittest.TestCase):
    def test_small_graph_path_enumeration(self):
        # 手工核验小词图：0 -x-> 1 -z-> 2，0 -y-> 1
        graph = WordGraph(2)
        graph.add_edge(Edge(0, 1, "x", 1.0, True, None))
        graph.add_edge(Edge(0, 1, "y", W, False, ("w", 0)))
        graph.add_edge(Edge(1, 2, "z", 1.0, True, None))
        paths, truncated = enumerate_paths(graph)
        self.assertFalse(truncated)
        self.assertEqual([p.terms for p in paths], [("x", "z"), ("y", "z")])
        self.assertAlmostEqual(paths[0].weight, 1.0)
        self.assertAlmostEqual(paths[1].weight, W)
        self.assertTrue(paths[0].is_original)
        self.assertFalse(paths[1].is_original)
        self.assertEqual(paths[0].length, 2)
        self.assertEqual(paths[0].original_span, (0, 2))

    def test_phrase_edge_keeps_original_positions(self):
        g = make_groups()
        graph = build_query_graph(["new", "york", "city"], g)
        paths, _ = enumerate_paths(graph)
        by_terms = {p.terms: p for p in paths}
        self.assertIn(("new", "york", "city"), by_terms)
        self.assertIn(("ny", "city"), by_terms)
        p = by_terms[("ny", "city")]
        # 路径长度变短，但覆盖的原词位置仍是 [0, 3)
        self.assertEqual(p.length, 2)
        self.assertEqual(p.original_span, (0, 3))
        self.assertAlmostEqual(p.weight, W)
        self.assertEqual(p.group_keys, (("p", 0),))

    def test_multiword_alternative_chain(self):
        # 多词 -> 多词：边链中间节点不影响原词位置
        g = SynonymGroups()
        g.add_phrase_group(["new york", "the big apple"])
        graph = build_query_graph(["new", "york"], g)
        paths, _ = enumerate_paths(graph)
        by_terms = {p.terms: p for p in paths}
        p = by_terms[("the", "big", "apple")]
        self.assertEqual(p.original_span, (0, 2))
        self.assertAlmostEqual(p.weight, W)  # 边链只首边带扩展权重

    def test_duplicate_group_dedup(self):
        # 同一短语组登记两次：共享扩展不重复进图、不重复计分
        g1 = SynonymGroups()
        g1.add_phrase_group(["new york", "ny"])
        g2 = SynonymGroups()
        g2.add_phrase_group(["new york", "ny"])
        g2.add_phrase_group(["new york", "ny"])
        p1, _ = enumerate_paths(build_query_graph(["new", "york"], g1))
        p2, _ = enumerate_paths(build_query_graph(["new", "york"], g2))
        self.assertEqual(len(p1), len(p2))


class TestPhraseIntegrity(unittest.TestCase):
    def setUp(self):
        self.groups = make_groups()
        self.index = make_index()

    def test_phrase_not_split(self):
        res = search(self.index, "new york", self.groups)
        hit_docs = {doc for _, doc in res.hits}
        self.assertIn("d1", hit_docs)   # 原词连续命中
        self.assertIn("d2", hit_docs)   # 扩展 "ny" 命中
        self.assertNotIn("d8", hit_docs)  # york/new 不连续
        self.assertNotIn("d9", hit_docs)  # new ... york 被拆散

    def test_word_expansion_not_applied_to_phrase_member(self):
        # "ml" 是短语组等价物；查询 "ml" 自身是单词，无单词同义 => 只命中 d5 原文
        res = search(self.index, "ml", self.groups)
        self.assertEqual([doc for _, doc in res.hits], ["d5"])

    def test_chinese_phrase(self):
        res = search(self.index, "深度学习", self.groups)
        hit_docs = {doc for _, doc in res.hits}
        self.assertIn("d6", hit_docs)


class TestScoring(unittest.TestCase):
    def setUp(self):
        self.groups = make_groups()
        self.index = make_index()

    def test_original_outranks_expansion(self):
        res = search(self.index, "car", self.groups)
        scores = dict((doc, s) for s, doc in res.hits)
        self.assertAlmostEqual(scores["d3"], 1.0)   # 原词命中
        self.assertAlmostEqual(scores["d4"], W)     # 仅扩展命中
        self.assertEqual(res.hits[0][1], "d3")

    def test_both_hit_bounded_no_unbounded_stacking(self):
        idx = PositionalIndex()
        idx.add_document("both", "car automobile car automobile")
        idx.add_document("orig", "car")
        res = search(idx, "car", self.groups)
        scores = dict((doc, s) for s, doc in res.hits)
        # 原词+扩展同时命中：概率和合并，总分有界 <= 1
        self.assertLessEqual(scores["both"], 1.0)
        self.assertAlmostEqual(scores["orig"], 1.0)

    def test_repeated_expansion_hits_bounded(self):
        idx = PositionalIndex()
        idx.add_document("x", "automobile automobile automobile")
        res = search(idx, "car", self.groups)
        score = res.hits[0][0]
        # 3 次扩展命中：1-(1-0.8)^3 = 0.992，有界而不是 3*0.8=2.4
        self.assertAlmostEqual(score, 1 - 0.2 ** 3)
        self.assertLess(score, 1.0)

    def test_shared_expansion_scored_once(self):
        # 两个单词组都把查询词扩到同一个词 z：同区间只计一次
        g = SynonymGroups()
        g.add_word_group(["a", "z"])
        g.add_word_group(["b", "z"])
        idx = PositionalIndex()
        idx.add_document("doc", "z z")
        res = search(idx, "a b", g)
        # 路径 (z, z) 命中区间 (0,2) 一次，权重 0.8*0.8
        self.assertAlmostEqual(res.hits[0][0], W * W)

    def test_topk_limit(self):
        res = search(self.index, "car", self.groups, k=1)
        self.assertEqual(len(res.hits), 1)
        self.assertEqual(res.hits[0][1], "d3")


class TestBudgetTruncation(unittest.TestCase):
    def test_truncation_marks_incomplete(self):
        g = SynonymGroups()
        g.add_word_group(["a1", "a2", "a3"])
        g.add_word_group(["b1", "b2", "b3"])
        idx = PositionalIndex()
        idx.add_document("d", "a1 b1")
        res = search(idx, "a1 b1", g, max_paths=2)
        self.assertTrue(res.truncated)
        self.assertFalse(res.complete)
        self.assertEqual(len(res.paths), 2)

    def test_no_truncation_when_budget_sufficient(self):
        g = SynonymGroups()
        g.add_word_group(["a1", "a2"])
        idx = PositionalIndex()
        idx.add_document("d", "a1")
        res = search(idx, "a1", g, max_paths=16)
        self.assertFalse(res.truncated)
        self.assertTrue(res.complete)


class TestEvidence(unittest.TestCase):
    def test_evidence_distinguishes_origin(self):
        groups = make_groups()
        index = make_index()
        res = search(index, "new york", groups)
        d1_matches = res.evidence["d1"]
        self.assertTrue(all(m.is_original for m in d1_matches))
        d2_matches = res.evidence["d2"]
        self.assertTrue(any(not m.is_original for m in d2_matches))
        exp = [m for m in d2_matches if not m.is_original][0]
        self.assertEqual(exp.terms, ("ny",))
        self.assertEqual(exp.group_keys, (("p", 0),))
        self.assertAlmostEqual(exp.weight, W)

    def test_empty_query(self):
        res = search(make_index(), "  ", make_groups())
        self.assertEqual(res.hits, [])
        self.assertFalse(res.truncated)


if __name__ == "__main__":
    unittest.main()
