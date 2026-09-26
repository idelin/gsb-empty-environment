import random
import string
import unittest

from topk import StaleCursorError, TopKSuggester, normalize, tokenize


def ref_suggest(freq: dict, prefix: str, k: int):
    """参考模型：全排序。"""
    norm_prefix = normalize(prefix)
    items = [(w, f) for w, f in freq.items() if w.startswith(norm_prefix)]
    items.sort(key=lambda wf: (-wf[1], wf[0]))
    return items[:k]


class TokenizerTest(unittest.TestCase):
    def test_rules(self):
        self.assertEqual(list(tokenize("state-of-the-art")), ["state", "of", "the", "art"])
        self.assertEqual(list(tokenize("don't stop")), ["don", "t", "stop"])
        self.assertEqual(list(tokenize("a_b c2 中文词")), ["a_b", "c2", "中文词"])
        self.assertEqual(list(tokenize("  \t\n")), [])

    def test_nfc_and_casefold(self):
        # 组合序列 é (e + U+0301) 与预组合 é (U+00E9) 归一为同一身份
        self.assertEqual(normalize("café"), normalize("café"))
        self.assertEqual(normalize("Straße"), "strasse")
        self.assertEqual(normalize("APPLE"), "apple")


class BasicRankingTest(unittest.TestCase):
    def test_freq_desc_then_word_asc(self):
        s = TopKSuggester()
        for w, f in [("banana", 5), ("band", 5), ("bank", 7), ("banner", 3)]:
            s.set(w, f)
        got = [(i.normalized, i.freq) for i in s.suggest("ban", 10).items]
        self.assertEqual(got, [("bank", 7), ("banana", 5), ("band", 5), ("banner", 3)])

    def test_prefix_word_and_its_extension_both_returned(self):
        s = TopKSuggester()
        s.set("app", 4)
        s.set("apple", 9)
        s.set("application", 1)
        got = [i.normalized for i in s.suggest("app", 10).items]
        self.assertEqual(got, ["apple", "app", "application"])

    def test_empty_prefix_respects_result_cap(self):
        s = TopKSuggester(max_results=50)
        for i in range(200):
            s.set(f"w{i:03d}", i + 1)
        page = s.suggest("", 10**6)
        self.assertEqual(len(page.items), 50)
        self.assertEqual(page.items[0].normalized, "w199")

    def test_display_alias_aggregation(self):
        s = TopKSuggester()
        s.add("Apple", 2)
        s.add("apple", 5)
        s.add("APPLE", 1)
        self.assertEqual(s.frequency("aPpLe"), 8)
        self.assertEqual(s.display_of("APPLE"), "apple")  # 观测频次最高
        self.assertEqual(s.suggest("app", 1).items[0].display, "apple")

    def test_display_alias_tie_break_deterministic(self):
        s = TopKSuggester()
        s.add("apple", 3)
        s.add("Apple", 3)
        self.assertEqual(s.display_of("apple"), "Apple")  # 并列取字典序最小


class UpdateDeleteBackfillTest(unittest.TestCase):
    def test_delete_top_backfills_next_candidate(self):
        s = TopKSuggester()
        for w, f in [("alpha", 100), ("alpine", 90), ("alps", 80), ("altar", 70)]:
            s.set(w, f)
        self.assertEqual([i.normalized for i in s.suggest("al", 2).items], ["alpha", "alpine"])
        s.remove("alpha")
        self.assertEqual([i.normalized for i in s.suggest("al", 2).items], ["alpine", "alps"])
        s.remove("alpine")
        self.assertEqual([i.normalized for i in s.suggest("al", 2).items], ["alps", "altar"])

    def test_freq_decrease_backfills(self):
        s = TopKSuggester()
        for w, f in [("x1", 100), ("x2", 99), ("x3", 98)]:
            s.set(w, f)
        s.set("x1", 1)
        got = [(i.normalized, i.freq) for i in s.suggest("x", 3).items]
        self.assertEqual(got, [("x2", 99), ("x3", 98), ("x1", 1)])

    def test_backfill_beyond_initial_bound(self):
        # 缓存宽度被截断后，删除高频词必须补回更靠后的候选
        s = TopKSuggester(base_bound=4)
        for i in range(30):
            s.set(f"p{i:02d}", 100 - i)
        for i in range(10):
            s.remove(f"p{i:02d}")
        got = [i.normalized for i in s.suggest("p", 5).items]
        self.assertEqual(got, [f"p{i}" for i in range(10, 15)])

    def test_remove_missing_is_noop(self):
        s = TopKSuggester()
        self.assertFalse(s.remove("nope"))
        self.assertEqual(len(s), 0)


class PaginationTest(unittest.TestCase):
    def _filled(self, n=37):
        s = TopKSuggester()
        for i in range(n):
            s.set(f"item{i:02d}", (i * 7) % 13 + 1)
        return s

    def test_ranked_pages_concatenate_to_full_order(self):
        s = self._filled()
        full = [i.normalized for i in s.suggest("item", 100).items]
        seen, cursor, pages = [], None, 0
        while True:
            page = s.suggest("item", 5, cursor=cursor)
            seen.extend(i.normalized for i in page.items)
            pages += 1
            if not page.has_more:
                break
            cursor = page.cursor
            self.assertIsNotNone(cursor)
        self.assertEqual(seen, full)
        self.assertEqual(len(seen), len(set(seen)))
        self.assertGreater(pages, 1)

    def test_cursor_prefix_mismatch_rejected(self):
        s = self._filled()
        cursor = s.suggest("item", 3).cursor
        with self.assertRaises(ValueError):
            s.suggest("other", 3, cursor=cursor)

    def test_lex_pagination(self):
        s = self._filled(25)
        all_words = sorted(w for w in (f"item{i:02d}" for i in range(25)))
        seen, after = [], None
        while True:
            page, after = s.list_lex("item", limit=6, start_after=after)
            seen.extend(i.normalized for i in page)
            if after is None:
                break
        self.assertEqual(seen, all_words)

    def test_lex_skips_start_after(self):
        s = TopKSuggester()
        for w in ["a1", "a2", "a3", "b1", "b2"]:
            s.set(w, 1)
        page, _ = s.list_lex("", limit=10, start_after="a2")
        self.assertEqual([i.normalized for i in page], ["a3", "b1", "b2"])


