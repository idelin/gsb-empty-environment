import unittest

from errparse.demo_grammar import make_grammar
from errparse.grammar import (EPS, Grammar, TableConflict, build_table,
                              compute_first, compute_follow)
from errparse.tokens import EOF_SYMBOL


class FirstFollowTest(unittest.TestCase):
    def setUp(self):
        self.g = make_grammar()
        self.first = compute_first(self.g)
        self.follow = compute_follow(self.g, self.first)

    def test_first_sets(self):
        self.assertEqual(self.first["term"], {"ID", "NUM", "LPAREN"})
        self.assertEqual(self.first["stmt"], {"ID", "PRINT", "IF"})
        self.assertIn(EPS, self.first["expr_tail"])
        self.assertIn(EPS, self.first["stmt_list"])

    def test_follow_sets(self):
        self.assertEqual(self.follow["stmt"], {"ID", "PRINT", "IF", EOF_SYMBOL})
        self.assertEqual(self.follow["expr"],
                         {"SEMI", "ID", "PRINT", "IF", "RPAREN"})
        self.assertIn(EOF_SYMBOL, self.follow["program"])

    def test_table_is_conflict_free(self):
        table = build_table(self.g)
        self.assertEqual(table["expr_tail"]["PLUS"], ("PLUS", "term", "expr_tail"))
        self.assertEqual(table["stmt_list"][EOF_SYMBOL], ())
        self.assertEqual(table["stmt"]["IF"], ("IF", "expr", "stmt"))

    def test_left_recursion_rejected(self):
        g = Grammar("e", {"e": [("e", "PLUS", "t"), ("t",)], "t": [("NUM",)]})
        with self.assertRaises(TableConflict):
            build_table(g)

    def test_dangling_else_rejected(self):
        g = Grammar("s", {
            "s": [("IF", "x", "s"), ("IF", "x", "s", "ELSE", "s"), ("a",)],
            "x": [("b",)],
        })
        with self.assertRaises(TableConflict):
            build_table(g)


if __name__ == "__main__":
    unittest.main()
