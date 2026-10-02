import unittest

from parserlib import Grammar, ParseTable, TableConflictError
from parserlib import demolang


class TableTest(unittest.TestCase):
    def test_demo_grammar_is_conflict_free(self):
        table = ParseTable(demolang.program_grammar())
        self.assertEqual(table.get("statement", "id"), ("assign", ";"))
        self.assertEqual(table.get("statement", "print"), ("print_stmt", ";"))
        self.assertEqual(table.get("statement_list", "$"), ())
        self.assertEqual(table.get("expr_tail", ";"), ())
        self.assertIsNone(table.get("term", ";"))

    def test_expected_set(self):
        table = ParseTable(demolang.program_grammar())
        self.assertEqual(table.expected("term"), ("(", "id", "num"))
        self.assertEqual(table.expected("expr_tail"), (")", "+", ";"))

    def test_conflict_detected(self):
        grammar = Grammar(
            "s", {"s": [("a",), ("a", "b")]}, frozenset({"a", "b"})
        )
        with self.assertRaises(TableConflictError) as ctx:
            ParseTable(grammar)
        conflicts = ctx.exception.conflicts
        self.assertEqual(len(conflicts), 1)
        self.assertEqual(conflicts[0].nonterminal, "s")
        self.assertEqual(conflicts[0].terminal, "a")

    def test_duplicate_identical_production_is_not_a_conflict(self):
        grammar = Grammar("s", {"s": [("a",), ("a",)]}, frozenset({"a"}))
        ParseTable(grammar)  # must not raise


if __name__ == "__main__":
    unittest.main()
