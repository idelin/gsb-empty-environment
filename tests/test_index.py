"""BK 树索引的单元测试：阈值、TopK、去重、剪枝与全扫描参考一致性。"""

import random
import unittest

from editindex.distance import Weights, weighted_distance
from editindex.index import BKTreeIndex, Match
from editindex.reference import full_scan_search, full_scan_topk
from editindex.tokenizer import tokenize, vocabulary

# 破坏三角不等式的成本组合：
# 1) 全单位权重 + 相邻换位 → OSA 距离不满足三角不等式（ca/ac/abc 反例）
# 2) 非对称插入/删除 → 距离连对称性都不满足
TRIANGLE_BREAKING_WEIGHTS = [
    Weights(insert=1, delete=1, substitute=1, transpose=1),
    Weights(insert=1, delete=5, substitute=2, transpose=1),
    Weights(insert=3, delete=1, substitute=4, transpose=1),
]


class TestThresholdSearch(unittest.TestCase):
    def setUp(self):
        self.vocab = ["cat", "bat", "rat", "cats", "car", "cart", "dog"]
        self.index = BKTreeIndex(Weights())
        self.index.build(self.vocab)

    def test_threshold_inclusive_boundary(self):
        # "cat"→"cart" 距离恰为 1，阈值含等号必须命中
        words = [m.word for m in self.index.search("cat", 1)]
        self.assertIn("cart", words)
        self.assertIn("cats", words)
        self.assertNotIn("dog", words)

    def test_exact_match_distance_zero(self):
        self.assertEqual(self.index.search("cat", 0), [Match("cat", 0.0)])

    def test_results_sorted_by_distance_then_vocab_order(self):
        matches = self.index.search("cat", 1)
        self.assertEqual(
            matches,
            [
                Match("cat", 0.0),
                Match("bat", 1.0),
                Match("rat", 1.0),
                Match("cats", 1.0),
                Match("car", 1.0),
                Match("cart", 1.0),
            ],
        )

    def test_empty_index(self):
        index = BKTreeIndex()
        self.assertEqual(index.search("cat", 2), [])
        self.assertEqual(index.topk("cat", 3), [])


class TestTopK(unittest.TestCase):
    def test_ties_complete_and_stable(self):
        # 三个词与查询等距且超过 k：等距候选必须完整、按词表顺序稳定
        vocab = ["ab", "ac", "ad", "xy"]
        index = BKTreeIndex(Weights())
        index.build(vocab)
        matches = index.topk("aa", 2)  # 第 2 名距离为 1，并列 3 个
        self.assertEqual(
            matches,
            [Match("ab", 1.0), Match("ac", 1.0), Match("ad", 1.0)],
        )

    def test_k_larger_than_vocab_returns_all(self):
        vocab = ["a", "bb", "ccc"]
        index = BKTreeIndex(Weights())
        index.build(vocab)
        matches = index.topk("a", 10)
        self.assertEqual([m.word for m in matches], ["a", "bb", "ccc"])

    def test_invalid_k(self):
        index = BKTreeIndex()
        with self.assertRaises(ValueError):
            index.topk("x", 0)


class TestDedup(unittest.TestCase):
    def test_duplicate_normalized_words_output_once(self):
        text = "Café caf\u00e9 cafe\u0301 CAFÉ cafe 北京 北京 北京"
        index = BKTreeIndex.from_text(text, Weights())
        matches = index.search("café", 0)
        self.assertEqual(matches, [Match("café", 0.0)])
        # 词表中同一规范词只有一个条目
        self.assertEqual(index.size, 3)  # café, cafe, 北京


