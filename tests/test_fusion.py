import unittest

from fusion import (
    AllZeroWeightsError,
    Index,
    NegativeWeightError,
    exhaustive_fuse,
    exact_score,
    fuse_topk,
    greedy_upper_bound,
    load_corpus,
    validate_weights,
)


def make_index():
    return Index(load_corpus("data/corpus.tsv"))


WEIGHTS = {"title": 0.7, "body": 0.3}


class TestWeightValidation(unittest.TestCase):
    def test_negative_rejected(self):
        with self.assertRaises(NegativeWeightError):
            validate_weights({"title": -1.0, "body": 0.5})

    def test_all_zero_rejected(self):
        with self.assertRaises(AllZeroWeightsError):
            fuse_topk(make_index(), "检索", {"title": 0.0, "body": 0.0}, k=3)

    def test_bad_k(self):
        with self.assertRaises(ValueError):
            fuse_topk(make_index(), "检索", WEIGHTS, k=0)

    def test_zero_weight_field_ignored(self):
        index = make_index()
        res = fuse_topk(index, "检索 融合",
                        {"title": 1.0, "body": 0.0}, k=5)
        self.assertNotIn("body", res.pages_fetched)
        ref = exhaustive_fuse(index, "检索 融合",
                              {"title": 1.0, "body": 0.0}, k=5)
        self.assertEqual(
            [(h.doc_id, round(h.score, 12)) for h in res.hits],
            [(h.doc_id, round(h.score, 12)) for h in ref.hits],
        )
        for h in res.hits:
            self.assertNotIn("body", h.field_scores)


class TestMissingFieldRule(unittest.TestCase):
    def test_missing_field_not_zero_penalty(self):
        # d05 只在 body 命中（且 body 归一化=1.0）。
        res = fuse_topk(make_index(), "检索 融合", WEIGHTS, k=10)
        by_id = {h.doc_id: h for h in res.hits}
        d05 = by_id["d05"]
        self.assertAlmostEqual(d05.score, 1.0)  # 非 0.3*1.0
        self.assertEqual(d05.missing_fields, ("title",))
        self.assertEqual(set(d05.field_scores), {"body"})

    def test_exact_score_averages_present_only(self):
        s = exact_score({"body": 1.0}, {"title": 0.7, "body": 0.3})
        self.assertAlmostEqual(s, 1.0)
        s2 = exact_score({"title": 1.0, "body": 0.5},
                         {"title": 0.7, "body": 0.3})
        self.assertAlmostEqual(s2, 0.85)


class TestThresholdEquivalence(unittest.TestCase):
    def test_matches_exhaustive(self):
        index = make_index()
        for query in ["检索", "融合", "检索 融合", "fusion", "归一化",
                      "不存在的词xyz"]:
            with self.subTest(query=query):
                got = fuse_topk(index, query, WEIGHTS, k=3, page_size=2)
                ref = exhaustive_fuse(index, query, WEIGHTS, k=3)
                self.assertEqual(
                    [(h.doc_id, round(h.score, 12)) for h in got.hits],
                    [(h.doc_id, round(h.score, 12)) for h in ref.hits],
                )

    def test_baseline_stable_across_page_sizes(self):
        index = make_index()
        baseline = None
        for ps in (1, 2, 3, 100):
            res = fuse_topk(index, "检索 融合", WEIGHTS, k=3, page_size=ps)
            cur = [(h.doc_id, round(h.score, 12)) for h in res.hits]
            if baseline is None:
                baseline = cur
            else:
                self.assertEqual(cur, baseline)

    def test_k_larger_than_matches(self):
        index = make_index()
        res = fuse_topk(index, "归一化", WEIGHTS, k=50)
        ref = exhaustive_fuse(index, "归一化", WEIGHTS, k=50)
        self.assertEqual(len(res.hits), len(ref.hits))


class TestUpperBoundProof(unittest.TestCase):
    def test_greedy_ub_is_upper_bound(self):
        weights = {"a": 1.0, "b": 2.0, "c": 3.0}
        known = {"a": 0.4}
        caps = {"a": 1.0, "b": 0.9, "c": 0.2}
        ub = greedy_upper_bound(known, caps, weights)
        # 枚举所有未知字段子集 + 端点分值，验证贪心不低估。
        import itertools
        best = 0.4
        fields = ["b", "c"]
        for r in range(1, 3):
            for subset in itertools.combinations(fields, r):
                num = 1.0 * 0.4
                den = 1.0
                for f in subset:
                    num += weights[f] * caps[f]
                    den += weights[f]
                best = max(best, num / den)
        self.assertAlmostEqual(ub, best, places=12)
        self.assertGreaterEqual(ub + 1e-12, 0.4)

    def test_strict_stop_preserves_ties(self):
        res = fuse_topk(make_index(), "检索 融合", WEIGHTS, k=3, page_size=2)
        self.assertEqual(res.stop_reason, "upper_bound_proven")
        ids = [h.doc_id for h in res.hits]
        # d02 与 d06 同为 0.85，二者都必须保留（严格大于才停止）。
        self.assertIn("d02", ids)
        self.assertIn("d06", ids)

    def test_stop_is_proven_sooner_or_equal(self):
        index = make_index()
        res = fuse_topk(index, "检索", WEIGHTS, k=1, page_size=1)
        ref = exhaustive_fuse(index, "检索", WEIGHTS, k=1)
        self.assertEqual(res.hits[0].doc_id, ref.hits[0].doc_id)
        # 阈值模式读取的页数不应超过全量所需的条目数
        total = sum(len(index.get_snapshot(["检", "索"], f).postings)
                    for f in ("title", "body"))
        self.assertLessEqual(sum(res.pages_fetched.values()), total)


class TestDegrade(unittest.TestCase):
    def test_unbounded_field_degrades_but_exact(self):
        index = make_index()
        res = fuse_topk(index, "检索 融合", WEIGHTS, k=3,
                        bounded={"body": False})
        self.assertTrue(res.degraded)
        self.assertEqual(res.mode, "degraded_exhaustive")
        self.assertIn("body", res.degrade_reasons[0])
        ref = exhaustive_fuse(index, "检索 融合", WEIGHTS, k=3)
        self.assertEqual(
            [(h.doc_id, round(h.score, 12)) for h in res.hits],
            [(h.doc_id, round(h.score, 12)) for h in ref.hits],
        )

    def test_norm_violation_degrades(self):
        import fusion.index as index_mod
        index = make_index()
        original = index_mod.normalize_series

        def tampered(values):
            out = original(values)
            if out:
                out = list(out)
                out[0] = 2.0  # 破坏 [0,1] 假设
            return out

        index_mod.normalize_series = tampered
        try:
            fresh = Index(load_corpus("data/corpus.tsv"))
            res = fuse_topk(fresh, "检索", WEIGHTS, k=2, page_size=1)
            self.assertTrue(res.degraded)
            self.assertEqual(res.stop_reason, "norm_violation")
        finally:
            index_mod.normalize_series = original


if __name__ == "__main__":
    unittest.main()
