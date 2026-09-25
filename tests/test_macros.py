"""宏展开与来源帧测试。"""
import unittest

from posmap.intervals import Span
from posmap.lexer import lex
from posmap.macros import (
    Frame,
    MacroDef,
    MacroError,
    PToken,
    expand_tokens,
    render,
)


def ptoks(program):
    return [
        PToken(t.text, t.kind, (Frame("user", Span("program", t.start, t.end)),))
        for t in lex(program)
    ]


class TestMacroDef(unittest.TestCase):
    def test_parse(self):
        d = MacroDef.parse("SQUARE(x) = x * x")
        self.assertEqual(d.name, "SQUARE")
        self.assertEqual(d.params, ("x",))
        self.assertEqual([t.text for t in d.body], ["x", "*", "x"])
        self.assertEqual(d.doc, "macro:SQUARE")
        self.assertEqual(d.text[d.def_span.start:d.def_span.end], "x * x")

    def test_bad_def(self):
        with self.assertRaises(MacroError):
            MacroDef.parse("F(x) =")


class TestExpand(unittest.TestCase):
    def test_one_to_many(self):
        macros = {"SQUARE": MacroDef.parse("SQUARE(x) = x * x")}
        src = "SQUARE(a)"
        out = expand_tokens(ptoks(src), macros)
        self.assertEqual([t.text for t in out], ["a", "*", "a"])
        a_span = Span("program", src.index("a"), src.index("a") + 1)
        # 一个实参片段 -> 多个展开片段，均指回同一源区间
        self.assertEqual(out[0].frames[0].span, a_span)
        self.assertEqual(out[2].frames[0].span, a_span)
        star = out[1].frames[0]
        self.assertEqual(star.kind, "macro_body")
        self.assertEqual(star.call_site, Span("program", 0, len(src)))
        self.assertEqual(star.def_span.doc, "macro:SQUARE")

    def test_nested_overlapping(self):
        macros = {
            "DOUBLE": MacroDef.parse("DOUBLE(x) = x + x"),
            "SQUARE": MacroDef.parse("SQUARE(x) = x * x"),
        }
        src = "DOUBLE(SQUARE(a))"
        out = expand_tokens(ptoks(src), macros)
        self.assertEqual("".join(t.text for t in out), "a*a+a*a")
        inner = Span("program", src.index("SQUARE"),
                     src.index("SQUARE") + len("SQUARE(a)"))
        outer = Span("program", 0, len(src))
        star = out[1].frames[0]
        self.assertEqual(star.kind, "macro_body")
        self.assertEqual(star.call_site, inner)
        self.assertTrue(outer.contains(inner))
        a_frames = out[0].frames
        self.assertEqual(a_frames[0].kind, "user")
        self.assertTrue(
            any(f.kind == "call_context" and f.macro == "SQUARE"
                for f in a_frames))
        self.assertTrue(
            any(f.kind == "call_context" and f.macro == "DOUBLE"
                for f in a_frames))

    def test_recursion_guard(self):
        macros = {"A": MacroDef.parse("A(x) = A(x)")}
        out = expand_tokens(ptoks("A(1)"), macros)
        self.assertEqual([t.text for t in out], ["A", "(", "1", ")"])

    def test_mutual_recursion_guard(self):
        macros = {
            "A": MacroDef.parse("A(x) = B(x)"),
            "B": MacroDef.parse("B(x) = A(x)"),
        }
        out = expand_tokens(ptoks("A(1)"), macros)
        self.assertEqual([t.text for t in out], ["A", "(", "1", ")"])

    def test_arity_mismatch(self):
        macros = {"F": MacroDef.parse("F(x, y) = x + y")}
        with self.assertRaises(MacroError):
            expand_tokens(ptoks("F(1)"), macros)

    def test_render_utf8_byte_offsets(self):
        toks = expand_tokens(ptoks("甲 + 1"), {})
        text, spans = render(toks)
        self.assertEqual(text, "甲 + 1")
        self.assertEqual(spans[0], Span("expanded", 0, 3))
        self.assertEqual(spans[1], Span("expanded", 4, 5))
        self.assertEqual(spans[2], Span("expanded", 6, 7))


if __name__ == "__main__":
    unittest.main()