class CompactionTest(unittest.TestCase):
    def test_old_cursor_invalidated_after_compact(self):
        s = TopKSuggester()
        for i in range(20):
            s.set(f"w{i:02d}", i + 1)
        cursor = s.suggest("w", 5).cursor
        self.assertIsNotNone(cursor)
        s.compact()
        with self.assertRaises(StaleCursorError):
            s.suggest("w", 5, cursor=cursor)

    def test_results_identical_after_compact(self):
        s = TopKSuggester()
        for i in range(50):
            s.set(f"pre{i:02d}", (i * 11) % 17 + 1)
        before = [(i.normalized, i.freq) for i in s.suggest("pre", 50).items]
        s.compact()
        after = [(i.normalized, i.freq) for i in s.suggest("pre", 50).items]
        self.assertEqual(before, after)

    def test_compact_reduces_nodes(self):
        s = TopKSuggester()
        for i in range(100):
            s.set(f"common-prefix-word-{i:03d}", i + 1)
        for i in range(90):
            s.remove(f"common-prefix-word-{i:03d}")
        before = s.stats()["nodes"]
        s.compact()
        after = s.stats()["nodes"]
        self.assertLessEqual(after, before)
        self.assertEqual(len(s), 10)


class EfficiencyTest(unittest.TestCase):
    def test_untouched_subtree_not_rescanned(self):
        s = TopKSuggester()
        for i in range(500):
            s.set(f"x{i:03d}", i + 1)
            s.set(f"y{i:03d}", i + 1)
        s.suggest("x", 10)  # 预热 x 分支缓存
        refreshed = s.stats()["refreshed_nodes"]
        s.set("y000", 99999)          # 只动 y 分支
        s.suggest("x", 10)            # x 分支不应重建任何节点
        self.assertEqual(s.stats()["refreshed_nodes"], refreshed)

    def test_repeated_queries_do_not_refresh(self):
        s = TopKSuggester(base_bound=32)
        for i in range(300):
            s.set(f"z{i:03d}", i + 1)
        s.suggest("z", 10)
        refreshed = s.stats()["refreshed_nodes"]
        for k in (5, 10, 20):
            s.suggest("z", k)
        self.assertEqual(s.stats()["refreshed_nodes"], refreshed)


class LoadTextTest(unittest.TestCase):
    def test_synthetic_utf8_document(self):
        doc = "Apple 苹果 apple 苹果 APPLE，banana！苹果 café café 中文 中文 中文"
        s = TopKSuggester()
        s.load_text(doc)
        self.assertEqual(s.frequency("苹果"), 3)
        self.assertEqual(s.frequency("apple"), 3)
        self.assertEqual(s.frequency("café"), 2)
        self.assertEqual(s.frequency("中文"), 3)
        top = s.suggest("", 3)
        self.assertEqual({i.freq for i in top.items}, {3})


class RandomizedDifferentialTest(unittest.TestCase):
    def test_random_ops_match_full_sort(self):
        rng = random.Random(20260926)
        alphabet = "ab"
        vocab = set()
        while len(vocab) < 60:  # 小字母表制造大量前缀重叠
            n = rng.randint(1, 6)
            vocab.add("".join(rng.choice(alphabet) for _ in range(n)))
        vocab = sorted(vocab)

        s = TopKSuggester(base_bound=4)
        freq: dict[str, int] = {}
        for step in range(3000):
            op = rng.random()
            w = rng.choice(vocab)
            if op < 0.45:
                delta = rng.randint(1, 20)
                s.add(w, delta)
                freq[w] = freq.get(w, 0) + delta
            elif op < 0.75:
                val = rng.randint(1, 60)
                s.set(w, val)
                freq[w] = val
            else:
                if s.remove(w):
                    freq.pop(w, None)
            if step % 97 == 0:
                s.compact()
            if step % 7 == 0:
                prefix = "".join(rng.choice(alphabet) for _ in range(rng.randint(0, 3)))
                k = rng.randint(1, 12)
                got = [(i.normalized, i.freq) for i in s.suggest(prefix, k).items]
                self.assertEqual(got, ref_suggest(freq, prefix, k), f"step={step} prefix={prefix!r}")
        # 最终全量校验 + 分页链校验
        for prefix in ["", "a", "b", "ab", "ba"]:
            got = [(i.normalized, i.freq) for i in s.suggest(prefix, 200).items]
            self.assertEqual(got, ref_suggest(freq, prefix, 200))
            chained, cursor = [], None
            while True:
                page = s.suggest(prefix, 3, cursor=cursor)
                chained.extend((i.normalized, i.freq) for i in page.items)
                if not page.has_more:
                    break
                cursor = page.cursor
            self.assertEqual(chained, got)


if __name__ == "__main__":
    unittest.main(verbosity=2)
