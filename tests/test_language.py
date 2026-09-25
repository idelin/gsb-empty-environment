import unittest

from constkit import Engine, EnumVal
from constkit.lexer import lex
from constkit.parser import parse_module


def codes(diags, module=None, code=None):
    return [d for d in diags
            if (module is None or d.module == module)
            and (code is None or d.code == code)]


class SpanTests(unittest.TestCase):
    def test_spans_are_utf8_byte_offsets(self):
        src = "module M;\n# 中文注释\nconst A = 1;\n"
        raw = src.encode("utf-8")
        self.assertNotEqual(len(raw), len(src))  # multibyte content present
        diags = []
        tokens = lex(src, "M", diags)
        self.assertEqual(diags, [])
        a_tok = next(t for t in tokens if t.text == "A")
        self.assertEqual(raw[a_tok.span.start:a_tok.span.end], b"A")
        self.assertEqual(a_tok.span.start, raw.index(b"A"))
        one_tok = next(t for t in tokens if t.kind == "int")
        self.assertEqual(raw[one_tok.span.start:one_tok.span.end], b"1")
        self.assertGreater(one_tok.span.start, raw.index("中文".encode()))

    def test_diagnostic_span_points_at_source_bytes(self):
        src = "module M;\nconst Z = 0;\nconst Y = Z;\nconst X = 1 / Y;\n"
        raw = src.encode("utf-8")
        e = Engine()
        e.apply(sources={"M": src})
        d = next(d for d in e.diagnostics if d.code == "div-by-zero")
        self.assertEqual(raw[d.span.start:d.span.end], b"1 / Y")


class ParsingTests(unittest.TestCase):
    def test_precedence_and_unary_and_modulo(self):
        e = Engine()
        e.apply(sources={"M": "module M;\n"
                              "const a = 1 + 2 * 3;\n"
                              "const b = (1 + 2) * 3;\n"
                              "const c = -7 % 3;\n"})
        self.assertEqual(e.artifact("M", "a"), 7)
        self.assertEqual(e.artifact("M", "b"), 9)
        self.assertEqual(e.artifact("M", "c"), 2)  # floor semantics

    def test_forward_reference(self):
        e = Engine()
        e.apply(sources={"M": "module M;\nconst A = B + 1;\nconst B = 2;\n"})
        self.assertEqual(e.artifact("M", "A"), 3)

    def test_parse_error_is_diagnostic(self):
        e = Engine()
        r = e.apply(sources={"M": "module M;\nconst A = ;\nconst B = 1;\n"})
        self.assertTrue(any(d.code == "parse-error" for d in r.diagnostics))
        self.assertEqual(e.artifact("M", "B"), 1)


class EnumTests(unittest.TestCase):
    SRC = ("module M;\n"
           "enum Color { Red, Green = 5, Blue }\n"
           "const c = Color.Blue;\n"
           "const d = c + 1;\n"
           "const r = Color.Red;\n")

    def test_enum_values_and_arithmetic(self):
        e = Engine()
        e.apply(sources={"M": self.SRC})
        self.assertEqual(e.diagnostics, [])
        self.assertEqual(e.artifact("M", "c"), EnumVal("Color", "Blue", 6))
        self.assertEqual(e.artifact("M", "d"), 7)
        self.assertEqual(e.artifact("M", "r"), EnumVal("Color", "Red", 0))

    def test_cross_module_enum_reference(self):
        e = Engine()
        e.apply(sources={
            "M": "module M;\nenum E { A, B = 10 }\n",
            "N": "module N;\nconst x = M.E.B;\n",
        })
        self.assertEqual(e.artifact("N", "x"), EnumVal("E", "B", 10))


class ArrayTests(unittest.TestCase):
    SRC = ("module M;\n"
           "const a = [1, 2, 3];\n"
           "const i = 1;\n"
           "const b = a[i];\n"
           "const c = a[2] + a[0];\n"
           "const bad = a[9];\n")

    def test_indexing_and_out_of_range(self):
        e = Engine()
        e.apply(sources={"M": self.SRC})
        self.assertEqual(e.artifact("M", "b"), 2)
        self.assertEqual(e.artifact("M", "c"), 4)
        d = next(d for d in e.diagnostics if d.code == "index-out-of-range")
        self.assertIn("9", d.message)
        with self.assertRaises(KeyError):
            e.artifact("M", "bad")


class OverflowTests(unittest.TestCase):
    MAX = 9223372036854775807

    def test_exact_mode_allows_arbitrary_precision(self):
        e = Engine(overflow="exact")
        big = str(1 << 100)
        e.apply(sources={"M": f"module M;\nconst X = {big};\n"})
        self.assertEqual(e.artifact("M", "X"), 1 << 100)

    def test_wrap_mode_is_signed_64bit(self):
        e = Engine(overflow="wrap", bits=64)
        e.apply(sources={
            "M": f"module M;\nconst X = {self.MAX} + 1;\nconst Y = 0 - {2 ** 63} - 1;\n"
        })
        self.assertEqual(e.artifact("M", "X"), -2 ** 63)
        self.assertEqual(e.artifact("M", "Y"), 2 ** 63 - 1)

    def test_error_mode_reports_and_keeps_last_good(self):
        e = Engine(overflow="error", bits=64)
        e.apply(sources={"M": "module M;\nconst X = 5;\n"})
        e.apply(sources={"M": f"module M;\nconst X = {self.MAX} + 1;\n"})
        self.assertEqual(e.artifact("M", "X"), 5)  # last usable artifact kept
        self.assertTrue(any(d.code == "overflow" for d in e.diagnostics))

    def test_policy_change_invalidates_fingerprint(self):
        e = Engine(overflow="exact")
        e.apply(sources={"M": f"module M;\nconst X = {self.MAX};\n"})
        self.assertEqual(e.eval_count, 1)
        e.overflow = "wrap"
        e.apply(sources={"M": f"module M;\nconst X = {self.MAX};\n"})
        self.assertIn("M.X", e.last_evaluated)


class DivisionByZeroTests(unittest.TestCase):
    def test_zero_source_chain_and_dependent_diagnostic(self):
        src = ("module M;\n"
               "const Z = 0;\n"
               "const Y = Z;\n"
               "const X = 1 / Y;\n"
               "const W = X + 1;\n")
        e = Engine()
        e.apply(sources={"M": src})
        d = next(d for d in e.diagnostics if d.code == "div-by-zero")
        labels = [label for label, _ in d.chain]
        self.assertTrue(any("M.Y" in label for label in labels))
        self.assertTrue(any("M.Z" in label for label in labels))
        for _, span in d.chain:
            self.assertIsNotNone(span)
        w = next(d for d in e.diagnostics
                 if d.code == "dependency-failed" and "M.X" in d.message)
        self.assertTrue(any("M.X" in label for label, _ in w.chain))
        # good constants next to the failure still produce artifacts
        self.assertEqual(e.artifact("M", "Y"), 0)
        self.assertEqual(e.artifact("M", "Z"), 0)

    def test_modulo_zero(self):
        e = Engine()
        e.apply(sources={"M": "module M;\nconst Z = 0;\nconst X = 5 % Z;\n"})
        self.assertTrue(any(d.code == "div-by-zero" for d in e.diagnostics))


if __name__ == "__main__":
    unittest.main()
