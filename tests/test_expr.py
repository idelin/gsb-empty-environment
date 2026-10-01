import unittest

from invcheck import expr


class TestExpr(unittest.TestCase):
    def test_precedence(self):
        ast = expr.parse("a | b & c")
        self.assertEqual(ast[0], "or")
        self.assertEqual(ast[2][0], "and")

    def test_implication_right_assoc(self):
        ast = expr.parse("a -> b -> c")
        self.assertEqual(ast[0], "imp")
        self.assertEqual(ast[2][0], "imp")

    def test_iff_lowest(self):
        ast = expr.parse("a -> b <-> c")
        self.assertEqual(ast[0], "iff")

    def test_primed_names(self):
        ast = expr.parse("b0' & ~b1'")
        self.assertEqual(expr.ast_variables(ast), {"b0'", "b1'"})

    def test_keywords_and_constants(self):
        ast = expr.parse("true and not false or 1 xor 0")
        self.assertTrue(expr.eval_ast(ast, {}))

    def test_eval(self):
        ast = expr.parse("(a & b) | (~a & c)")
        self.assertTrue(expr.eval_ast(ast, {"a": True, "b": True}))
        self.assertFalse(expr.eval_ast(ast, {"a": True, "b": False, "c": True}))
        self.assertTrue(expr.eval_ast(ast, {"a": False, "c": True}))

    def test_parse_error(self):
        with self.assertRaises(expr.ParseError):
            expr.parse("a & & b")
        with self.assertRaises(expr.ParseError):
            expr.parse("a b")

    def test_cube_to_expr(self):
        self.assertEqual(expr.cube_to_expr({"b": True, "a": False}), "~a & b")
        self.assertEqual(expr.cube_to_expr({}), "1")


if __name__ == "__main__":
    unittest.main()
