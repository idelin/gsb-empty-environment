import unittest

from regex_engine import ParseError, Parser
from regex_engine.ast_nodes import (Alt, CharClass, Concat, EmptyLang,
                                    Epsilon, Group, Literal, Star)


def parse(src):
    return Parser(src).parse()


class TestParser(unittest.TestCase):
    def test_source_spans(self):
        ast = parse("(a|b)*c")
        self.assertIsInstance(ast, Concat)
        star, lit = ast.parts
        self.assertIsInstance(star, Star)
        self.assertEqual((star.span.start, star.span.end), (0, 6))
        group = star.body
        self.assertIsInstance(group, Group)
        self.assertEqual((group.span.start, group.span.end), (0, 5))
        alt = group.body
        self.assertIsInstance(alt, Alt)
        self.assertEqual((alt.span.start, alt.span.end), (1, 4))
        self.assertEqual((lit.span.start, lit.span.end), (6, 7))

    def test_empty_string_vs_empty_language(self):
        self.assertIsInstance(parse("()"), Epsilon)      # 空串
        self.assertIsInstance(parse("[]"), EmptyLang)    # 空语言
        self.assertIsInstance(parse("a()"), Concat)
        star = parse("[]*")
        self.assertIsInstance(star, Star)
        self.assertIsInstance(star.body, EmptyLang)

    def test_char_classes(self):
        cls = parse("[a-cx]")
        self.assertIsInstance(cls, CharClass)
        self.assertEqual(cls.chars, frozenset("abcx"))
        self.assertFalse(cls.negated)
        neg = parse("[^a]")
        self.assertTrue(neg.negated)
        self.assertEqual(neg.chars, frozenset("a"))
        anyc = parse("[^]")
        self.assertTrue(anyc.negated)
        self.assertEqual(anyc.chars, frozenset())
        dot = parse(".")
        self.assertTrue(dot.negated)
        self.assertEqual(dot.chars, frozenset())
        dash = parse("[a-]")
        self.assertEqual(dash.chars, frozenset("a-"))

    def test_escapes(self):
        self.assertEqual(parse(r"\n").char, "\n")
        self.assertEqual(parse(r"\*").char, "*")
        self.assertEqual(parse(r"[\\\]]").chars, frozenset("\\]"))

    def test_group_numbering(self):
        ast = parse("(a)((b)|c)")
        self.assertEqual(ast.parts[0].index, 1)
        self.assertEqual(ast.parts[1].index, 2)
        self.assertEqual(ast.parts[1].body.branches[0].index, 3)

    def test_repeat_desugar(self):
        plus = parse("a+")
        self.assertIsInstance(plus, Concat)
        self.assertIsInstance(plus.parts[1], Star)
        quest = parse("a?")
        self.assertIsInstance(quest, Alt)
        nested = parse("a**")
        self.assertIsInstance(nested, Star)
        self.assertIsInstance(nested.body, Star)

    def test_errors_report_position(self):
        cases = ["(a", "*a", "[a", "a\\", "[z-a]", "a)"]
        for src in cases:
            with self.assertRaises(ParseError) as ctx:
                parse(src)
            self.assertIsInstance(ctx.exception.pos, int)
        # 空交替分支是合法的：a| 等价于 (a|)
        self.assertIsInstance(parse("a|"), Alt)


if __name__ == "__main__":
    unittest.main()
