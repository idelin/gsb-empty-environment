import unittest

from feature_resolver import expr
from feature_resolver.errors import ExprError


def active(*names):
    return lambda name: name in names


class ExprParseEvalTest(unittest.TestCase):
    def test_precedence_and_binds_tighter_than_or(self):
        node = expr.parse("a and b or c")
        self.assertTrue(expr.evaluate(node, active("c")))
        self.assertFalse(expr.evaluate(node, active("a")))
        self.assertTrue(expr.evaluate(node, active("a", "b")))

    def test_not_binds_tightest(self):
        node = expr.parse("not a and b")
        self.assertTrue(expr.evaluate(node, active("b")))
        self.assertFalse(expr.evaluate(node, active("a", "b")))

    def test_parentheses_override_precedence(self):
        node = expr.parse("a and (b or c)")
        self.assertFalse(expr.evaluate(node, active("a")))
        self.assertTrue(expr.evaluate(node, active("a", "c")))

    def test_double_negation(self):
        node = expr.parse("not not a")
        self.assertTrue(expr.evaluate(node, active("a")))
        self.assertFalse(expr.evaluate(node, active()))

    def test_names_collects_identifiers(self):
        node = expr.parse("net and not (minimal or debug)")
        self.assertEqual(expr.names(node), {"net", "minimal", "debug"})

    def test_hyphenated_feature_names(self):
        node = expr.parse("my-feature and other_2")
        self.assertTrue(expr.evaluate(node, active("my-feature", "other_2")))

    def test_render_roundtrip_semantics(self):
        node = expr.parse("a and (b or not c)")
        reparsed = expr.parse(expr.render(node))
        for combo in [(), ("a",), ("a", "b"), ("a", "c"), ("a", "b", "c")]:
            self.assertEqual(
                expr.evaluate(node, active(*combo)),
                expr.evaluate(reparsed, active(*combo)),
            )


class ExprErrorTest(unittest.TestCase):
    def test_invalid_expressions(self):
        for text in ["", "a and", "and a", "(a", "a)", "a b", "not", "a && b"]:
            with self.subTest(text=text):
                with self.assertRaises(ExprError):
                    expr.parse(text)


if __name__ == "__main__":
    unittest.main()
