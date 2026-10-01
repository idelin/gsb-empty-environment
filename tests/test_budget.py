import unittest

from regex_engine import BudgetExceeded, Regex


def triples(matches):
    return [(m.start, m.end, m.captures) for m in matches]


class TestBudget(unittest.TestCase):
    def test_budget_exceeded_marks_incomplete_not_wrong_answer(self):
        pat = "(a|b)*a(a|b)(a|b)(a|b)(a|b)(a|b)"  # DFA 状态数约 2^5
        text = "abbaabba"
        full = Regex(pat, "ab")
        self.assertFalse(full.findall(text).incomplete)
        expected = triples(full.findall(text).matches)

        tight = Regex(pat, "ab", max_states=4)
        res = tight.findall(text)
        self.assertTrue(res.incomplete)  # 标未完成
        # 已返回的部分必须是真实答案的前缀，绝不返回错误结果
        self.assertEqual(triples(res.matches), expected[:len(res.matches)])

    def test_search_raises_on_budget(self):
        rx = Regex("ab", "ab", max_states=1)
        with self.assertRaises(BudgetExceeded):
            rx.search("ab")

    def test_budget_covers_whole_run(self):
        # 预算是累计的：跨多次 findall 共享同一确定化器
        rx = Regex("a[bcd]e", "abcde", max_states=3)
        rx.findall("abe")
        res = rx.findall("ace")
        self.assertTrue(res.incomplete)

    def test_stream_marks_incomplete_and_stays_safe(self):
        pat = "(a|b)*a(a|b)(a|b)(a|b)"
        s = Regex(pat, "ab", max_states=3).stream()
        s.feed("abba")
        self.assertTrue(s.incomplete)
        self.assertEqual(s.feed("abba"), [])  # 不返回编造的结果
        self.assertEqual(s.finish(), [])

    def test_sufficient_budget_completes(self):
        rx = Regex("(a|b)*abb", "ab", max_states=64)
        res = rx.findall("aabbbabb")
        self.assertFalse(res.incomplete)
        self.assertEqual([(m.start, m.end) for m in res.matches], [(0, 8)])


class TestEvictionPreservesSemantics(unittest.TestCase):
    CORPUS = [
        ("(a|b)*abb", "ab", "aababbab"),
        ("(a*)(b|a)*", "ab", "aabbaab"),
        ("(ab|a)(b?)", "ab", "abab"),
        ("(x|y)*(x)(y)", "xy", "xyxxy"),
        ("a*", "ab", "aabaa"),
    ]

    def test_tiny_cache_same_results(self):
        for pat, alphabet, text in self.CORPUS:
            big = Regex(pat, alphabet, cache_size=100000)
            tiny = Regex(pat, alphabet, cache_size=1)  # 每次都驱逐
            self.assertEqual(triples(big.findall(text).matches),
                             triples(tiny.findall(text).matches), pat)

    def test_tiny_cache_same_results_streaming(self):
        for pat, alphabet, text in self.CORPUS:
            big = Regex(pat, alphabet, cache_size=100000).stream()
            tiny = Regex(pat, alphabet, cache_size=2).stream()
            for cut in range(0, len(text) + 1, 3):
                chunk = text[cut:cut + 3]
                self.assertEqual(triples(big.feed(chunk)),
                                 triples(tiny.feed(chunk)), (pat, cut))
            self.assertEqual(triples(big.finish()), triples(tiny.finish()))


if __name__ == "__main__":
    unittest.main()
