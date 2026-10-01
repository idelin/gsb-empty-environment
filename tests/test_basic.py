import unittest

from regex_engine import CompileError, Regex
from regex_engine.ast_nodes import EmptyLang, Epsilon


def found(rx, text):
    return [(m.start, m.end, m.captures) for m in rx.findall(text).matches]


class TestEmptyVsEmptyLanguage(unittest.TestCase):
    def test_empty_string_matches_everywhere(self):
        rx = Regex("()", "ab")
        self.assertEqual([(m.start, m.end) for m in rx.findall("ab").matches],
                         [(0, 0), (1, 1), (2, 2)])

    def test_empty_language_never_matches(self):
        rx = Regex("[]", "ab")
        self.assertIsNone(rx.search(""))
        self.assertIsNone(rx.search("ab"))
        self.assertEqual(rx.findall("ab").matches, [])

    def test_ast_distinguishes_them(self):
        self.assertIsInstance(Regex("()", "ab").ast, Epsilon)
        self.assertIsInstance(Regex("[]", "ab").ast, EmptyLang)

    def test_star_of_empty_language_is_empty_string(self):
        rx = Regex("[]*", "ab")
        m = rx.search("")
        self.assertIsNotNone(m)
        self.assertEqual((m.start, m.end), (0, 0))

    def test_concat_with_empty_language_is_empty(self):
        self.assertEqual(found(Regex("a[]", "ab"), "aaa"), [])
        self.assertEqual(found(Regex("[]a", "ab"), "aaa"), [])


class TestNestedStarTerminates(unittest.TestCase):
    PATTERNS = ["(a*)*", "((a*))*", "(a|)*", "(()*)*", "(a*)*b", "a**",
                "(a+)*", "(a?)*", "(()|a*)*"]

    def test_terminates_and_matches(self):
        for pat in self.PATTERNS:
            rx = Regex(pat, "ab")
            for text in ["", "a", "aa", "b", "aab", "abab"]:
                res = rx.findall(text)  # 若死循环本测试将超时失败
                self.assertFalse(res.incomplete, (pat, text))

    def test_nested_star_empty_match(self):
        m = Regex("(a*)*", "ab").search("")
        self.assertEqual((m.start, m.end), (0, 0))
        # 首次空迭代允许：组 1 匹配一次空串
        self.assertEqual(m.captures, {1: (0, 0)})


class TestPriorityAndCaptures(unittest.TestCase):
    def test_leftmost_longest(self):
        m = Regex("a|ab", "ab").search("ab")
        self.assertEqual((m.start, m.end), (0, 2))  # 最长优先于分支顺序

    def test_overlapping_branches_first_wins_on_tie(self):
        m = Regex("(a|a)", "a").search("a")
        self.assertEqual(m.captures[1], (0, 1))

    def test_priority_among_longest(self):
        # 两条路径都达到最长终点 2：先写分支 (a)(b?) 的捕获生效
        m = Regex("(a|ab)(b?)", "ab").search("ab")
        self.assertEqual((m.start, m.end), (0, 2))
        self.assertEqual(m.captures[1], (0, 1))
        self.assertEqual(m.captures[2], (1, 2))

    def test_branch_order_backtrack(self):
        m = Regex("(ab|a)(b)", "ab").search("ab")
        self.assertEqual(m.captures[1], (0, 1))  # ab 分支失败后回退到 a
        self.assertEqual(m.captures[2], (1, 2))

    def test_greedy_star_capture(self):
        m = Regex("(a*)(a*)", "a").search("aa")
        self.assertEqual(m.captures[1], (0, 2))
        self.assertEqual(m.captures[2], (2, 2))

    def test_nested_group_boundaries(self):
        m = Regex("(a(b)c)", "abc").search("abc")
        self.assertEqual(m.captures[1], (0, 3))
        self.assertEqual(m.captures[2], (1, 2))

    def test_repeated_group_last_iteration_wins(self):
        m = Regex("(a)+", "a").search("aaa")
        self.assertEqual(m.captures[1], (2, 3))

    def test_optional_group_unmatched_is_none(self):
        m = Regex("(a)|(b)", "ab").search("a")
        self.assertEqual(m.captures[1], (0, 1))
        self.assertIsNone(m.captures[2])

    def test_tags_survive_dfa_state_merging(self):
        # (x|y)*(x)(y)：DFA 把多条 NFA 路径合并进同一状态集合，
        # 捕获仍须从 NFA 路径正确恢复
        rx = Regex("(x|y)*(x)(y)", "xy")
        det = rx._determinizer()
        for ch in "xxy":
            det.step(det.start_id, ch)
        self.assertTrue(any(len(s) > 1 for s in det._sets),
                        "前提：确实存在合并了多个 NFA 状态的 DFA 状态")
        m = rx.search("xxy")
        self.assertEqual((m.start, m.end), (0, 3))
        self.assertEqual(m.captures[1], (0, 1))  # 星号组取最后一次迭代
        self.assertEqual(m.captures[2], (1, 2))
        self.assertEqual(m.captures[3], (2, 3))

    def test_findall_leftmost_longest(self):
        rx = Regex("a*", "ab")
        self.assertEqual([(m.start, m.end) for m in rx.findall("aaab").matches],
                         [(0, 3), (3, 3), (4, 4)])

    def test_alphabet_is_explicit(self):
        with self.assertRaises(CompileError):
            Regex("abc", "ab")
        with self.assertRaises(CompileError):
            Regex("[a-z]", "ab")
        rx = Regex("a", "ab")
        with self.assertRaises(ValueError):
            rx.search("ax")
        self.assertEqual(Regex("[^a]", "ab").search("b").span(), (0, 1))
        self.assertIsNone(Regex("[^a]", "ab").search("a"))


if __name__ == "__main__":
    unittest.main()
