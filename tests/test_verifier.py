import copy
import unittest

from helpers import QUERY, build_world, child_of
from ranker import Verifier
from ranker.explanation import finalize


class TestVerifier(unittest.TestCase):
    def setUp(self):
        self.docs, self.stats, self.rules, self.engine = build_world()
        self.verifier = Verifier(self.docs, self.stats, self.rules)
        self.expl = self.engine.search(**QUERY)

    def tampered_copy(self, mutate):
        expl = copy.deepcopy(self.expl)
        mutate(expl)
        finalize(expl)  # 攻击者重算哈希掩盖痕迹
        return expl

    def test_valid_explanation_passes(self):
        self.assertEqual(self.verifier.verify(self.expl)["verdict"], "valid")

    # ---- 篡改注入：定位首个不成立节点 ----

    def test_tampered_weight_locates_term_node(self):
        def mutate(expl):
            node = child_of(expl, "m1")["children"][0]
            node["weight"] = 99.0
            node["contribution"] = 99.0 * node["tf"] * node["idf"]
        verdict = self.verifier.verify(self.tampered_copy(mutate))
        self.assertEqual(verdict["verdict"], "invalid")
        self.assertEqual(verdict["first_failure"]["path"], "root/result#1(m1)/term(alpha)")
        self.assertIn("权重", verdict["first_failure"]["reason"])

    def test_tampered_weight_without_rehash_caught_at_root(self):
        expl = copy.deepcopy(self.expl)
        child_of(expl, "m1")["children"][0]["weight"] = 99.0  # 不重算哈希
        verdict = self.verifier.verify(expl)
        self.assertEqual(verdict["verdict"], "invalid")
        self.assertEqual(verdict["first_failure"]["path"], "root")
        self.assertIn("哈希", verdict["first_failure"]["reason"])

    def test_tampered_contribution_breaks_sum(self):
        def mutate(expl):
            child_of(expl, "m2")["children"][0]["contribution"] += 1.0
        verdict = self.verifier.verify(self.tampered_copy(mutate))
        self.assertEqual(verdict["verdict"], "invalid")
        self.assertIn("term(alpha)", verdict["first_failure"]["path"])

    def test_omitted_filtered_node_breaks_accounting(self):
        def mutate(expl):
            expl["children"] = [c for c in expl["children"]
                                if not (c["type"] == "filtered" and c["doc_id"] == "m6")]
        verdict = self.verifier.verify(self.tampered_copy(mutate))
        self.assertEqual(verdict["verdict"], "invalid")
        self.assertEqual(verdict["first_failure"]["path"], "root")
        self.assertIn("m6", verdict["first_failure"]["reason"])

    def test_omitted_pruned_node_breaks_accounting(self):
        def mutate(expl):
            expl["children"] = [c for c in expl["children"] if c["doc_id"] != "m4"]
        verdict = self.verifier.verify(self.tampered_copy(mutate))
        self.assertEqual(verdict["verdict"], "invalid")
        self.assertIn("m4", verdict["first_failure"]["reason"])

    def test_fabricated_hit_not_trusted(self):
        """伪造命中标签不被信任：验证器从文档证据复算。"""
        def mutate(expl):
            m3 = child_of(expl, "m3")
            for child in expl["children"]:
                if child["doc_id"] == "m5":
                    child.clear()
                    child.update({"type": "result", "rank": 3, "doc_id": "m5",
                                  "doc_version": 1,
                                  "doc_hash": self.docs.get("m5", 1)["hash"],
                                  "score": m3["score"],
                                  "children": [{"type": "term", "term": "alpha",
                                                "weight": 2.0, "tf": 1,
                                                "idf": m3["children"][0]["idf"],
                                                "contribution": m3["score"]}]})
            m3.clear()
            m3.update({"type": "scored_out", "doc_id": "m3", "doc_version": 1,
                       "doc_hash": self.docs.get("m3", 1)["hash"],
                       "score": 0.0, "children": [],
                       "threshold": 0.0, "kth_doc_id": "m5"})
        verdict = self.verifier.verify(self.tampered_copy(mutate))
        self.assertEqual(verdict["verdict"], "invalid")
        self.assertIn("m5", verdict["first_failure"]["path"])

    def test_inflated_pruned_upper_bound(self):
        def mutate(expl):
            node = child_of(expl, "m4")
            node["upper_bound"] = node["threshold"] + 1.0
        verdict = self.verifier.verify(self.tampered_copy(mutate))
        self.assertEqual(verdict["verdict"], "invalid")
        self.assertEqual(verdict["first_failure"]["path"], "root/pruned(m4)")

    # ---- 文档版本替换 ----

    def test_legit_doc_update_keeps_old_explanation_valid(self):
        self.docs.update("m1", "alpha beta gamma delta")
        verdict = self.verifier.verify(self.expl)
        self.assertEqual(verdict["verdict"], "valid")

    def test_replaced_doc_history_locates_result_node(self):
        # 词项多重集合不变的篡改（绕过统计复算），仍被文档哈希复算抓出
        self.docs._docs["m1"][0]["text"] = "beta alpha alpha"
        verdict = self.verifier.verify(self.expl)
        self.assertEqual(verdict["verdict"], "invalid")
        self.assertEqual(verdict["first_failure"]["path"], "root/result#1(m1)")
        self.assertIn("哈希", verdict["first_failure"]["reason"])

    def test_stats_tampered_detected_by_recompute(self):
        self.stats.get(1)["df"]["alpha"] = 1
        verdict = self.verifier.verify(self.expl)
        self.assertEqual(verdict["verdict"], "invalid")
        self.assertEqual(verdict["first_failure"]["path"], "root")

    # ---- 历史有效性 ----

    def test_stats_update_keeps_old_explanation_valid(self):
        self.docs.add("m8", "alpha beta alpha", {"year": 2023})
        self.engine.build_stats()  # 统计版本 2
        new_expl = self.engine.search(**QUERY)
        self.assertEqual(new_expl["stats_version"], 2)
        self.assertEqual(self.expl["stats_version"], 1)
        self.assertEqual(self.verifier.verify(self.expl)["verdict"], "valid")
        self.assertEqual(self.verifier.verify(new_expl)["verdict"], "valid")

    def test_rules_update_keeps_old_explanation_valid(self):
        self.rules.add("unicode-unigram-v1", "idf-bm25-v2")  # 规则版本 2
        new_expl = self.engine.search(**QUERY)
        self.assertEqual(new_expl["rules_version"], 2)
        self.assertEqual(self.expl["rules_version"], 1)
        self.assertEqual(self.verifier.verify(self.expl)["verdict"], "valid")
        self.assertEqual(self.verifier.verify(new_expl)["verdict"], "valid")

    # ---- 缺证据只能不可验证 ----

    def test_missing_stats_snapshot_is_unverifiable(self):
        self.stats.drop(1)
        verdict = self.verifier.verify(self.expl)
        self.assertEqual(verdict["verdict"], "unverifiable")
        self.assertIn("统计", verdict["reason"])

    def test_missing_rules_version_is_unverifiable(self):
        expl = copy.deepcopy(self.expl)
        expl["rules_version"] = 99
        finalize(expl)
        self.assertEqual(self.verifier.verify(expl)["verdict"], "unverifiable")

    def test_missing_doc_history_is_unverifiable(self):
        del self.docs._docs["m1"]
        verdict = self.verifier.verify(self.expl)
        self.assertEqual(verdict["verdict"], "unverifiable")

    def test_missing_stats_never_invalid(self):
        """即使解释同时被篡改，缺统计证据时也只能判不可验证。"""
        expl = copy.deepcopy(self.expl)
        child_of(expl, "m1")["children"][0]["weight"] = 99.0
        finalize(expl)
        self.stats.drop(1)
        self.assertEqual(self.verifier.verify(expl)["verdict"], "unverifiable")


if __name__ == "__main__":
    unittest.main()
