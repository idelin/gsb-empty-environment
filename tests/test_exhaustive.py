"""穷举交叉验证：主引擎（NFA+懒惰DFA） vs 独立回溯参考实现。

对小字母表上的所有短字符串逐一比对匹配区间与捕获；任何反例都可
用 repro.py 独立复现：python3.11 repro.py '<模式>' '<字母表>' '<文本>'
"""
import itertools
import random
import unittest

from regex_engine import Regex
from regex_engine.reference import ref_findall

CURATED = [
    ("a", "ab"), ("b", "ab"), ("ab", "ab"), ("a|b", "ab"), ("a*", "ab"),
    ("a*b*", "ab"), ("(a|b)*", "ab"), ("(a*)(b*)", "ab"), ("(a|ab)", "ab"),
    ("(ab|a)(b)", "ab"), ("(a|ab)(b?)", "ab"), ("(a|a)", "ab"),
    ("((a))", "ab"), ("()", "ab"), ("()*", "ab"), ("(a*)*", "ab"),
    ("((a*))*", "ab"), ("(a|)*", "ab"), ("(()*)*", "ab"), ("[]", "ab"),
    ("[]*", "ab"), ("a[]", "ab"), ("[]a", "ab"), ("a?", "ab"), ("a+", "ab"),
    ("(ab|a)*", "ab"), ("(a|b)*abb", "ab"), ("(a*)(a*)", "ab"),
    ("(a|b)*(a)(b)", "ab"), (".", "ab"), ("[^a]", "ab"), ("[a-b]", "ab"),
    ("[^]", "ab"), ("a**", "ab"), ("(a+)*", "ab"), ("(a?)*", "ab"),
    ("(a(b|c)*d)", "abcd"), ("x(a|b)*y", "xyab"), ("(a|b|c)*", "abc"),
    ("((a)|(b))*", "ab"), ("(a(b))*", "ab"), ("b(a|ab)*c", "abc"),
]


def all_strings(alphabet, maxlen):
    for n in range(maxlen + 1):
        for tup in itertools.product(sorted(alphabet), repeat=n):
            yield "".join(tup)


def engine_result(rx, text):
    res = rx.findall(text)
    assert not res.incomplete
    return [(m.start, m.end, m.captures) for m in res.matches]


def stream_result(rx, text, cuts):
    s = rx.stream()
    out = []
    prev = 0
    for cut in cuts:
        out += s.feed(text[prev:cut])
        prev = cut
    out += s.feed(text[prev:])
    out += s.finish()
    assert not s.incomplete
    return [(m.start, m.end, m.captures) for m in out]


def repro_cmd(pattern, alphabet, text):
    return (f"python3.11 repro.py '{pattern}' '{alphabet}' '{text}'")


class TestCuratedExhaustive(unittest.TestCase):
    def check(self, pattern, alphabet, maxlen):
        rx = Regex(pattern, alphabet)
        for text in all_strings(alphabet, maxlen):
            got = engine_result(rx, text)
            want = ref_findall(rx.ast, text, rx.alphabet)
            self.assertEqual(got, want,
                             f"反例复现: {repro_cmd(pattern, alphabet, text)}")

    def test_curated(self):
        for pattern, alphabet in CURATED:
            maxlen = 5 if len(alphabet) <= 2 else 4
            with self.subTest(pattern=pattern):
                self.check(pattern, alphabet, maxlen)


class TestRandomExhaustive(unittest.TestCase):
    """随机生成小表达式（渲染为合法源码再解析），穷举短串对拍。"""

    def random_ast_src(self, rng, alphabet, depth):
        if depth == 0:
            return rng.choice([rng.choice(alphabet), "()", "[]", "."])
        kind = rng.randrange(6)
        sub = lambda: self.random_ast_src(rng, alphabet, depth - 1)  # noqa: E731
        if kind == 0:
            return sub() + sub()
        if kind == 1:
            return "(" + sub() + "|" + sub() + ")"
        if kind == 2:
            return "(" + sub() + ")*"
        if kind == 3:
            return "(" + sub() + ")"
        if kind == 4:
            return "(" + sub() + ")?"
        return sub() + "(" + sub() + ")*"

    def test_random(self):
        rng = random.Random(20261001)
        alphabet = "ab"
        for trial in range(150):
            pattern = self.random_ast_src(rng, alphabet, depth=3)
            rx = Regex(pattern, alphabet)
            for text in all_strings(alphabet, 4):
                got = engine_result(rx, text)
                want = ref_findall(rx.ast, text, rx.alphabet)
                self.assertEqual(got, want,
                                 f"反例复现: {repro_cmd(pattern, alphabet, text)}")


class TestStreamMatchesOneShot(unittest.TestCase):
    def test_random_chunkings(self):
        rng = random.Random(7)
        cases = [("(a|b)*abb", "ab", "aababbab"), ("(a*)(b*)", "ab", "aabbb"),
                 ("a*", "ab", "aabaa"), ("(ab|a)(b?)", "ab", "abab"),
                 ("()", "ab", "ab"), ("(a|b)*(a)(b)", "ab", "abbaab")]
        for pattern, alphabet, text in cases:
            rx = Regex(pattern, alphabet)
            expected = engine_result(rx, text)
            for _ in range(10):
                cuts = sorted(rng.sample(range(len(text) + 1),
                                         rng.randrange(0, 4)))
                got = stream_result(rx, text, cuts)
                self.assertEqual(got, expected,
                                 f"反例复现: {repro_cmd(pattern, alphabet, text)} cuts={cuts}")


if __name__ == "__main__":
    unittest.main()
