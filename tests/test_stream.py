import unittest

from regex_engine import Regex


def triples(matches):
    return [(m.start, m.end, m.captures) for m in matches]


class TestStream(unittest.TestCase):
    def test_longest_match_waits_for_more_input(self):
        rx = Regex("a*", "ab")
        s = rx.stream()
        self.assertEqual(s.feed("aa"), [])   # 可能延长：等待
        self.assertEqual(s.feed("a"), [])    # 仍可能延长：等待
        got = s.feed("b")                    # 'b' 使 DFA 死亡：提交 aaa
        # 同时提交位置 3 的空匹配（该处尝试已死亡，无需等待）
        self.assertEqual([(m.start, m.end) for m in got], [(0, 3), (3, 3)])
        tail = s.finish()
        self.assertEqual([(m.start, m.end) for m in tail], [(4, 4)])

    def test_finish_flushes_pending_match(self):
        rx = Regex("ab+", "ab")
        s = rx.stream()
        self.assertEqual(s.feed("abb"), [])
        got = s.finish()
        self.assertEqual([(m.start, m.end) for m in got], [(0, 3)])

    def test_chunking_invariance(self):
        rx = Regex("(a|b)*(abb)", "ab")
        text = "aababbba"
        expected = triples(rx.findall(text).matches)
        # 所有两段切分
        for cut in range(len(text) + 1):
            s = rx.stream()
            got = s.feed(text[:cut]) + s.feed(text[cut:]) + s.finish()
            self.assertEqual(triples(got), expected, f"cut={cut}")
        # 逐字符喂入
        s = rx.stream()
        got = []
        for ch in text:
            got += s.feed(ch)
        got += s.finish()
        self.assertEqual(triples(got), expected)

    def test_recovery_between_matches(self):
        rx = Regex("(a)(b)", "ab")
        s = rx.stream()
        out = s.feed("abab") + s.feed("ab") + s.finish()
        self.assertEqual(triples(out),
                         [(0, 2, {1: (0, 1), 2: (1, 2)}),
                          (2, 4, {1: (2, 3), 2: (3, 4)}),
                          (4, 6, {1: (4, 5), 2: (5, 6)})])

    def test_absolute_positions_after_buffer_drop(self):
        rx = Regex("b", "ab")
        s = rx.stream()
        out = []
        for _ in range(5):
            out += s.feed("aab")
        out += s.finish()
        self.assertEqual([(m.start, m.end) for m in out],
                         [(2, 3), (5, 6), (8, 9), (11, 12), (14, 15)])

    def test_empty_match_at_stream_end(self):
        rx = Regex("()", "ab")
        s = rx.stream()
        out = s.feed("ab") + s.finish()
        self.assertEqual([(m.start, m.end) for m in out],
                         [(0, 0), (1, 1), (2, 2)])

    def test_feed_after_finish_rejected(self):
        s = Regex("a", "a").stream()
        s.finish()
        with self.assertRaises(ValueError):
            s.feed("a")

    def test_unknown_char_rejected(self):
        s = Regex("a", "ab").stream()
        with self.assertRaises(ValueError):
            s.feed("z")


if __name__ == "__main__":
    unittest.main()
