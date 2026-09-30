import unittest

from regexlite import (
    Alt, CharClass, Concat, Dot, Empty, Group, Literal, ParseError, Star,
    parse,
)


class TestStructure(unittest.TestCase):
    def test_concat_alt_precedence(self):
        # ab|c* parses as Alt(Concat(a, b), Star(c))
        ast = parse("ab|c*").ast
        self.assertIsInstance(ast, Alt)
        left, right = ast.branches
        self.assertIsInstance(left, Concat)
        self.assertIsInstance(right, Star)
        self.assertIsInstance(right.child, Literal)

    def test_star_binds_tighter_than_concat(self):
        ast = parse("ab*").ast
        self.assertIsInstance(ast, Concat)
        self.assertIsInstance(ast.parts[1], Star)
        self.assertEqual(ast.parts[1].child.char, "b")

    def test_nested_star_parses(self):
        ast = parse("(a*)*").ast
        self.assertIsInstance(ast, Star)
        self.assertIsInstance(ast.child, Group)
        self.assertIsInstance(ast.child.child, Star)

    def test_double_star(self):
        ast = parse("a**").ast
        self.assertIsInstance(ast, Star)
        self.assertIsInstance(ast.child, Star)

    def test_empty_pattern_is_empty_node(self):
        self.assertIsInstance(parse("").ast, Empty)

    def test_empty_alternative_branch(self):
        ast = parse("a|").ast
        self.assertIsInstance(ast, Alt)
        self.assertIsInstance(ast.branches[1], Empty)

    def test_empty_group(self):
        ast = parse("()").ast
        self.assertIsInstance(ast, Group)
        self.assertIsInstance(ast.child, Empty)

    def test_empty_class_is_empty_language(self):
        ast = parse("[]").ast
        self.assertIsInstance(ast, CharClass)
        self.assertEqual(ast.ranges, ())
        self.assertFalse(ast.negated)

    def test_negated_empty_class(self):
        ast = parse("[^]").ast
        self.assertEqual(ast.ranges, ())
        self.assertTrue(ast.negated)

    def test_class_ranges_and_negation(self):
        ast = parse("[^a-cx]").ast
        self.assertEqual(ast.ranges, (("a", "c"), ("x", "x")))
        self.assertTrue(ast.negated)

    def test_trailing_dash_is_literal(self):
        ast = parse("[a-]").ast
        self.assertEqual(ast.ranges, (("a", "a"), ("-", "-")))

    def test_escapes(self):
        ast = parse(r"\*\n").ast
        self.assertIsInstance(ast, Concat)
        self.assertEqual(ast.parts[0].char, "*")
        self.assertEqual(ast.parts[1].char, "\n")

    def test_dot(self):
        self.assertIsInstance(parse(".").ast, Dot)


class TestLabels(unittest.TestCase):
    def test_numbered_labels(self):
        parsed = parse("(a)(b)")
        self.assertEqual(parsed.labels, {"1": 0, "2": 1})

    def test_named_labels_angle_and_python_style(self):
        parsed = parse("(?<x>a)(?P<y>b)")
        self.assertEqual(parsed.labels, {"x": 0, "y": 1})

    def test_mixed_numbering_follows_open_paren_order(self):
        parsed = parse("(?<outer>a(b)c)")
        self.assertEqual(parsed.labels, {"outer": 0, "2": 1})

    def test_duplicate_label_rejected(self):
        with self.assertRaises(ParseError):
            parse("(?<x>a)(?<x>b)")


class TestSourcePositions(unittest.TestCase):
    def test_literal_spans(self):
        ast = parse("abc").ast
        self.assertEqual([p.span for p in ast.parts], [(0, 1), (1, 2), (2, 3)])

    def test_group_and_star_spans(self):
        ast = parse("a(bc)*").ast
        star = ast.parts[1]
        self.assertEqual(star.span, (1, 6))
        self.assertEqual(star.child.span, (1, 5))

    def test_alt_span(self):
        ast = parse("a|bc").ast
        self.assertEqual(ast.span, (0, 4))


class TestErrors(unittest.TestCase):
    def assert_error_at(self, pattern, pos):
        with self.assertRaises(ParseError) as ctx:
            parse(pattern)
        self.assertEqual(ctx.exception.pos, pos, pattern)

    def test_unclosed_group(self):
        self.assert_error_at("(a", 0)

    def test_stray_close_paren(self):
        self.assert_error_at("a)", 1)

    def test_leading_star(self):
        self.assert_error_at("*a", 0)

    def test_unterminated_class(self):
        self.assert_error_at("[ab", 0)

    def test_bad_range(self):
        self.assert_error_at("[z-a]", 0)

    def test_trailing_backslash(self):
        self.assert_error_at("a\\", 2)

    def test_bad_group_syntax(self):
        self.assert_error_at("(?x)", 0)


if __name__ == "__main__":
    unittest.main()
