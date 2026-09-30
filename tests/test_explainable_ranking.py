import copy
import json
import math
import unittest

from explainable_ranking import (
    Corpus,
    CorpusStore,
    Document,
    Filter,
    RuleSet,
    RuleStore,
    StatsSnapshot,
    StatsStore,
    explain_miss,
    rank,
    tokenize,
    verify,
)

QUERY = "检索 引擎 ranking"


def build_world():
    docs = [
        Document("d1", "检索引擎入门 ranking ranking search engine 教程", "v1"),
        Document("d2", "搜索引擎检索 ranking ranking ranking 实战", "v1"),
        Document("d3", "spam 检索引擎 ranking", "v1"),
        Document("d4", "短文", "v1"),
        Document("d5", "阳光明媚心情愉悦", "v1"),
        Document("d6", "检索引擎深入原理分析 ranking ranking 系统设计实现", "v1"),
        Document("d7", "检索系统优化笔记 ranking ranking 引擎实践", "v1"),
        Document("d8", "检索一下而已随便看看东西", "v1"),
    ]
    corpus = Corpus()
    for doc in docs:
        corpus.put(doc)
    corpus_store = CorpusStore()
    corpus_store.put("corpus-v1", corpus)
    stats_store = StatsStore()
    stats_store.put(StatsSnapshot.build(
        "stats-v1", [tokenize(corpus.get(d).text) for d in corpus.ids()]))
    rule_store = RuleStore()
    rule_store.put(RuleSet(
        version="rules-v1",
        weights={"检": 1.0, "索": 1.0, "引": 1.0, "擎": 1.0, "ranking": 1.5},
        filters=(Filter("min_tokens", 3), Filter("forbidden_term", "spam")),
        top_k=3,
        score_threshold=8.0,
    ))
    tree = rank(QUERY, corpus, "corpus-v1",
                rule_store.get("rules-v1"), stats_store.get("stats-v1"))
    return corpus_store, rule_store, stats_store, tree


def child(tree, doc_id):
    for node in tree["children"]:
        if node["doc_id"] == doc_id:
            return node
    raise AssertionError(f"no node for {doc_id}")


class TokenizerTest(unittest.TestCase):
    def test_ascii_runs_and_cjk_chars(self):
        self.assertEqual(tokenize("Hello, 世界! Foo42"),
                         ["hello", "世", "界", "foo42"])

    def test_casefold_and_nfkc(self):
        self.assertEqual(tokenize("ＲＡＮＫ"), ["rank"])
        self.assertEqual(tokenize("Straße"), ["strasse"])

    def test_separators_produce_no_tokens(self):
        self.assertEqual(tokenize("  \t\n。。。"), [])


class ExplanationTreeTest(unittest.TestCase):
    def setUp(self):
        stores = build_world()
        self.corpus_store, self.rule_store, self.stats_store, self.tree = stores

    def test_every_document_accounted_exactly_once(self):
        self.assertEqual(
            sorted(n["doc_id"] for n in self.tree["children"]),
            ["d1", "d2", "d3", "d4", "d5", "d6", "d7", "d8"])

    def test_score_equals_component_sum(self):
        for node in self.tree["children"]:
            if "components" in node:
                total = math.fsum(c["contribution"] for c in node["components"])
                self.assertTrue(node["score_equals_component_sum"])
                self.assertAlmostEqual(node["score"], total, places=12)
                self.assertAlmostEqual(node["sum_components"], total, places=12)

    def test_upper_bound_dominates_score(self):
        for node in self.tree["children"]:
            if "upper_bound" in node:
                self.assertGreaterEqual(node["upper_bound"] + 1e-12,
                                        node["score"])

    def test_pruned_nodes_carry_bound_and_stats_version(self):
        pruned = [n for n in self.tree["children"] if n["type"] == "pruned"]
        self.assertTrue(pruned)
        for node in pruned:
            self.assertIn("upper_bound", node)
            self.assertEqual(node["stats_version"], "stats-v1")
            self.assertIn(node["reason"],
                          ("upper_bound_below_threshold",
                           "score_below_threshold", "beyond_top_k"))

    def test_hit_components_record_term_weight_idf_tf(self):
        hit = child(self.tree, "d1")
        self.assertEqual(hit["type"], "hit")
        terms = {c["term"] for c in hit["components"]}
        self.assertEqual(terms, {"检", "索", "引", "擎", "ranking"})
        for comp in hit["components"]:
            self.assertIn("weight", comp)
            self.assertIn("idf", comp)
            self.assertIn("tf", comp)


