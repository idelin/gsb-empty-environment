import unittest

from pmatch import MatchError, compile_source, run_source
from pmatch.lexer import lex


def diags_of(src):
    _, _, diags = compile_source(src)
    return diags


def errors(src):
    return [d for d in diags_of(src) if d.severity == "error"]


def warnings(src):
    return [d for d in diags_of(src) if d.severity == "warning"]


class LexerTest(unittest.TestCase):
    def test_ascii_spans(self):
        toks = lex("match 1:")
        self.assertEqual(toks[0].span, (0, 5))
        self.assertEqual(toks[1].span, (6, 7))
        self.assertEqual(toks[2].span, (7, 8))

    def test_utf8_byte_spans(self):
        # "é" is 2 UTF-8 bytes: " h é l l o " -> 1+1+2+1+1+1+1 = 8 bytes
        toks = lex('"héllo"')
        self.assertEqual(toks[0].kind, "STRING")
        self.assertEqual(toks[0].span, (0, 8))
        self.assertEqual(toks[0].value, "héllo")

    def test_multibyte_comment_shifts_spans(self):
        src = "# 注释\nx"          # '#' + ' ' + 3+3 bytes + '\n' = 9 bytes
        toks = lex(src)
        name = [t for t in toks if t.kind == "NAME"][0]
        self.assertEqual(name.span, (9, 10))
        self.assertEqual(src.encode("utf-8")[9:10], b"x")


class UnionMatchTest(unittest.TestCase):
    def test_basic_union(self):
        src = """\
union Shape = Circle(r) | Rect(w, h) | Point
match Rect(3, 4):
  | Point -> 0
  | Circle(r) -> 3 * r * r
  | Rect(w, h) -> w * h
"""
        value, diags = run_source(src)
        self.assertEqual(value, 12)
        self.assertEqual(diags, [])

    def test_zero_arg_constructor(self):
        src = """\
union U = A | B
match B:
  | A -> 1
  | B -> 2
"""
        value, diags = run_source(src)
        self.assertEqual(value, 2)
        self.assertEqual(diags, [])

    def test_nested_constructor_pattern(self):
        src = """\
union Tree = Leaf(v) | Node(l, r)
match Node(Leaf(1), Node(Leaf(2), Leaf(3))):
  | Node(Leaf(a), Node(Leaf(b), Leaf(c))) -> a + b + c
  | _ -> 0
"""
        value, diags = run_source(src)
        self.assertEqual(value, 6)
        self.assertEqual(diags, [])

    def test_union_value_equality_in_guard(self):
        src = """\
union U = A | B
match A:
  | x if x == A -> "is-a"
  | _ -> "other"
"""
        value, _ = run_source(src)
        self.assertEqual(value, "is-a")


class TupleLiteralTest(unittest.TestCase):
    def test_tuple_and_string_literal(self):
        src = """\
match (1, "hi"):
  | (0, _) -> "zero"
  | (1, s) if len(s) > 5 -> "long"
  | (1, s) -> "one:" + s
  | _ -> "other"
"""
        value, diags = run_source(src)
        self.assertEqual(value, "one:hi")
        self.assertEqual(diags, [])

    def test_single_element_tuple(self):
        src = """\
match (7,):
  | (x,) -> x * 2
  | _ -> 0
"""
        value, diags = run_source(src)
        self.assertEqual(value, 14)
        # (x,) already covers every 1-tuple, so the wildcard is unreachable
        self.assertEqual([d.message for d in diags],
                         ["case 2 is unreachable"])

    def test_bool_literals_exhaustive(self):
        src = """\
match true:
  | true -> 1
  | false -> 0
"""
        value, diags = run_source(src)
        self.assertEqual(value, 1)
        self.assertEqual(warnings(src), [])

    def test_bool_not_confused_with_int(self):
        src = """\
match true:
  | 1 -> "int-one"
  | true -> "bool-true"
  | _ -> "other"
"""
        value, _ = run_source(src)
        self.assertEqual(value, "bool-true")

    def test_none_literal_exhaustive(self):
        src = """\
match none:
  | none -> "nothing"
"""
        value, diags = run_source(src)
        self.assertEqual(value, "nothing")
        self.assertEqual(warnings(src), [])

    def test_int_literals_need_wildcard(self):
        src = """\
match 5:
  | 0 -> "zero"
  | 5 -> "five"
"""
        w = warnings(src)
        self.assertEqual(len(w), 1)
        self.assertIn("non-exhaustive", w[0].message)

    def test_int_literals_with_wildcard(self):
        src = """\
match 5:
  | 0 -> "zero"
  | 5 -> "five"
  | _ -> "many"
"""
        value, diags = run_source(src)
        self.assertEqual(value, "five")
        self.assertEqual(diags, [])

    def test_negative_int_literal_via_guard(self):
        src = """\
match -3:
  | x if x < 0 -> "neg"
  | _ -> "non-neg"
"""
        value, _ = run_source(src)
        self.assertEqual(value, "neg")


class UnreachableTest(unittest.TestCase):
    def test_wildcard_first_makes_later_case_unreachable(self):
        src = """\
union U = A | B
match A:
  | _ -> 1
  | A -> 2
"""
        w = warnings(src)
        self.assertEqual(len(w), 1)
        self.assertIn("unreachable", w[0].message)
        self.assertIn("case 2", w[0].message)
        value, _ = run_source(src)
        self.assertEqual(value, 1)

    def test_shadowed_literal_unreachable(self):
        src = """\
match 1:
  | 1 -> "one"
  | 1 -> "uno"
  | _ -> "other"
"""
        w = warnings(src)
        self.assertTrue(any("unreachable" in d.message for d in w))

    def test_unreachable_span_points_at_case(self):
        src = """\
union U = A | B
match A:
  | _ -> 1
  | A -> 2
"""
        w = warnings(src)
        frag = src.encode("utf-8")[w[0].span[0]:w[0].span[1]].decode("utf-8")
        self.assertTrue(frag.startswith("| A -> 2"))


