"""tagcomplete 的 unittest 测试。所有数据均为合成数据。"""

import random
import unittest

from tagcomplete import (
    TagIndex,
    edit_distance,
    edit_script,
    naive_search,
)


def apply_script(source, script):
    """按脚本语义把 source 变换为目标串，用于验证脚本正确性。"""
    chars = list(source)
    offset = 0
    for op in script:
        pos = op["pos"] + offset
        if op["op"] == "insert":
            chars.insert(pos, op["char"])
            offset += 1
        elif op["op"] == "delete":
            assert chars[pos] == op["char"], (source, script)
            del chars[pos]
            offset -= 1
        elif op["op"] == "replace":
            assert chars[pos] == op["from"], (source, script)
            chars[pos] = op["to"]
        else:  # pragma: no cover
            raise AssertionError(f"未知操作 {op}")
    return "".join(chars)


class TestEditDistance(unittest.TestCase):
    def test_basic(self):
        self.assertEqual(edit_distance("", ""), 0)
        self.assertEqual(edit_distance("ab", "ab"), 0)
        self.assertEqual(edit_distance("ab", "ac"), 1)   # 替换
        self.assertEqual(edit_distance("ab", "a"), 1)    # 删除
        self.assertEqual(edit_distance("a", "ab"), 1)    # 插入

    def test_no_transposition(self):
        # 不允许交换：ab -> ba 需要 2 步（两次替换），不能算 1
        self.assertEqual(edit_distance("ab", "ba"), 2)
        self.assertEqual(edit_distance("abcd", "abdc"), 2)

    def test_unicode_codepoints(self):
        self.assertEqual(edit_distance("🙂a", "🙂b"), 1)
        self.assertEqual(edit_distance("微笑", "微"), 1)
        self.assertEqual(edit_distance("🇨🇳x", "🇨🇳y"), 1)  # 国旗为 2 个码点


class TestEditScript(unittest.TestCase):
    def test_scripts(self):
        self.assertEqual(edit_script("ab", "ab"), [])
        self.assertEqual(
            edit_script("ab", "ac"),
            [{"op": "replace", "pos": 1, "from": "b", "to": "c"}])
        self.assertEqual(
            edit_script("ab", "a"), [{"op": "delete", "pos": 1, "char": "b"}])
        self.assertEqual(
            edit_script("a", "ab"), [{"op": "insert", "pos": 1, "char": "b"}])

    def test_script_roundtrip_random(self):
        rng = random.Random(20260927)
        alphabet = "abc🙂微"
        for _ in range(500):
            a = "".join(rng.choice(alphabet) for _ in range(rng.randrange(6)))
            b = "".join(rng.choice(alphabet) for _ in range(rng.randrange(6)))
            script = edit_script(a, b)
            self.assertEqual(apply_script(a, script), b)
            self.assertEqual(len(script), edit_distance(a, b))  # 最短性


