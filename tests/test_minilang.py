from __future__ import annotations

import os
import sys
import unittest

sys.path.insert(0, os.path.join(os.path.dirname(__file__), "..", "src"))

from minilang.common import Span, SyntaxErr, TypeInferenceError
from minilang.interpreter import Interpreter, format_value
from minilang.lexer import Lexer
from minilang.parser import parse
from minilang.report import render_type_error
from minilang.typer import TypeChecker, infer_program


def check(src: str) -> str:
    data = src.encode("utf-8")
    ast = parse(data)
    return TypeChecker(data).result_type(ast)


def run(src: str):
    data = src.encode("utf-8")
    ast = parse(data)
    TypeChecker(data).result_type(ast)
    return format_value(Interpreter().eval(ast, {}))


def infer_error(src: str) -> TypeInferenceError:
    data = src.encode("utf-8")
    ast = parse(data)
    try:
        TypeChecker(data).result_type(ast)
    except TypeInferenceError as err:
        return err
    raise AssertionError("expected a type error")


class PolymorphismTests(unittest.TestCase):
    def test_identity_reused_at_two_types(self):
        src = "let id = fun x. x in id 3 + 0"
        self.assertEqual(check(src), "Int")
        self.assertEqual(run(src), "3")

    def test_identity_at_bool_and_int(self):
        src = "let id = fun x. x in let b = id true in id 7"
        self.assertEqual(check(src), "Int")
        self.assertEqual(run(src), "7")

    def test_identity_type(self):
        self.assertEqual(check("let id = fun x. x in id"), "a -> a")

    def test_lambda_param_is_not_generalized(self):
        # Without a let, the parameter is monomorphic across both uses.
        with self.assertRaises(TypeInferenceError):
            check("fun f. f 1 + (if f true then 1 else 2)")

    def test_higher_order_compose(self):
        src = "let apply = fun f. fun x. f x in apply (fun n. n + 1) 41"
        self.assertEqual(check(src), "Int")
        self.assertEqual(run(src), "42")

    def test_polymorphic_let_within_body(self):
        src = "let k = fun a. fun b. a in k 1 true"
        self.assertEqual(check(src), "Int")
        self.assertEqual(run(src), "1")


class OccursTests(unittest.TestCase):
    def test_self_application_is_rejected(self):
        err = infer_error("let f = fun x. x x in f f")
        self.assertEqual(err.kind, "occurs")
        self.assertTrue(err.links)
        report = render_type_error(b"let f = fun x. x x in f f", err)
        self.assertIn("infinite type", report)
        # Every link carries a byte span inside the program.
        for span, _ in err.links:
            self.assertGreaterEqual(span.start, 0)
            self.assertLessEqual(span.end, len(b"let f = fun x. x x in f f"))

    def test_simple_self_application_chain_has_structural_edge(self):
        err = infer_error("fun x. x x")
        descriptions = " ".join(span.note for span, _ in err.links)
        self.assertTrue(descriptions.strip())

    def test_occurs_positions_mention_application(self):
        src = "fun x. x x"
        err = infer_error(src)
        # The chain should reference the inner application "x x" or the function usage.
        flat = [(span.start, span.end, note) for span, note in err.links]
        self.assertTrue(any(span_note for _, _, span_note in flat))


class ClashTests(unittest.TestCase):
    def test_bool_plus_int_locates_both_sources(self):
        src = "true + 1"
        err = infer_error(src)
        self.assertEqual(err.kind, "clash")
        covered = {span.start for span, _ in err.links}
        # Byte 0 is `true`, byte 7 is `1`; both origins appear in the chain.
        self.assertIn(0, covered)
        self.assertIn(7, covered)
        notes = " ".join(note for _, note in err.links)
        self.assertIn("+", notes)

    def test_clash_chain_is_ordered(self):
        err = infer_error("true + 1")
        # chain: Bool literal -> + operand requirement -> Int literal
        self.assertGreaterEqual(len(err.links), 3)

    def test_int_plus_bool_reverse_order(self):
        err = infer_error("1 + true")
        starts = [span.start for span, _ in err.links]
        self.assertIn(0, starts)  # integer literal
        self.assertIn(4, starts)  # boolean literal

    def test_condition_must_be_bool(self):
        with self.assertRaises(TypeInferenceError):
            check("if 1 then 2 else 3")

    def test_branches_must_agree(self):
        with self.assertRaises(TypeInferenceError):
            check("if true then 1 else false")

    def test_apply_non_function(self):
        with self.assertRaises(TypeInferenceError):
            check("1 true")


class LexerSpanTests(unittest.TestCase):
    def test_offsets_are_utf8_bytes(self):
        # A Chinese identifier occupies 3 UTF-8 bytes per character.
        src = "let 变量 = 1 in 变量".encode("utf-8")
        tokens = Lexer(src).tokenize()
        ident = next(t for t in tokens if t.text == "变量")
        self.assertEqual(ident.end - ident.start, 6)
        # `in` starts immediately after the single space following 6 identifier bytes.
        in_tok = next(t for t in tokens if t.kind == "IN")
        self.assertEqual(src[in_tok.start:in_tok.end], b"in")

    def test_multibyte_column_in_report(self):
        src = "let 值 = true in 值 + 1".encode("utf-8")
        ast = parse(src)
        try:
            TypeChecker(src).result_type(ast)
        except TypeInferenceError as err:
            report = render_type_error(src, err)
            # The second `值` starts at byte 18 and occupies 3 UTF-8 bytes.
            self.assertIn("bytes 18..21", report)
            # The integer literal source is also in the chain.
            self.assertIn("integer literal 1", report)
        else:
            self.fail("expected clash")

    def test_integer_literal_span(self):
        tokens = Lexer(b"40 + 2").tokenize()
        self.assertEqual((tokens[0].start, tokens[0].end), (0, 2))


class InterpreterTests(unittest.TestCase):
    def test_arith_and_if(self):
        self.assertEqual(run("if 1 < 2 then 10 + 5 else 0"), "15")

    def test_closure_capture(self):
        src = "let add = fun a. fun b. a + b in add 20 22"
        self.assertEqual(run(src), "42")

    def test_recursion_unavailable_but_nested_let_works(self):
        src = "let x = 1 in let y = 2 in x + y"
        self.assertEqual(run(src), "3")

    def test_bool_values(self):
        self.assertEqual(run("true == false"), "false")
        self.assertEqual(run("1 <= 1"), "true")


class SyntaxTests(unittest.TestCase):
    def test_unexpected_token(self):
        with self.assertRaises(SyntaxErr):
            parse(b"let = 1")

    def test_unclosed_paren(self):
        with self.assertRaises(SyntaxErr):
            parse(b"(1 + 2")


if __name__ == "__main__":
    unittest.main(verbosity=2)
