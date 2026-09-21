"""macrolang 的 unittest 测试套件。

运行：python3.11 -m unittest discover -s tests -v
"""
from __future__ import annotations

import io
import unittest

from macrolang import (
    ArityError,
    DepthLimitExceeded,
    EvalError,
    Form,
    LexError,
    NodeLimitExceeded,
    ParseError,
    Sym,
    expand_source,
    lex,
    parse,
    run_source,
    walk,
)

SWAP_DEF = "(defmacro swap (a b) (let tmp a (seq (set a b) (set b tmp))))"


def run(src: str, **limits):
    return run_source(src, **limits)


def run_printed(src: str, **limits) -> str:
    out = io.StringIO()
    run_source(src, out=out, **limits)
    return out.getvalue()


class LexerTest(unittest.TestCase):
    def test_utf8_byte_spans(self):
        # “注释”二字各占 3 字节，后续 token 的字节区间必须跳过它们
        src = "; 注释\n(def x 1)"
        forms = parse(src)
        raw = src.encode("utf-8")
        span = forms[0].span
        self.assertEqual(raw[span.start : span.end].decode("utf-8"), "(def x 1)")
        # 中文注释共 6 字节 + ';' + ' ' + '\n' = 9 字节
        self.assertEqual(span.start, 9)

    def test_string_token_span_multibyte(self):
        src = '(print "héllo")'
        tokens = lex(src)
        string_tok = next(t for t in tokens if t.kind == "string")
        raw = src.encode("utf-8")
        self.assertEqual(
            raw[string_tok.span.start : string_tok.span.end].decode("utf-8"),
            '"héllo"',
        )

    def test_hash_is_rejected_in_source(self):
        # '#' 保留给卫生改名，用户不可书写，保证新名字不可伪造
        with self.assertRaises(LexError):
            lex("(def tmp#1 0)")

    def test_unterminated_string(self):
        with self.assertRaises(LexError):
            lex('"abc')


class ParserTest(unittest.TestCase):
    def test_unclosed_paren(self):
        with self.assertRaises(ParseError):
            parse("(def x 1")

    def test_unexpected_rparen(self):
        with self.assertRaises(ParseError):
            parse(")")


class InterpreterTest(unittest.TestCase):
    def test_arithmetic_and_if(self):
        self.assertEqual(run("(+ 1 2 3)"), 6)
        self.assertEqual(run("(- 10 3 2)"), 5)
        self.assertEqual(run("(- 5)"), -5)
        self.assertEqual(run("(* 2 3 4)"), 24)
        self.assertEqual(run("(/ 7 2)"), 3)
        self.assertEqual(run("(% 7 3)"), 1)
        self.assertEqual(run("(if (< 1 2) 10 20)"), 10)
        self.assertEqual(run("(if (= 1 2) 10 20)"), 20)

    def test_while_loop(self):
        src = """
        (def i 0)
        (def acc 0)
        (while (< i 5) (seq (set acc (+ acc i)) (set i (+ i 1))))
        acc
        """
        self.assertEqual(run(src), 10)

    def test_let_scoping_does_not_leak(self):
        src = "(def x 1) (let x 5 x) x"
        self.assertEqual(run(src), 1)

    def test_unbound_variable(self):
        with self.assertRaises(EvalError):
            run("nope")

    def test_set_unbound_variable(self):
        with self.assertRaises(EvalError):
            run("(set nope 1)")

    def test_print_output(self):
        printed = run_printed('(print "héllo" 42)')
        self.assertEqual(printed, "héllo 42\n")


class HygieneTest(unittest.TestCase):
    def test_caller_tmp_not_clobbered_by_macro_tmp(self):
        # 宏内 let 绑定的 tmp 必须被卫生改名，调用方 tmp 保持 100
        src = f"""
        (def tmp 100)
        (def x 1)
        (def y 2)
        {SWAP_DEF}
        (swap x y)
        tmp
        """
        self.assertEqual(run(src), 100)

    def test_swap_actually_swaps(self):
        src = f"""
        (def x 1)
        (def y 2)
        {SWAP_DEF}
        (swap x y)
        (+ (* x 10) y)
        """
        self.assertEqual(run(src), 21)  # x=2, y=1

    def test_argument_named_tmp_is_not_renamed(self):
        # 实参 tmp 指调用方变量；宏内 tmp 改名不得波及它
        src = f"""
        (def tmp 10)
        (def y 20)
        {SWAP_DEF}
        (swap tmp y)
        (+ (* tmp 100) y)
        """
        self.assertEqual(run(src), 2010)  # tmp=20, y=10

    def test_macro_binder_gets_fresh_unforgeable_name(self):
        src = f"(def x 1) (def y 2) {SWAP_DEF} (swap x y)"
        forms, _ = expand_source(src)
        let_form = forms[-1]
        self.assertIsInstance(let_form, Form)
        binder = let_form.items[1]
        self.assertIsInstance(binder, Sym)
        self.assertTrue(binder.name.startswith("tmp#"))
        self.assertNotEqual(binder.name, "tmp")

    def test_nested_lets_in_macro_get_distinct_names(self):
        src = """
        (defmacro two (a b)
          (let t a (let u b (+ t u))))
        (def t 100)
        (def u 200)
        (def r (two 1 2))
        (+ (* r 1000) (+ t u))
        """
        self.assertEqual(run(src), 3300)  # r=3, t/u 未被捕获

    def test_macro_generating_macro_call(self):
        src = """
        (defmacro inc (x) (+ x 1))
        (defmacro inc2 (x) (inc (inc x)))
        (def a 40)
        (inc2 a)
        """
        self.assertEqual(run(src), 42)


