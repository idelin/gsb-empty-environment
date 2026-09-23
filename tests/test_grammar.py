"""歧义/非法运算表必须在构造时拒绝。"""

import unittest

from exprlib import Grammar, GrammarError


class GrammarRejectionTest(unittest.TestCase):
    def assertRejected(self, operators, **kwargs):
        with self.assertRaises(GrammarError):
            Grammar.build(operators, **kwargs)

    def test_duplicate_symbol(self):
        self.assertRejected([("+", 1, "L"), ("+", 2, "L")])

    def test_reserved_symbol(self):
        self.assertRejected([("?", 1, "L")])
        self.assertRejected([(":", 1, "L")])
        self.assertRejected([("(", 1, "L")])
        self.assertRejected([(",", 1, "L")])

    def test_bad_precedence_or_assoc(self):
        self.assertRejected([("+", 0, "L")])
        self.assertRejected([("+", -1, "L")])
        self.assertRejected([("+", 1, "X")])
        self.assertRejected([("", 1, "L")])

    def test_mixed_associativity_same_level_is_ambiguous(self):
        # a + b * c 在同一级且结合性冲突，分组不唯一。
        self.assertRejected([("+", 2, "L"), ("*", 2, "R")])

    def test_left_assoc_tighter_than_unary_is_ambiguous(self):
        # '-' 既是一元又是二元左结合，若比一元层更紧，-a-b 归约不唯一。
        self.assertRejected(
            [("-", 5, "L"), ("+", 1, "L")],
            unary_symbol="-", unary_prec=3,
        )

    def test_right_assoc_tighter_than_unary_allowed(self):
        g = Grammar.build([("^", 4, "R"), ("+", 1, "L")],
                          unary_symbol="-", unary_prec=3)
        self.assertEqual(g.unary_prec, 3)

    def test_bad_unary_config(self):
        with self.assertRaises(GrammarError):
            Grammar.build([("+", 1, "L")], unary_symbol="?", unary_prec=3)
        with self.assertRaises(GrammarError):
            Grammar.build([("+", 1, "L")], unary_symbol="-", unary_prec=0)

    def test_custom_table_drives_parse_and_print(self):
        g = Grammar.build([("..", 1, "L"), ("++", 2, "R")],
                          unary_symbol="~", unary_prec=3)
        from exprlib import parse, to_string
        from exprlib.ast import BinOp, Var
        self.assertEqual(to_string(parse("a..b..c", g), g), "a .. b .. c")
        # 右结合：直接解析得到右嵌套，打印无括号；左嵌套树必须加括号。
        right_nested = BinOp("++", Var("a"), BinOp("++", Var("b"), Var("c")))
        self.assertEqual(parse("a++b++c", g), right_nested)
        self.assertEqual(to_string(right_nested, g), "a ++ b ++ c")
        left_nested = BinOp("++", BinOp("++", Var("a"), Var("b")), Var("c"))
        self.assertEqual(to_string(left_nested, g), "(a ++ b) ++ c")
        self.assertEqual(parse("(a++b)++c", g), left_nested)
        self.assertEqual(to_string(parse("~a++b", g), g), "~a ++ b")


if __name__ == "__main__":
    unittest.main()
