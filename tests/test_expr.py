import unittest

from invcheck import expr


class TestExpr(unittest.TestCase):
    def test_precedence(self):
        self.assertEqual(
            expr.parse("a | b & c"),
            ("or", ("var", "a"), ("and", ("var", "b"), ("var", "c"))),
        )
        self.assertEqual(
            expr.parse("!a & b"),
            ("and", ("not", ("var", "a")), ("var", "b")),
        )

    def test_implication_right_associative(self):
        self.assertEqual(
            expr.parse("a -> b -> c"),
            ("implies", ("var", "a"), ("implies", ("var", "b"), ("var", "c"))),
        )

    def test_constants_and_keywords(self):
        self.assertEqual(expr.parse("true"), ("const", True))
        self.assertEqual(expr.parse("0"), ("const", False))
        self.assertEqual(
            expr.parse("a and not b"), ("and", ("var", "a"), ("not", ("var", "b")))
        )

    def test_eval(self):
        ast = expr.parse("(a | b) & !(a & b)")
        self.assertTrue(expr.eval_ast(ast, {"a": True, "b": False}))
        self.assertFalse(expr.eval_ast(ast, {"a": True, "b": True}))

    def test_parse_errors(self):
        for bad in ("a &", "& a", "a b", "(a", "a -> -> b", "2"):
            with self.assertRaises(expr.ParseError, msg=bad):
                expr.parse(bad)

    def test_ast_vars(self):
        self.assertEqual(expr.ast_vars(expr.parse("a & (b -> a)")), {"a", "b"})


if __name__ == "__main__":
    unittest.main()
