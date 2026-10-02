import unittest

from parserlib import Grammar, GrammarError
from parserlib import demolang


class GrammarTest(unittest.TestCase):
    def setUp(self):
        self.grammar = demolang.program_grammar()

    def test_first_sets(self):
        first = self.grammar.first_sets()
        self.assertEqual(first["term"], {"id", "num", "("})
        self.assertEqual(first["expr"], {"id", "num", "("})
        self.assertEqual(first["statement"], {"id", "print"})
        self.assertEqual(first["expr_tail"], {"+"})

    def test_nullable(self):
        nullable = self.grammar.nullable_set()
        self.assertIn("statement_list", nullable)
        self.assertIn("expr_tail", nullable)
        self.assertIn("program", nullable)
        self.assertNotIn("term", nullable)
        self.assertNotIn("statement", nullable)

    def test_follow_sets(self):
        follow = self.grammar.follow_sets()
        self.assertEqual(follow["statement"], {"id", "print", "$"})
        self.assertEqual(follow["statement_list"], {"$"})
        self.assertEqual(follow["expr"], {";", ")"})
        self.assertEqual(follow["expr_tail"], {";", ")"})
        self.assertEqual(follow["term"], {"+", ";", ")"})

    def test_unknown_symbol_rejected(self):
        with self.assertRaises(GrammarError):
            Grammar("s", {"s": [("missing",)]}, frozenset({"a"}))

    def test_terminal_nonterminal_overlap_rejected(self):
        with self.assertRaises(GrammarError):
            Grammar("s", {"s": [("a",)]}, frozenset({"s"}))

    def test_left_recursion_rejected(self):
        with self.assertRaises(GrammarError):
            Grammar("s", {"s": [("s", "a"), ("b",)]}, frozenset({"a", "b"}))

    def test_indirect_left_recursion_rejected(self):
        rules = {"s": [("t", "a")], "t": [("s", "b"), ("c",)]}
        with self.assertRaises(GrammarError):
            Grammar("s", rules, frozenset({"a", "b", "c"}))

    def test_unit_cycle_rejected(self):
        rules = {"s": [("t",)], "t": [("s",), ("c",)]}
        with self.assertRaises(GrammarError):
            Grammar("s", rules, frozenset({"c"}))


if __name__ == "__main__":
    unittest.main()
