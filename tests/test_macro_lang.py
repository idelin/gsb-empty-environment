import unittest

from macro_lang import expand_source, render, run_source
from macro_lang.errors import (
    EvalError,
    ExpansionError,
    ExpansionLimitError,
    ParseError,
)
from macro_lang.lexer import tokenize


def run(src, **kw):
    lines = []
    values, _ = run_source(src, emit=lines.append, **kw)
    return values, lines


SWAP_SRC = """\
; 交换两个变量的宏(含非 ASCII 注释,验证 UTF-8 字节区间)
(defmacro swap! (a b)
  (let ((tmp a))
    (do (set a b) (set b tmp))))
(let ((x 1) (y 2) (tmp 99))
  (swap! x y)
  (print x y tmp))
"""


class LexerTest(unittest.TestCase):
    def test_utf8_byte_spans(self):
        src = '; 注释\n(print "héllo" tmp)'
        data = src.encode("utf-8")
        tokens = tokenize(src)
        tmp = next(t for t in tokens if t.text == "tmp")
        self.assertEqual(data[tmp.span.start : tmp.span.end], b"tmp")
        prefix = src[: src.index("tmp")]
        self.assertEqual(tmp.span.start, len(prefix.encode("utf-8")))
        string = next(t for t in tokens if t.kind == "string")
        self.assertEqual(data[string.span.start : string.span.end], '"héllo"'.encode("utf-8"))
        self.assertEqual(string.text, "héllo")

    def test_unexpected_character(self):
        with self.assertRaises(ParseError):
            tokenize("(+ 1 @)")


class ParserTest(unittest.TestCase):
    def test_nested_structure(self):
        from macro_lang.parser import parse

        nodes = parse("(+ 1 (* 2 3))")
        self.assertEqual(render(nodes[0]), "(+ 1 (* 2 3))")

    def test_unclosed_paren(self):
        from macro_lang.parser import parse

        with self.assertRaises(ParseError):
            parse("(+ 1")

    def test_float_and_negative(self):
        values, _ = run("(+ -1 2.5)")
        self.assertEqual(values[-1], 1.5)


class HygieneTest(unittest.TestCase):
    def test_caller_tmp_not_overwritten(self):
        values, lines = run(SWAP_SRC)
        self.assertEqual(lines, ["2 1 99"])

    def test_param_reference_not_renamed(self):
        src = """
        (defmacro plus-one (e) (let ((tmp 1000)) (+ e 1)))
        (let ((tmp 41)) (plus-one tmp))
        """
        values, _ = run(src)
        self.assertEqual(values[-1], 42)

    def test_macro_local_does_not_leak(self):
        src = """
        (defmacro m (x) (let ((tmp x)) tmp))
        (let ((tmp 5)) (m 9) tmp)
        """
        values, _ = run(src)
        self.assertEqual(values[-1], 5)

    def test_local_binding_renamed_in_expansion(self):
        expanded, _ = expand_source("(defmacro m (x) (let ((tmp x)) tmp))\n(m 7)")
        self.assertEqual(render(expanded[0]), "(let ((tmp#1 7)) tmp#1)")

    def test_caller_var_same_name_as_param(self):
        values, _ = run("(defmacro dbl (x) (+ x x))\n(let ((x 21)) (dbl x))")
        self.assertEqual(values[-1], 42)

    def test_let_shadows_param_inside_template(self):
        src = """
        (defmacro m (x) (let ((x 10)) (+ x x)))
        (m 1)
        """
        values, _ = run(src)
        self.assertEqual(values[-1], 20)


class LimitTest(unittest.TestCase):
    def test_depth_limit_reports_chain(self):
        src = "(defmacro boom (x) (boom (boom x)))\n(boom 1)"
        with self.assertRaises(ExpansionLimitError) as ctx:
            expand_source(src, max_depth=8)
        err = ctx.exception
        self.assertEqual(err.kind, "depth")
        self.assertGreaterEqual(len(err.chain), 8)
        msg = str(err)
        self.assertIn("expansion chain", msg)
        self.assertIn("boom", msg)
        self.assertIn("called at bytes", msg)
        self.assertIn("defined at bytes", msg)

    def test_node_limit(self):
        src = "(defmacro dup (x) (+ x x))\n(dup (dup (dup (dup (dup (dup (dup 1)))))))"
        values, _ = run(src)
        self.assertEqual(values[-1], 128)
        with self.assertRaises(ExpansionLimitError) as ctx:
            expand_source(src, max_nodes=50)
        self.assertEqual(ctx.exception.kind, "nodes")

    def test_arity_mismatch(self):
        with self.assertRaises(ExpansionError):
            expand_source("(defmacro m (a b) a)\n(m 1)")

    def test_malformed_defmacro(self):
        with self.assertRaises(ExpansionError):
            expand_source("(defmacro m a)")


class SourceMapTest(unittest.TestCase):
    def test_dual_mapping_def_and_call_spans(self):
        _, expander = expand_source(SWAP_SRC)
        data = SWAP_SRC.encode("utf-8")

        def slice_of(span):
            return data[span.start : span.end].decode("utf-8")

        self.assertTrue(expander.source_map)
        for entry in expander.source_map:
            self.assertEqual(entry.macro_name, "swap!")
            self.assertEqual(slice_of(entry.call_span), "(swap! x y)")
            self.assertTrue(
                slice_of(entry.def_span),
                "def_span must point into the macro definition",
            )
        tmp_entries = [e for e in expander.source_map if e.node_text.startswith("tmp#")]
        self.assertTrue(tmp_entries)
        self.assertEqual(slice_of(tmp_entries[0].def_span), "tmp")

    def test_argument_nodes_keep_call_site_origin(self):
        src = "(defmacro m (x) (+ x 1))\n(m foo)"
        expanded, _ = expand_source(src)
        call = expanded[0]
        arg = call.items[1]
        self.assertEqual(arg.origin.kind, "source")
        data = src.encode("utf-8")
        self.assertEqual(data[arg.span.start : arg.span.end], b"foo")


class InterpreterTest(unittest.TestCase):
    def test_arithmetic_and_if(self):
        values, _ = run("(if (> 3 2) (* 6 7) 0)")
        self.assertEqual(values[-1], 42)

    def test_while_loop_sum(self):
        src = """
        (let ((i 0) (acc 0))
          (while (< i 10)
            (set acc (+ acc i))
            (set i (+ i 1)))
          acc)
        """
        values, _ = run(src)
        self.assertEqual(values[-1], 45)

    def test_nested_macros(self):
        src = """
        (defmacro inc (x) (+ x 1))
        (defmacro inc2 (x) (inc (inc x)))
        (inc2 10)
        """
        values, _ = run(src)
        self.assertEqual(values[-1], 12)

    def test_unbound_variable(self):
        with self.assertRaises(EvalError):
            run("xyz")

    def test_string_print(self):
        _, lines = run('(print "a b" 1.5 true nil)')
        self.assertEqual(lines, ["a b 1.5 true nil"])

    def test_defmacro_not_allowed_nested(self):
        with self.assertRaises(EvalError):
            run("(let ((x 1)) (defmacro m (a) a))")


if __name__ == "__main__":
    unittest.main()