class ExhaustivenessTest(unittest.TestCase):
    def test_missing_tag_reported_with_witness(self):
        src = """\
union U = A | B | C
match A:
  | A -> 1
  | B -> 2
"""
        w = warnings(src)
        self.assertEqual(len(w), 1)
        self.assertIn("non-exhaustive", w[0].message)
        self.assertIn("`C`", w[0].message)

    def test_witness_is_nested_pattern(self):
        src = """\
union Shape = Circle(r) | Rect(w, h) | Point
match Point:
  | Point -> 0
  | Circle(r) -> r
"""
        w = warnings(src)
        self.assertIn("`Rect(_, _)`", w[0].message)

    def test_guard_does_not_prove_exhaustiveness(self):
        src = """\
match 1:
  | _ if true -> 1
"""
        w = warnings(src)
        self.assertTrue(any("non-exhaustive" in d.message for d in w))

    def test_guarded_tag_branch_does_not_cover_tag(self):
        src = """\
union U = A | B
match B:
  | A if true -> 1
  | B -> 2
"""
        w = warnings(src)
        self.assertTrue(any("non-exhaustive" in d.message for d in w))
        self.assertTrue(any("`A`" in d.message for d in w))
        value, _ = run_source(src)
        self.assertEqual(value, 2)

    def test_guard_plus_unguarded_is_exhaustive(self):
        src = """\
union U = A | B
match A:
  | A if false -> 1
  | A -> 2
  | B -> 3
"""
        self.assertEqual(warnings(src), [])
        value, _ = run_source(src)
        self.assertEqual(value, 2)

    def test_guard_fallthrough_at_runtime(self):
        src = """\
match (1, "hi"):
  | (1, s) if len(s) > 5 -> "long"
  | (1, s) -> "short"
  | _ -> "other"
"""
        value, diags = run_source(src)
        self.assertEqual(value, "short")
        self.assertEqual(diags, [])

    def test_runtime_match_error(self):
        src = """\
union U = A | B
match B:
  | A -> 1
"""
        with self.assertRaises(MatchError):
            run_source(src)


class ValidationTest(unittest.TestCase):
    def test_duplicate_binding_nested_rejected(self):
        src = """\
match (1, (2,)):
  | (x, (x,)) -> x
  | _ -> 0
"""
        e = errors(src)
        self.assertEqual(len(e), 1)
        self.assertIn("duplicate binding 'x'", e[0].message)

    def test_duplicate_binding_in_constructor_rejected(self):
        src = """\
union P = Pair(a, b)
match Pair(1, 2):
  | Pair(y, y) -> y
"""
        e = errors(src)
        self.assertTrue(any("duplicate binding 'y'" in d.message for d in e))

    def test_duplicate_binding_span_is_utf8_bytes(self):
        src = "# 注释：多字节字符\nmatch (1, 2):\n  | (x, x) -> x\n"
        e = errors(src)
        self.assertEqual(len(e), 1)
        frag = src.encode("utf-8")[e[0].span[0]:e[0].span[1]].decode("utf-8")
        self.assertEqual(frag, "x")
        self.assertGreater(e[0].span[0], src.index("(x, x)"))

    def test_unknown_constructor_rejected(self):
        src = """\
union U = A
match A:
  | B -> 1
  | A -> 2
"""
        e = errors(src)
        self.assertTrue(any("unknown constructor 'B'" in d.message
                            for d in e))

    def test_arity_mismatch_rejected(self):
        src = """\
union U = Pair(a, b)
match Pair(1, 2):
  | Pair(x) -> x
"""
        e = errors(src)
        self.assertTrue(any("takes 2 field(s), got 1" in d.message
                            for d in e))

    def test_unbound_variable_rejected(self):
        src = """\
match 1:
  | _ -> y
"""
        e = errors(src)
        self.assertTrue(any("unbound variable 'y'" in d.message for d in e))

    def test_unknown_function_rejected(self):
        src = """\
match 1:
  | x if foo(x) -> 1
  | _ -> 0
"""
        e = errors(src)
        self.assertTrue(any("unknown function 'foo'" in d.message
                            for d in e))

    def test_syntax_error_reported_with_span(self):
        src = "match 1:\n  | _ 1\n"
        e = errors(src)
        self.assertEqual(len(e), 1)
        self.assertIn("expected '->'", e[0].message)


class ExprTest(unittest.TestCase):
    def test_arithmetic_precedence(self):
        src = """\
match 1:
  | _ -> 2 + 3 * 4 - 10 / 3
"""
        value, _ = run_source(src)
        self.assertEqual(value, 2 + 12 - 3)

    def test_logic_operators(self):
        src = """\
match 5:
  | x if x > 3 and not x == 4 or false -> "yes"
  | _ -> "no"
"""
        value, _ = run_source(src)
        self.assertEqual(value, "yes")

    def test_modulo_and_comparison(self):
        src = """\
match 7:
  | x if x % 2 == 1 -> "odd"
  | _ -> "even"
"""
        value, _ = run_source(src)
        self.assertEqual(value, "odd")


if __name__ == "__main__":
    unittest.main()
