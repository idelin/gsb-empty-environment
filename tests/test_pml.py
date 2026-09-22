from __future__ import annotations

import unittest

from pml import parser
from pml.api import compile_source, run_source
from pml.errors import CompileError


def compile_error(source: str) -> str:
    with unittest.TestCase().assertRaises(CompileError) as caught:
        compile_source(source.encode("utf-8"), "<test>")
    return str(caught.exception)


class PatternLanguageTests(unittest.TestCase):
    def test_tagged_union_tuple_and_guard_execution(self):
        source = """
        union Color { Red, Green, Rgb(int, int, int) }
        let x: Color = Rgb(1, 2, 3)
        match x: Color {
          Rgb(r, g, b) | r == g -> (r + b, "equal"),
          Rgb(r, g, b) -> (r + g + b, "rgb"),
          Red -> 0,
          Green -> 1
        }
        """
        self.assertEqual(run_source(source.encode("utf-8")), (6, "rgb"))

    def test_failed_guard_continues_to_later_clause(self):
        source = """
        union N { One, Two }
        let x: N = Two
        match x: N {
          One | 1 < 0 -> "no",
          Two | false -> "guarded",
          Two -> "fallthrough",
          One -> "one"
        }
        """
        self.assertEqual(run_source(source.encode("utf-8")), "fallthrough")

    def test_wildcard_before_specific_clause_is_unreachable(self):
        source = """
        union E { A, B }
        let x: E = B
        match x: E {
          . -> "wildcard",
          A -> "a",
          B -> "b"
        }
        """
        message = compile_error(source)
        self.assertIn("unreachable match clause", message)
        self.assertIn('A -> "a"', message)
        self.assertIn('B -> "b"', message)

    def test_guarded_wildcard_does_not_make_later_specific_unreachable(self):
        source = """
        union E { A, B }
        let x: E = B
        match x: E {
          . | false -> "no",
          A -> "a",
          B -> "b"
        }
        """
        self.assertEqual(run_source(source.encode("utf-8")), "b")

    def test_only_guarded_constructor_does_not_cover_that_constructor(self):
        source = """
        union E { A, B }
        match (A): E {
          A | false -> "a guarded",
          B -> "b"
        }
        """
        message = compile_error(source)
        self.assertIn("match is not exhaustive", message)
        self.assertIn("A", message)

    def test_guarded_constructor_can_fall_through_to_wildcard(self):
        source = """
        union E { A, B }
        let x: E = A
        match x: E {
          A | false -> "guarded",
          B -> "b",
          . -> "fallback"
        }
        """
        self.assertEqual(run_source(source.encode("utf-8")), "fallback")

    def test_missing_constructor_is_nonexhaustive(self):
        source = """
        union E { A, B, C }
        match A: E {
          A -> "a",
          B -> "b"
        }
        """
        message = compile_error(source)
        self.assertIn("match is not exhaustive", message)
        self.assertIn("C", message)

    def test_literal_and_wildcard_exhaustiveness(self):
        source = """
        match 3: int {
          1 -> "one",
          2 | true -> "two guarded",
          . -> "other"
        }
        """
        self.assertEqual(run_source(source.encode("utf-8")), "other")

    def test_literal_guards_do_not_cover_unconditionally(self):
        source = """
        match 2: int {
          1 -> "one",
          2 | true -> "two"
        }
        """
        message = compile_error(source)
        self.assertIn("match is not exhaustive", message)

    def test_tuple_arity_and_nested_patterns_are_checked(self):
        source = """
        union E { A(int), B }
        match A(1): E {
          A((x, y)) -> x,
          B -> 0
        }
        """
        self.assertIn("expected int", compile_error(source))

    def test_duplicate_nested_bindings_are_rejected(self):
        source = """
        union E { P(int, int) }
        match P(1, 2): E {
          P(x, x) -> x
        }
        """
        message = compile_error(source)
        self.assertIn("duplicate binding x", message)

    def test_duplicate_bindings_inside_nested_tuple_are_rejected(self):
        source = """
        match (1, 2): (int, int) {
          (x, x) -> x
        }
        """
        self.assertIn("duplicate binding x", compile_error(source))

    def test_spans_are_utf8_byte_ranges(self):
        source = "let 值 = 1\n值 + 2\n"
        encoded = source.encode("utf-8")
        program = parser.parse(encoded, "<utf8>")
        variable = program.statements[1].left
        self.assertEqual(encoded[variable.span.start : variable.span.end], "值".encode("utf-8"))
        self.assertEqual(variable.span.end - variable.span.start, len("值".encode("utf-8")))
        self.assertGreater(variable.span.start, source.index("值"))

    def test_boolean_literal_patterns(self):
        source = """
        match false: bool {
          true -> 1,
          false -> 0
        }
        """
        self.assertEqual(run_source(source.encode("utf-8")), 0)

    def test_negative_integer_literal_pattern(self):
        source = """
        match -2: int {
          -1 -> 1,
          -2 -> 2,
          . -> 0
        }
        """
        self.assertEqual(run_source(source.encode("utf-8")), 2)

    def test_string_literal_patterns(self):
        source = """
        match "hi": string {
          "no" -> 0,
          "hi" -> 1,
          . -> 2
        }
        """
        self.assertEqual(run_source(source.encode("utf-8")), 1)


if __name__ == "__main__":
    unittest.main()
