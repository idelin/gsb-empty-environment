#!/usr/bin/env python3
"""autocomplete 的 unittest 测试。所有数据均为合成数据。"""
import random
import unittest

from autocomplete import (
    TagIndex, apply_script, edit_distance, edit_script, naive_search,
)


def build(words, max_len=32):
    idx = TagIndex(max_len=max_len)
    for w, wt in words:
        idx.add(w, wt)
    return idx


class TestEditScript(unittest.TestCase):
    def test_basic_distances(self):
        self.assertEqual(edit_distance("ab", "ac"), 1)   # 替换
        self.assertEqual(edit_distance("ab", "ba"), 2)   # 不允许交换，两次替换
        self.assertEqual(edit_distance("ab", "ab"), 0)
        self.assertEqual(edit_distance("ab", "abc"), 1)  # 插入
        self.assertEqual(edit_distance("abc", "ab"), 1)  # 删除
        self.assertEqual(edit_distance("", ""), 0)

    def test_unicode_codepoints(self):
        self.assertEqual(edit_distance("你好", "你号"), 1)
        self.assertEqual(edit_distance("🙂", "🙂🙃"), 1)  # emoji 按单个码点计
        self.assertEqual(edit_distance("你好", "好你"), 2)  # 交换不算一次

    def test_script_roundtrip(self):
        rng = random.Random(42)
        alphabet = "ab好🙂"
        for _ in range(300):
            a = "".join(rng.choice(alphabet) for _ in range(rng.randrange(6)))
            b = "".join(rng.choice(alphabet) for _ in range(rng.randrange(6)))
            dist, script = edit_script(a, b)
            self.assertEqual(apply_script(a, script), b, (a, b, script))
            self.assertEqual(dist, len(script))


class TestMatchingSemantics(unittest.TestCase):
    def setUp(self):
        self.idx = build([("ab", 5), ("abc", 9), ("abd", 3), ("ac", 7),
                          ("ba", 6), ("aab", 4), ("b", 2)])

    def words_of(self, query, k=10):
        cands, _ = self.idx.search(query, k)
        return [w for w, *_ in cands]

    def test_prefix_and_one_edit(self):
        got = set(self.words_of("ab"))
        self.assertIn("ac", got)    # 距离 1
        self.assertIn("abc", got)   # 前缀
        self.assertIn("aab", got)   # 距离 1（插入）
        self.assertIn("b", got)     # 距离 1（删除 a）
        self.assertNotIn("ba", got)  # 距离 2，不能因交换算 1

    def test_no_transposition(self):
        idx = build([("ba", 10)])
        cands, _ = idx.search("ab", 10)
        self.assertEqual(cands, [])

    def test_dedup_multiple_paths(self):
        # "aab" 可由 (匹配a, 插入a, 匹配b) 与 (插入a, 匹配a, 匹配b) 两条路径命中
        cands, _ = self.idx.search("ab", 10)
        words = [w for w, *_ in cands]
        self.assertEqual(len(words), len(set(words)))
        self.assertEqual(words.count("aab"), 1)

    def test_exact_and_prefix_distances(self):
        cands, _ = self.idx.search("ab", 10)
        dist = {w: d for w, _, d, _ in cands}
        self.assertEqual(dist["ab"], 0)
        self.assertEqual(dist["abc"], 1)   # 前缀命中的脚本为补插后缀
        self.assertEqual(dist["ac"], 1)

    def test_scripts_valid(self):
        cands, _ = self.idx.search("ab", 10)
        for w, _, d, script in cands:
            self.assertEqual(apply_script("ab", script), w)
            self.assertEqual(d, len(script))


class TestRankingAndTopK(unittest.TestCase):
    def test_weight_desc_then_lex(self):
        idx = build([("ab", 1), ("ac", 5), ("ad", 5), ("ae", 3)])
        cands, _ = idx.search("ab", 10)
        self.assertEqual([w for w, *_ in cands], ["ac", "ad", "ae", "ab"])

    def test_topk_limit(self):
        idx = build([("ab", 1), ("ac", 5), ("ad", 4), ("ae", 3)])
        cands, _ = idx.search("ab", 2)
        self.assertEqual([w for w, *_ in cands], ["ac", "ad"])

    def test_negative_weight_rejected(self):
        idx = TagIndex()
        with self.assertRaises(ValueError):
            idx.add("x", -1)

    def test_max_len_limit(self):
        idx = TagIndex(max_len=3)
        self.assertFalse(idx.add("abcd", 1))
        self.assertTrue(idx.add("abc", 1))
        self.assertEqual(idx.rejected, 1)
        cands, _ = idx.search("abc", 10)
        self.assertEqual([w for w, *_ in cands], ["abc"])


class TestIndexVsNaive(unittest.TestCase):
    def test_equivalence_on_random_data(self):
        rng = random.Random(7)
        alphabet = "abc"
        words = {}
        for _ in range(200):
            w = "".join(rng.choice(alphabet) for _ in range(rng.randrange(1, 6)))
            words[w] = rng.randrange(0, 100)
        idx = build(list(words.items()))
        for _ in range(100):
            q = "".join(rng.choice(alphabet) for _ in range(rng.randrange(0, 5)))
            for k in (1, 3, 10, 1000):
                fast, _ = idx.search(q, k)
                slow, _, _ = naive_search(words, q, k)
                self.assertEqual([c[0] for c in fast], [c[0] for c in slow], (q, k))
                self.assertEqual([c[2] for c in fast], [c[2] for c in slow], (q, k))

    def test_index_visits_fewer_than_naive(self):
        rng = random.Random(11)
        words = {}
        for i in range(500):
            w = "".join(rng.choice("abcdef") for _ in range(rng.randrange(3, 9)))
            words[w] = float(i)
        idx = build(list(words.items()))
        _, visited = idx.search("abc", 10)
        _, scanned, _ = naive_search(words, "abc", 10)
        self.assertLess(visited, scanned)  # 共享索引避免全表扫描


if __name__ == "__main__":
    unittest.main()
