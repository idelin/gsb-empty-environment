"""距离函数的单元测试。"""

import itertools
import random
import unittest

from editindex.distance import (
    Weights,
    damerau_levenshtein,
    key_distance,
    weighted_distance,
)


class TestWeightedDistance(unittest.TestCase):
    def test_basic_levenshtein(self):
        w = Weights()  # 全 1，含换位
        self.assertEqual(weighted_distance("kitten", "sitting", w), 3)
        self.assertEqual(weighted_distance("flaw", "lawn", w), 2)
        self.assertEqual(weighted_distance("", "", w), 0)
        self.assertEqual(weighted_distance("abc", "abc", w), 0)

    def test_unicode_code_points(self):
        w = Weights()
        # 单个码点：CJK 与 emoji 都按一个码点计
        self.assertEqual(weighted_distance("猫", "猫", w), 0)
        self.assertEqual(weighted_distance("猫", "狗", w), 1)
        self.assertEqual(weighted_distance("🐱🐱", "🐱", w), 1)
        self.assertEqual(weighted_distance("北京", "南京", w), 1)
        # 组合字符是独立码点：预组合 é(1码点) vs e+U+0301(2码点)
        # 需要 替换+插入 共 2 步 —— 这正是分词时先做 NFC 的原因
        self.assertEqual(weighted_distance("\u00e9", "e\u0301", w), 2)

    def test_weighted_insert_delete_asymmetric(self):
        w = Weights(insert=2, delete=3, substitute=1, transpose=10)
        self.assertEqual(weighted_distance("", "ab", w), 4)   # 两次插入
        self.assertEqual(weighted_distance("ab", "", w), 6)   # 两次删除
        # 替换(1) 比 删+插(5) 便宜
        self.assertEqual(weighted_distance("a", "b", w), 1)

    def test_transpose(self):
        self.assertEqual(weighted_distance("ab", "ba", Weights(transpose=1)), 1)
        # 换位太贵时退化为两次替换
        self.assertEqual(
            weighted_distance("ab", "ba", Weights(substitute=1, transpose=5)), 2
        )

    def test_negative_weight_rejected(self):
        with self.assertRaises(ValueError):
            Weights(insert=-1)


class TestDamerauLevenshtein(unittest.TestCase):
    def test_known_values(self):
        self.assertEqual(damerau_levenshtein("abc", "acb"), 1)   # 相邻换位
        self.assertEqual(damerau_levenshtein("ca", "abc"), 2)    # 真 DL（OSA 给 3）
        self.assertEqual(damerau_levenshtein("kitten", "sitting"), 3)
        self.assertEqual(damerau_levenshtein("", "abc"), 3)
        self.assertEqual(damerau_levenshtein("猫🐱", "🐱猫"), 1)

    def test_is_metric_exhaustive_small_alphabet(self):
        # 在 {a,b} 上长度 <= 3 的全部串上穷举验证度量三公理
        words = [""]
        for length in range(1, 4):
            for combo in itertools.product("ab", repeat=length):
                words.append("".join(combo))
        dist = {
            (x, y): damerau_levenshtein(x, y)
            for x, y in itertools.product(words, repeat=2)
        }
        for x, y in itertools.product(words, repeat=2):
            self.assertEqual(dist[(x, y)], dist[(y, x)])           # 对称
            self.assertEqual(dist[(x, y)] == 0, x == y)            # 同一性
        for x, y, z in itertools.product(words, repeat=3):
            self.assertLessEqual(
                dist[(x, z)], dist[(x, y)] + dist[(y, z)]          # 三角不等式
            )


class TestLowerBound(unittest.TestCase):
    def test_osa_distance_violates_triangle_inequality(self):
        # 经典反例：全单位权重下 OSA 距离不满足三角不等式
        w = Weights()
        self.assertEqual(weighted_distance("ca", "ac", w), 1)
        self.assertEqual(weighted_distance("ac", "abc", w), 1)
        self.assertEqual(weighted_distance("ca", "abc", w), 3)  # 3 > 1 + 1

    def test_asymmetric_weights_violate_symmetry(self):
        w = Weights(insert=1, delete=5, substitute=2, transpose=1)
        self.assertNotEqual(
            weighted_distance("", "x", w), weighted_distance("x", "", w)
        )

    def test_key_is_lower_bound_of_weighted_distance(self):
        # 随机串、随机权重：key <= 精确加权距离
        rng = random.Random(20260926)
        alphabet = "abé猫🐱"
        for _ in range(500):
            a = "".join(rng.choice(alphabet) for _ in range(rng.randint(0, 6)))
            b = "".join(rng.choice(alphabet) for _ in range(rng.randint(0, 6)))
            w = Weights(
                insert=rng.choice([1, 2, 3]),
                delete=rng.choice([1, 2, 3]),
                substitute=rng.choice([1, 2, 3]),
                transpose=rng.choice([1, 2, 3]),
            )
            self.assertLessEqual(
                key_distance(a, b, w), weighted_distance(a, b, w)
            )


if __name__ == "__main__":
    unittest.main()