class MissExplanationTest(unittest.TestCase):
    def setUp(self):
        self.tree = build_world()[3]

    def test_filtered_forbidden_term(self):
        miss = explain_miss(self.tree, "d3")
        self.assertTrue(miss["missed"])
        self.assertEqual(miss["category"], "filtered")
        self.assertEqual(miss["filter"]["kind"], "forbidden_term")
        self.assertIn("spam", miss["reason"])

    def test_filtered_min_tokens(self):
        miss = explain_miss(self.tree, "d4")
        self.assertEqual(miss["category"], "filtered")
        self.assertEqual(miss["filter"]["kind"], "min_tokens")
        self.assertIn("token_count=2", miss["reason"])

    def test_pruned_by_upper_bound(self):
        miss = explain_miss(self.tree, "d5")
        self.assertEqual(miss["category"], "pruned")
        self.assertEqual(miss["reason"], "upper_bound_below_threshold")
        self.assertLess(miss["upper_bound"], 8.0)
        self.assertEqual(miss["stats_version"], "stats-v1")

    def test_pruned_by_score(self):
        miss = explain_miss(self.tree, "d8")
        self.assertEqual(miss["category"], "pruned")
        self.assertEqual(miss["reason"], "score_below_threshold")

    def test_pruned_beyond_top_k(self):
        miss = explain_miss(self.tree, "d7")
        self.assertEqual(miss["category"], "pruned")
        self.assertEqual(miss["reason"], "beyond_top_k")
        self.assertIn("top_k=3", miss["detail"])

    def test_not_in_corpus(self):
        miss = explain_miss(self.tree, "d99")
        self.assertTrue(miss["missed"])
        self.assertEqual(miss["category"], "not_in_corpus")
        self.assertIn("corpus-v1", miss["reason"])

    def test_hit_is_not_a_miss(self):
        miss = explain_miss(self.tree, "d1")
        self.assertFalse(miss["missed"])
        self.assertEqual(miss["rank"], 2)


class VerificationTest(unittest.TestCase):
    def setUp(self):
        stores = build_world()
        self.corpus_store, self.rule_store, self.stats_store, self.tree = stores

    def verdict(self, tree, corpus_store=None, rule_store=None, stats_store=None):
        return verify(tree,
                      corpus_store or self.corpus_store,
                      rule_store or self.rule_store,
                      stats_store or self.stats_store)

    def test_clean_tree_verifies(self):
        self.assertEqual(self.verdict(self.tree).status, "verified")

    def test_json_roundtrip_still_verifies(self):
        clone = json.loads(json.dumps(self.tree, ensure_ascii=False))
        self.assertEqual(self.verdict(clone).status, "verified")

    def test_tampered_weight_locates_first_failing_node(self):
        tampered = copy.deepcopy(self.tree)
        hit = child(tampered, "d1")
        hit["components"][0]["weight"] += 0.5
        verdict = self.verdict(tampered)
        self.assertEqual(verdict.status, "failed")
        self.assertEqual(verdict.node_id, hit["node_id"])
        self.assertIn("weight", verdict.field)

    def test_tampered_idf_detected(self):
        tampered = copy.deepcopy(self.tree)
        hit = child(tampered, "d2")
        hit["components"][0]["idf"] *= 1.1
        verdict = self.verdict(tampered)
        self.assertEqual(verdict.status, "failed")
        self.assertEqual(verdict.node_id, hit["node_id"])

    def test_omitted_filtered_node_detected(self):
        tampered = copy.deepcopy(self.tree)
        tampered["children"] = [n for n in tampered["children"]
                                if n["doc_id"] != "d3"]
        verdict = self.verdict(tampered)
        self.assertEqual(verdict.status, "failed")
        self.assertEqual(verdict.node_id, "root")
        self.assertIn("children", verdict.field)

    def test_replaced_document_version_detected(self):
        evil_store = copy.deepcopy(self.corpus_store)
        evil_store.get("corpus-v1").put(
            Document("d1", "被篡改的文档内容 completely replaced", "v1"))
        verdict = self.verdict(self.tree, corpus_store=evil_store)
        self.assertEqual(verdict.status, "failed")
        self.assertEqual(verdict.node_id, child(self.tree, "d1")["node_id"])
        self.assertEqual(verdict.field, "doc_hash")

    def test_forged_hit_label_not_trusted(self):
        tampered = copy.deepcopy(self.tree)
        pruned = child(tampered, "d7")
        self.assertEqual(pruned["reason"], "beyond_top_k")
        pruned["type"] = "hit"
        pruned["rank"] = 1
        verdict = self.verdict(tampered)
        self.assertEqual(verdict.status, "failed")
        self.assertEqual(verdict.node_id, pruned["node_id"])

    def test_first_failing_node_is_the_earliest(self):
        tampered = copy.deepcopy(self.tree)
        child(tampered, "d2")["score"] += 1.0
        child(tampered, "d1")["score"] += 1.0
        verdict = self.verdict(tampered)
        self.assertEqual(verdict.status, "failed")
        self.assertEqual(verdict.node_id, child(self.tree, "d1")["node_id"])


