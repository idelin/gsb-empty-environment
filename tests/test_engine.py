import math
import unittest
from collections import Counter

from helpers import QUERY, build_world, child_of
from ranker.engine import explain_miss
from ranker.tokenizer import tokenize


class TestEngine(unittest.TestCase):
    def setUp(self):
        self.docs, self.stats, self.rules, self.engine = build_world()
        self.expl = self.engine.search(**QUERY)

    def test_components_sum_equals_final_score(self):
        """解释分项合计必须严格等于最终分数。"""
        for child in self.expl["children"]:
            if child["type"] not in ("result", "scored_out"):
                continue
            total = 0.0
            for term_node in child["children"]:
                self.assertEqual(
                    term_node["contribution"],
                    term_node["weight"] * term_node["tf"] * term_node["idf"])
                total += term_node["contribution"]
            self.assertEqual(total, child["score"])

    def test_full_accounting_every_doc_exactly_once(self):
        accounted = sorted(c["doc_id"] for c in self.expl["children"])
        self.assertEqual(accounted, [f"m{i}" for i in range(1, 8)])

    def test_node_types_cover_all_cases(self):
        types = {c["doc_id"]: c["type"] for c in self.expl["children"]}
        self.assertEqual(types["m1"], "result")
        self.assertEqual(types["m4"], "pruned")
        self.assertEqual(types["m5"], "no_match")
        self.assertEqual(types["m6"], "filtered")
        self.assertEqual(types["m7"], "scored_out")

    def test_filter_reason_is_specific(self):
        node = child_of(self.expl, "m6")
        self.assertEqual(node["reason"], "字段 year=2010 不满足过滤条件 year >= 2020")
        self.assertEqual(node["filter"], {"field": "year", "op": ">=", "value": 2020})

    def test_pruned_upper_bound_is_reliable(self):
        """独立复算被剪枝文档的真实满分，上界必须不小于它。"""
        node = child_of(self.expl, "m4")
        stats = self.stats.get(self.expl["stats_version"])
        rev = self.docs.get("m4", node["doc_version"])
        tf = Counter(tokenize(rev["text"]))
        true_score = 0.0
        for entry in self.expl["query"]["terms"]:
            t = entry["term"]
            idf = math.log((stats["N"] + 1.0) / (stats["df"].get(t, 0) + 1.0)) + 1.0
            true_score += entry["weight"] * tf.get(t, 0) * idf
        self.assertGreaterEqual(node["upper_bound"], true_score - 1e-12)
        self.assertLess(node["upper_bound"], node["threshold"])
        self.assertEqual(self.expl["stats_version"], 1)

    def test_scored_out_records_threshold(self):
        node = child_of(self.expl, "m7")
        m3 = child_of(self.expl, "m3")
        self.assertEqual(node["threshold"], m3["score"])
        self.assertEqual(node["kth_doc_id"], "m3")

    def test_result_ranks_and_order(self):
        results = [c for c in self.expl["children"] if c["type"] == "result"]
        self.assertEqual([c["rank"] for c in results], [1, 2, 3])
        self.assertEqual([c["doc_id"] for c in results], ["m1", "m2", "m3"])

    def test_explain_miss_is_specific_not_generic(self):
        self.assertEqual(explain_miss(self.expl, "m6")["status"], "filtered")
        self.assertEqual(explain_miss(self.expl, "m5")["status"], "no_match")
        self.assertEqual(explain_miss(self.expl, "m5")["absent_terms"], ["alpha", "beta"])
        pruned = explain_miss(self.expl, "m4")
        self.assertEqual(pruned["status"], "pruned")
        self.assertIn("upper_bound", pruned)
        self.assertEqual(pruned["stats_version"], 1)
        self.assertEqual(explain_miss(self.expl, "m7")["status"], "scored_out")
        self.assertEqual(explain_miss(self.expl, "m1")["status"], "hit")
        self.assertEqual(explain_miss(self.expl, "m404")["status"], "unknown_doc")

    def test_stale_stats_rejected(self):
        self.docs.add("m8", "alpha", {"year": 2023})
        with self.assertRaises(RuntimeError):
            self.engine.search(**QUERY)


if __name__ == "__main__":
    unittest.main()