class TestTriangleBreakingCosts(unittest.TestCase):
    """构造破坏三角不等式（甚至对称性）的成本，验证索引不漏词。"""

    VOCAB = [
        "ca", "ac", "abc", "bac", "cab", "cba", "abcd", "abdc",
        "猫", "猫咪", "咪猫", "🐱🐶", "🐶🐱", "café", "cafe",
    ]
    QUERIES = ["ca", "abc", "bac", "猫咪", "🐱🐶", "café", "xyz", ""]

    def test_no_missed_words_against_full_scan(self):
        for weights in TRIANGLE_BREAKING_WEIGHTS:
            index = BKTreeIndex(weights)
            index.build(self.VOCAB)
            for query in self.QUERIES:
                for radius in (0, 1, 2, 3, 5):
                    with self.subTest(w=weights, q=query, r=radius):
                        self.assertEqual(
                            index.search(query, radius),
                            full_scan_search(self.VOCAB, query, weights, radius),
                        )
                for k in (1, 2, 5, 100):
                    with self.subTest(w=weights, q=query, k=k):
                        self.assertEqual(
                            index.topk(query, k),
                            full_scan_topk(self.VOCAB, query, weights, k),
                        )

    def test_oso_counterexample_vocab_no_miss(self):
        # 专门针对 ca/ac/abc 反例：OSA 下 d(ca,abc)=3 > d(ca,ac)+d(ac,abc)=2
        w = Weights()
        self.assertGreater(
            weighted_distance("ca", "abc", w),
            weighted_distance("ca", "ac", w) + weighted_distance("ac", "abc", w),
        )
        index = BKTreeIndex(w)
        index.build(self.VOCAB)
        for radius in range(0, 5):
            self.assertEqual(
                index.search("ca", radius),
                full_scan_search(self.VOCAB, "ca", w, radius),
            )


class TestFuzzAgainstFullScan(unittest.TestCase):
    """随机 Unicode 小词集 + 随机权重：索引结果必须与暴力 DP 全扫描一致。"""

    def test_random_vocab_and_weights(self):
        rng = random.Random(42)
        alphabet = ["a", "b", "c", "é", "ß", "猫", "狗", "🐱", "1"]
        for trial in range(150):
            vocab = []
            seen = set()
            for _ in range(rng.randint(1, 40)):
                word = "".join(
                    rng.choice(alphabet) for _ in range(rng.randint(1, 6))
                )
                if word not in seen:
                    seen.add(word)
                    vocab.append(word)
            weights = Weights(
                insert=rng.choice([0, 1, 2, 3]),
                delete=rng.choice([0, 1, 2, 3]),
                substitute=rng.choice([0, 1, 2, 3]),
                transpose=rng.choice([0, 1, 2, 3]),
            )
            index = BKTreeIndex(weights)
            index.build(vocab)
            query = "".join(
                rng.choice(alphabet) for _ in range(rng.randint(0, 5))
            )
            for radius in (0, 1, 2, 4):
                with self.subTest(trial=trial, q=query, r=radius, w=weights):
                    self.assertEqual(
                        index.search(query, radius),
                        full_scan_search(vocab, query, weights, radius),
                    )
            for k in (1, 3, 7):
                with self.subTest(trial=trial, q=query, k=k, w=weights):
                    self.assertEqual(
                        index.topk(query, k),
                        full_scan_topk(vocab, query, weights, k),
                    )


class TestPruningEffectiveness(unittest.TestCase):
    def test_pruning_skips_exact_computations(self):
        rng = random.Random(7)
        alphabet = "abcdef"
        vocab = sorted(
            {
                "".join(rng.choice(alphabet) for _ in range(rng.randint(5, 8)))
                for _ in range(2000)
            }
        )
        index = BKTreeIndex(Weights())
        index.build(vocab)
        matches = index.search("abcdef", 1)
        self.assertGreater(len(matches), 0)
        # 半径 1 的查询应当剪掉大量节点（精确距离计算次数远小于全扫描）
        self.assertLess(index.last_exact_computed, len(vocab) // 2)


class TestFromText(unittest.TestCase):
    def test_end_to_end(self):
        text = "东京 南京 北京 东京,东京!西京 猫咪 猫 🐱🐱 🐱"
        index = BKTreeIndex.from_text(text, Weights())
        # 重复规范词只输出一次
        matches = index.search("东京", 1)
        self.assertEqual(
            [m.word for m in matches].count("东京"), 1
        )
        self.assertEqual(matches[0], Match("东京", 0.0))
        words = [m.word for m in matches]
        self.assertIn("南京", words)   # 距离 1
        self.assertIn("北京", words)
        self.assertIn("西京", words)
        self.assertNotIn("猫咪", words)  # 距离 2，超出阈值


if __name__ == "__main__":
    unittest.main()