class VersioningTest(unittest.TestCase):
    def setUp(self):
        stores = build_world()
        self.corpus_store, self.rule_store, self.stats_store, self.tree = stores

    def publish_v2(self):
        corpus_v1 = self.corpus_store.get("corpus-v1")
        corpus_v2 = Corpus()
        for doc_id in corpus_v1.ids():
            corpus_v2.put(corpus_v1.get(doc_id))
        corpus_v2.put(Document("d9", "新的文档 ranking 检索引擎技术展望", "v1"))
        self.corpus_store.put("corpus-v2", corpus_v2)
        self.stats_store.put(StatsSnapshot.build(
            "stats-v2",
            [tokenize(corpus_v2.get(d).text) for d in corpus_v2.ids()]))
        self.rule_store.put(RuleSet(
            version="rules-v2",
            weights={"检": 1.0, "索": 1.0, "引": 1.0, "擎": 1.0,
                     "ranking": 2.0},
            filters=(Filter("min_tokens", 3), Filter("forbidden_term", "spam")),
            top_k=5,
            score_threshold=8.0,
        ))
        return corpus_v2

    def test_old_explanation_stays_valid_after_updates(self):
        self.publish_v2()
        verdict = verify(self.tree, self.corpus_store,
                         self.rule_store, self.stats_store)
        self.assertEqual(verdict.status, "verified")
        self.assertEqual(self.tree["stats_version"], "stats-v1")
        self.assertEqual(self.tree["rules_version"], "rules-v1")

    def test_new_run_binds_new_versions(self):
        corpus_v2 = self.publish_v2()
        tree_v2 = rank(QUERY, corpus_v2, "corpus-v2",
                       self.rule_store.get("rules-v2"),
                       self.stats_store.get("stats-v2"))
        self.assertEqual(tree_v2["stats_version"], "stats-v2")
        verdict = verify(tree_v2, self.corpus_store,
                         self.rule_store, self.stats_store)
        self.assertEqual(verdict.status, "verified")
        self.assertNotEqual(
            child(self.tree, "d1")["score"], child(tree_v2, "d1")["score"])

    def test_missing_statistics_is_unverifiable_not_failed(self):
        self.stats_store.drop("stats-v1")
        verdict = verify(self.tree, self.corpus_store,
                         self.rule_store, self.stats_store)
        self.assertEqual(verdict.status, "unverifiable")
        self.assertIn("stats-v1", verdict.reason)

    def test_missing_rules_is_unverifiable(self):
        empty_rules = RuleStore()
        verdict = verify(self.tree, self.corpus_store,
                         empty_rules, self.stats_store)
        self.assertEqual(verdict.status, "unverifiable")

    def test_missing_corpus_snapshot_is_unverifiable(self):
        verdict = verify(self.tree, CorpusStore(),
                         self.rule_store, self.stats_store)
        self.assertEqual(verdict.status, "unverifiable")


if __name__ == "__main__":
    unittest.main()
