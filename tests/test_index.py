"""索引行为：剪枝与全扫描一致、阈值含等号、去重、TopK 等距完整、稳定排序。"""
import random
import unicodedata
import unittest

from uniedit import CostModel, WordIndex, distance, length_lower_bound

WORDS = [
    "apple", "apply", "apples", "applet", "able",
    "café", "cafe", "caffe", "中文", "中问", "中文测试",
    "strasse", "straße", "naïve", "naive", "\U0001F600", "\U0001F603",
]


def reference_scan(words, query, costs, max_dist=None, top_k=None):
    """独立参考：规范词去重后对每个词算精确距离，不做任何剪枝。"""
    query = unicodedata.normalize("NFC", query).casefold()
    first = {}
    for i, w in enumerate(words):
        first.setdefault(unicodedata.normalize("NFC", w).casefold(), i)
    pairs = [(w, distance(query, w, costs)) for w in first]
    if max_dist is not None:
        pairs = [(w, d) for w, d in pairs if d <= max_dist]
    pairs.sort(key=lambda wd: (wd[1], first[wd[0]]))
    if top_k is not None and len(pairs) > top_k:
        tau = pairs[top_k - 1][1]
        pairs = [(w, d) for w, d in pairs if d <= tau]
    return pairs


class TestPruningMatchesFullScan(unittest.TestCase):
    """度量成本下，BK 树结果必须与全扫描参考逐条一致（小 Unicode 词集）。"""

    def setUp(self):
        self.costs = CostModel.uniform()
        self.idx = WordIndex.from_words(WORDS, self.costs)
        self.assertTrue(all(self.idx.metric_ok(q) for q in ["apple", "中文"]))

    def test_threshold_queries(self):
        rng = random.Random(3)
        queries = ["apple", "café", "中文", "strasse", "\U0001F600", "xyz", ""]
        queries += ["".join(rng.choice("abcé中") for _ in range(4)) for _ in range(5)]
        for q in queries:
            for t in (0, 1, 2, 3):
                got = [(h.word, h.distance) for h in self.idx.search(q, max_dist=t).hits]
                want = reference_scan(WORDS, q, self.costs, max_dist=t)
                self.assertEqual(got, want, f"q={q!r} t={t}")

    def test_topk_queries(self):
        for q in ["apple", "café", "中文", "\U0001F600"]:
            for k in (1, 2, 3, 5, len(WORDS) + 3):
                got = [(h.word, h.distance) for h in self.idx.search(q, top_k=k).hits]
                want = reference_scan(WORDS, q, self.costs, top_k=k)
                self.assertEqual(got, want, f"q={q!r} k={k}")

    def test_random_unicode_wordset(self):
        rng = random.Random(42)
        alphabet = "abé中问\U0001F600\U0001F603"
        words = list({
            "".join(rng.choice(alphabet) for _ in range(rng.randint(1, 5)))
            for _ in range(60)
        })
        idx = WordIndex.from_words(words, self.costs)
        pruned = 0
        for _ in range(15):
            q = "".join(rng.choice(alphabet) for _ in range(rng.randint(1, 4)))
            t = rng.randint(0, 3)
            res = idx.search(q, max_dist=t)
            self.assertEqual(res.mode, "bk-tree")
            pruned += res.visited < res.total
            got = [(h.word, h.distance) for h in res.hits]
            want = reference_scan(words, q, self.costs, max_dist=t)
            self.assertEqual(got, want, f"q={q!r} t={t}")
        self.assertGreater(pruned, 0)  # 确实发生了剪枝


class TestNonMetricFallback(unittest.TestCase):
    """破坏三角不等式的成本：自动回退安全方案，且不漏词。"""

    def test_cheap_transpose_falls_back_no_miss(self):
        costs = CostModel.uniform(trans=1.0)  # 破坏三角不等式（见 test_metric）
        words = ["ca", "ac", "abc", "cab", "bac", "abcd", "ca" , "AC"]
        idx = WordIndex.from_words(words, costs)
        self.assertFalse(idx.metric_ok("ca"))
        for q in ["ca", "abc", "ac"]:
            for t in (0, 1, 2, 3):
                res = idx.search(q, max_dist=t)
                self.assertEqual(res.mode, "full-scan")
                got = [(h.word, h.distance) for h in res.hits]
                want = reference_scan(words, q, costs, max_dist=t)
                self.assertEqual(got, want, f"q={q!r} t={t}：回退后不得漏词")

    def test_asymmetric_costs_fall_back(self):
        costs = CostModel.uniform(ins=1.0, delete=3.0)  # ins != del，非对称
        idx = WordIndex.from_words(["ab", "abc", "a", "b"], costs)
        res = idx.search("ab", max_dist=2)
        self.assertEqual(res.mode, "full-scan")
        got = [(h.word, h.distance) for h in res.hits]
        want = reference_scan(["ab", "abc", "a", "b"], "ab", costs, max_dist=2)
        self.assertEqual(got, want)

    def test_fallback_topk(self):
        costs = CostModel.uniform(trans=1.0)
        idx = WordIndex.from_words(WORDS, costs)
        res = idx.search("apple", top_k=4)
        self.assertEqual(res.mode, "full-scan")
        got = [(h.word, h.distance) for h in res.hits]
        want = reference_scan(WORDS, "apple", costs, top_k=4)
        self.assertEqual(got, want)


class TestQuerySemantics(unittest.TestCase):
    def setUp(self):
        self.costs = CostModel.uniform()

    def test_threshold_inclusive(self):
        idx = WordIndex.from_words(["abc", "abd", "abcde"], self.costs)
        res = idx.search("abc", max_dist=1)
        self.assertIn(("abd", 1.0), [(h.word, h.distance) for h in res.hits])
        res2 = idx.search("abc", max_dist=2)
        self.assertIn("abcde", [h.word for h in res2.hits])  # 距离恰为 2，含等号

    def test_duplicate_norm_words_emitted_once(self):
        idx = WordIndex.from_text("Apple apple APPLE aPpLe", self.costs)
        self.assertEqual(len(idx), 1)
        res = idx.search("apple", max_dist=0)
        self.assertEqual(len(res.hits), 1)
        self.assertEqual(res.hits[0].surface, "Apple")  # 保留首次出现的形式

    def test_stable_order_by_first_occurrence(self):
        idx = WordIndex.from_words(["zz", "ab", "ac", "ad"], self.costs)
        res = idx.search("aa", max_dist=1)
        # ab/ac/ad 同为距离 1，按首次出现位置排序
        self.assertEqual([h.word for h in res.hits], ["ab", "ac", "ad"])

    def test_topk_ties_complete(self):
        idx = WordIndex.from_words(["ab", "ac", "ad", "ax", "zz"], self.costs)
        res = idx.search("aa", top_k=2)
        # 第 2 小距离为 1，所有距离 1 的候选必须完整返回
        self.assertEqual([h.word for h in res.hits], ["ab", "ac", "ad", "ax"])

    def test_topk_larger_than_index(self):
        idx = WordIndex.from_words(["a", "b"], self.costs)
        res = idx.search("a", top_k=10)
        self.assertEqual(len(res.hits), 2)

    def test_empty_and_errors(self):
        idx = WordIndex.from_words([], self.costs)
        self.assertEqual(idx.search("a", max_dist=1).hits, [])
        idx2 = WordIndex.from_words(["a"], self.costs)
        self.assertEqual(idx2.search("", max_dist=1).hits[0].word, "a")
        with self.assertRaises(ValueError):
            idx2.search("a")


if __name__ == "__main__":
    unittest.main()