class TestIndexSemantics(unittest.TestCase):
    def test_exact_prefix(self):
        idx = TagIndex.from_entries([("app", 8), ("apple", 10), ("application", 6)])
        res = idx.search("app", k=10)
        words = [c.word for c in res.candidates]
        self.assertEqual(words, ["apple", "app", "application"])  # 权重降序
        for c in res.candidates:
            self.assertEqual(c.distance, 0)
            self.assertEqual(c.matched_prefix, "app")
            self.assertEqual(c.word, c.matched_prefix + c.completion)

    def test_substitution_one_edit(self):
        idx = TagIndex.from_entries([("ac", 5)])
        (c,) = idx.search("ab").candidates
        self.assertEqual(c.word, "ac")
        self.assertEqual(c.distance, 1)
        self.assertEqual(c.matched_prefix, "ac")
        self.assertEqual(
            list(c.script),
            [{"op": "replace", "pos": 1, "from": "b", "to": "c"}])

    def test_no_transposition_acceptance(self):
        # 验收边界：查询 ab，ac 距离 1 命中；ba 距离 2 不得命中（交换不算 1）
        idx = TagIndex.from_entries([("ac", 5), ("ba", 100)])
        res = idx.search("ab")
        words = [c.word for c in res.candidates]
        self.assertIn("ac", words)
        self.assertNotIn("ba", words)

    def test_insert_and_delete(self):
        idx = TagIndex.from_entries([("a", 1), ("axb", 2)])
        res = {c.word: c for c in idx.search("ab").candidates}
        self.assertEqual(res["a"].distance, 1)          # 删除 b
        self.assertEqual(res["a"].matched_prefix, "a")
        self.assertEqual(res["axb"].distance, 1)        # 插入 x
        self.assertEqual(res["axb"].matched_prefix, "axb")
        self.assertEqual(
            list(res["axb"].script),
            [{"op": "insert", "pos": 1, "char": "x"}])

    def test_unicode_codepoint_edits(self):
        idx = TagIndex.from_entries([("🙂b", 3), ("微", 2), ("🙂", 1)])
        res = {c.word: c for c in idx.search("🙂a").candidates}
        self.assertEqual(res["🙂b"].distance, 1)   # 替换一个码点
        self.assertEqual(res["🙂"].distance, 1)    # 删除一个码点
        self.assertNotIn("微", res)

    def test_dedup_multiple_paths(self):
        # 验收边界：查询 ab 命中 abc 的前缀 "ab"(距离0) 与 "abc"(距离1)，
        # 多条路径命中同一候选只能返回一次
        idx = TagIndex.from_entries([("abc", 7)])
        res = idx.search("ab")
        self.assertEqual(len(res.candidates), 1)
        c = res.candidates[0]
        self.assertEqual(c.distance, 0)
        self.assertEqual(c.matched_prefix, "ab")
        self.assertEqual(c.completion, "c")

    def test_dedup_duplicate_entries(self):
        idx = TagIndex(max_len=8)
        idx.add("tag", 3)
        idx.add("tag", 5)
        idx.add("tag", 4)
        self.assertEqual(idx.size, 1)
        res = idx.search("tag")
        self.assertEqual(len(res.candidates), 1)
        self.assertEqual(res.candidates[0].weight, 5)  # 保留最大权重

    def test_topk_order_weight_then_lex(self):
        entries = [("b1", 5), ("a1", 5), ("c1", 9), ("a0", 5)]
        idx = TagIndex.from_entries(entries)
        res = idx.search("", k=3)
        self.assertEqual([c.word for c in res.candidates], ["c1", "a0", "a1"])

    def test_empty_query_returns_all(self):
        idx = TagIndex.from_entries([("x", 1), ("yz", 2)])
        res = idx.search("", k=10)
        self.assertEqual([c.word for c in res.candidates], ["yz", "x"])
        self.assertTrue(all(c.distance == 0 and c.matched_prefix == ""
                            for c in res.candidates))

    def test_fuzzy_insert_whole_word(self):
        # 查询 ab 对词条 acb：整词距离 1（插入 c），模糊整词命中
        idx = TagIndex.from_entries([("acb", 1)])
        (c,) = idx.search("ab").candidates
        self.assertEqual(c.distance, 1)
        self.assertEqual(c.matched_prefix, "acb")
        self.assertEqual(c.completion, "")
        self.assertEqual(
            list(c.script), [{"op": "insert", "pos": 1, "char": "c"}])

    def test_exact_prefix_preferred_over_fuzzy(self):
        # 词条 abc 对查询 ab：既是精确前缀、整词距离也为 1，
        # 只返回一次且取精确前缀解释（距离 0）
        idx = TagIndex.from_entries([("abc", 1)])
        (c,) = idx.search("ab").candidates
        self.assertEqual(c.distance, 0)
        self.assertEqual(c.matched_prefix, "ab")
        self.assertEqual(c.completion, "c")
        self.assertEqual(list(c.script), [])

    def test_completion_beyond_one_edit_still_matches(self):
        # 命中前缀之后的补全部分可以任意长，不受编辑数限制
        idx = TagIndex.from_entries([("abzzz", 1)])
        (c,) = idx.search("ab").candidates
        self.assertEqual(c.matched_prefix, "ab")
        self.assertEqual(c.completion, "zzz")

    def test_validation(self):
        idx = TagIndex(max_len=3)
        with self.assertRaises(ValueError):
            idx.add("toolong", 1)
        with self.assertRaises(ValueError):
            idx.add("ok", -1)
        with self.assertRaises(ValueError):
            idx.add("ok", float("inf"))
        with self.assertRaises(ValueError):
            idx.add("ok", float("nan"))
        with self.assertRaises(TypeError):
            idx.add("ok", "1")
        with self.assertRaises(ValueError):
            TagIndex(max_len=-1)

    def test_query_too_long(self):
        idx = TagIndex.from_entries([("ab", 1)], max_len=2)
        self.assertEqual(idx.search("abcd").candidates, ())
        self.assertEqual(idx.search("abcd").nodes_visited, 0)
        self.assertEqual(len(idx.search("abc").candidates), 1)  # 删 1 个字符可命中

    def test_stats_nodes_visited(self):
        entries = [(f"w{i:03d}", i) for i in range(200)]
        idx = TagIndex.from_entries(entries)
        res = idx.search("w1", k=10)
        self.assertGreater(res.nodes_visited, 0)
        self.assertLessEqual(res.nodes_visited, idx.node_count)
        # 索引查询应显著少于朴素的全词条扫描
        naive = naive_search(list(idx.entries()), "w1", k=10)
        self.assertEqual(naive.words_examined, 200)
        self.assertLess(res.nodes_visited, naive.words_examined)


class TestTrieVsNaive(unittest.TestCase):
    def test_random_equivalence(self):
        rng = random.Random(20260927)
        alphabet = "ab🙂微"
        entries = []
        seen = set()
        for _ in range(300):
            word = "".join(rng.choice(alphabet) for _ in range(rng.randrange(7)))
            if word in seen:
                continue
            seen.add(word)
            entries.append((word, rng.randrange(20)))
        idx = TagIndex.from_entries(entries, max_len=8)
        canonical = sorted(idx.entries())
        queries = [""] + [
            "".join(rng.choice(alphabet) for _ in range(rng.randrange(6)))
            for _ in range(200)
        ]
        for query in queries:
            for k in (1, 3, 10, 10_000):
                got = idx.search(query, k=k)
                want = naive_search(canonical, query, k=k)
                self.assertEqual(got.candidates, want.candidates,
                                 f"query={query!r} k={k}")
                self.assertLessEqual(got.nodes_visited, idx.node_count)
                # 候选唯一性
                words = [c.word for c in got.candidates]
                self.assertEqual(len(words), len(set(words)))
                # 脚本可复现匹配前缀，且 word = 前缀 + 补全
                for c in got.candidates:
                    self.assertEqual(apply_script(query, c.script),
                                     c.matched_prefix)
                    self.assertEqual(c.word, c.matched_prefix + c.completion)
                    self.assertEqual(len(c.script), c.distance)


if __name__ == "__main__":
    unittest.main()