class SourceMapTest(unittest.TestCase):
    def test_dual_call_and_def_mapping(self):
        src = (
            SWAP_DEF
            + "\n(def x 1)\n(def y 2)\n(swap x y)"
        )
        raw = src.encode("utf-8")
        forms, _ = expand_source(src)
        let_form = forms[-1]
        self.assertTrue(let_form.provenance, "展开节点应带有源码映射")
        record = let_form.provenance[-1]
        self.assertEqual(record.macro_name, "swap")
        call_text = raw[record.call_span.start : record.call_span.end].decode()
        def_text = raw[record.def_span.start : record.def_span.end].decode()
        self.assertEqual(call_text, "(swap x y)")
        self.assertEqual(def_text, "(let tmp a (seq (set a b) (set b tmp)))")

    def test_nested_expansion_records_both_layers(self):
        src = (
            "(defmacro inc (x) (+ x 1))\n"
            "(defmacro inc2 (x) (inc (inc x)))\n"
            "(def a 0)\n"
            "(inc2 a)"
        )
        forms, _ = expand_source(src)
        last = forms[-1]
        # 最内层节点应依次记录 inc2 与 inc 两层展开
        deepest = max(
            (n for n in walk(last) if n.provenance),
            key=lambda n: len(n.provenance),
        )
        layers = [r.macro_name for r in deepest.provenance]
        self.assertEqual(layers, ["inc2", "inc"])
        raw = src.encode("utf-8")
        outer = deepest.provenance[0]
        self.assertEqual(
            raw[outer.call_span.start : outer.call_span.end].decode(), "(inc2 a)"
        )

    def test_argument_nodes_keep_caller_span(self):
        src = f"(def tmp 10)\n(def y 20)\n{SWAP_DEF}\n(swap tmp y)"
        raw = src.encode("utf-8")
        forms, _ = expand_source(src)
        let_form = forms[-1]
        # 展开后 (set a b) 中 a 被替换为调用方的 tmp，位置仍指向调用处
        seq_form = let_form.items[3]
        set_form = seq_form.items[1]
        arg_node = set_form.items[1]
        self.assertIsInstance(arg_node, Sym)
        self.assertEqual(arg_node.name, "tmp")
        self.assertEqual(
            raw[arg_node.span.start : arg_node.span.end].decode(), "tmp"
        )


class LimitTest(unittest.TestCase):
    def test_depth_limit_reports_expansion_chain(self):
        src = "(defmacro inf (x) (inf x))\n(inf 1)"
        with self.assertRaises(DepthLimitExceeded) as ctx:
            run(src, max_depth=8)
        message = str(ctx.exception)
        self.assertIn("depth limit (8)", message)
        self.assertIn("Expansion chain", message)
        self.assertIn("inf called at bytes", message)
        # 链长 = 已展开的 8 层 + 触发超限的第 9 层
        self.assertEqual(len(ctx.exception.chain), 9)
        names = {name for name, _ in ctx.exception.chain}
        self.assertEqual(names, {"inf"})

    def test_node_limit(self):
        # dup 每展开一次节点数翻倍，必然突破节点预算
        depth = 20
        call = "1"
        for _ in range(depth):
            call = f"(dup {call})"
        src = f"(defmacro dup (x) (+ x x))\n{call}"
        with self.assertRaises(NodeLimitExceeded) as ctx:
            run(src, max_depth=100, max_nodes=500)
        self.assertIn("node limit (500)", str(ctx.exception))

    def test_arity_error(self):
        src = f"{SWAP_DEF}\n(def x 1)\n(swap x)"
        with self.assertRaises(ArityError) as ctx:
            run(src)
        self.assertIn("expects 2 argument(s), got 1", str(ctx.exception))

    def test_depth_limit_zero_disallows_any_macro(self):
        src = "(defmacro m () 1)\n(m)"
        with self.assertRaises(DepthLimitExceeded):
            run(src, max_depth=0)


if __name__ == "__main__":
    unittest.main()
